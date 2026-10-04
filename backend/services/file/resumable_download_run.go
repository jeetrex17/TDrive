package file

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	tdcrypto "TDrive/backend/crypto"
	"TDrive/backend/projection"
	"TDrive/backend/tgclient"
	"golang.org/x/sync/errgroup"
)

var errDownloadSourceChanged = errors.New("download source changed")
var errDownloadUnlockRequired = errors.New("download vault is locked")
var errDownloadDestinationChanged = errors.New("download destination changed")

type downloadBlock struct {
	part   int
	index  int64
	remote int64
	local  int64
	length int
}

func (s *Service) runResumableDownload(ctx context.Context, job downloadJob) DownloadResult {
	s.downloadResumeMu.Lock()
	if s.downloadRuns == nil {
		s.downloadRuns = make(map[string]*downloadRun)
	}
	if s.downloadDiscards[job.ID] {
		s.downloadResumeMu.Unlock()
		return downloadFailure("error", "Download is being discarded", job.ID, fmt.Errorf("download is being discarded"))
	}
	if s.downloadRuns[job.ID] != nil {
		s.downloadResumeMu.Unlock()
		return downloadFailure("error", "Download is already running", job.ID, fmt.Errorf("download already running"))
	}
	runCtx, cancel := context.WithCancel(ctx)
	run := &downloadRun{cancel: cancel, done: make(chan struct{})}
	s.downloadRuns[job.ID] = run
	s.downloadResumeMu.Unlock()
	defer func() {
		cancel()
		s.downloadResumeMu.Lock()
		delete(s.downloadRuns, job.ID)
		s.downloadResumeMu.Unlock()
		close(run.done)
	}()
	// A location change may have committed between ResumeDownload's lookup and
	// this claim. Always use the journal row after the claim is exclusive.
	job, err := s.loadDownloadJob(runCtx, job.ID)
	if err != nil {
		return downloadFailure("error", "Download job not found", job.ID, err)
	}
	if job.Status == downloadCompleted {
		valid, err := publishedDownloadMatches(job)
		if err != nil {
			return downloadFailure("error", "Unable to verify saved download", job.ID, err)
		}
		if valid {
			return DownloadResult{Status: "success", Message: "Download complete", SavedPath: job.Destination, JobID: job.ID}
		}
		// The saved output disappeared or changed after completion. The private
		// stage may have been removed, so recovery can fetch from Telegram again.
		status := downloadPaused
		if _, statErr := os.Lstat(job.Destination); statErr == nil || job.DestExists {
			status = downloadNeedsDestination
		} else if !errors.Is(statErr, os.ErrNotExist) {
			return downloadFailure("error", "Unable to inspect saved download", job.ID, statErr)
		}
		if err := s.setDownloadStatus(runCtx, &job, status, "Saved file changed; choose a location to download again", 0); err != nil {
			return downloadFailure("error", "Unable to save download state", job.ID, err)
		}
		if status == downloadNeedsDestination {
			return downloadFailure("error", "Saved file changed; choose a location to download again", job.ID, errDownloadDestinationChanged)
		}
	}
	if job.Status == downloadSaving && job.OutputHash != "" {
		complete, err := s.reconcilePublishedDownload(runCtx, &job)
		if err != nil {
			if errors.Is(err, errDownloadDestinationChanged) {
				statusCtx, cancelStatus := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancelStatus()
				confirmed, _ := s.confirmedDownloadBytes(statusCtx, job.ID)
				if statusErr := s.setDownloadStatus(statusCtx, &job, downloadNeedsDestination, err.Error(), confirmed); statusErr != nil {
					return downloadFailure("error", "Unable to save download state", job.ID, errors.Join(err, statusErr))
				}
			}
			return downloadFailure("error", "Unable to reconcile saved download", job.ID, err)
		}
		if complete {
			return DownloadResult{Status: "success", Message: "Download complete", SavedPath: job.Destination, JobID: job.ID}
		}
	}

	confirmed, err := s.confirmedDownloadBytes(runCtx, job.ID)
	if err != nil {
		return downloadFailure("error", "Unable to read download progress", job.ID, err)
	}
	confirmed = min(max(confirmed, 0), job.File.StoredSize)
	if err := s.setDownloadStatus(runCtx, &job, downloadVerifying, "", confirmed); err != nil {
		return downloadFailure("error", "Unable to save download state", job.ID, err)
	}
	verified, err := s.transferResumableDownload(runCtx, &job)
	if err == nil {
		// Keep the completed record until the UI and Android export reconcile it.
		_ = os.Remove(job.StagePath)
		return DownloadResult{Status: "success", Message: "Download complete", SavedPath: job.Destination, JobID: job.ID}
	}
	status := downloadError
	resultStatus := "error"
	switch {
	case runCtx.Err() != nil:
		status, resultStatus = downloadPaused, "canceled"
	case errors.Is(err, errDownloadSourceChanged):
		status = downloadSourceChanged
	case errors.Is(err, errDownloadUnlockRequired):
		status = downloadWaitingUnlock
	case errors.Is(err, errDownloadDestinationChanged):
		status = downloadNeedsDestination
	case tgclient.IsTransientTransport(err):
		status = downloadWaitingNetwork
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		// gotd can cancel its own connection scope while the caller remains
		// live. That is a reconnectable transport interruption, not a pause.
		status = downloadWaitingNetwork
	case errors.Is(err, os.ErrPermission), errors.Is(err, os.ErrNotExist):
		status = downloadNeedsDestination
	}
	// The canceled operation context cannot write the durable paused state.
	statusCtx, statusCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer statusCancel()
	if job.Status == downloadSaving && job.OutputHash != "" {
		// A publication failure may still have followed a successful rename.
		// Reconcile before reporting it. If the journal is unavailable, retain
		// the saving intent for recovery on the next process start.
		complete, reconcileErr := s.reconcilePublishedDownload(statusCtx, &job)
		if reconcileErr != nil {
			if errors.Is(reconcileErr, errDownloadDestinationChanged) {
				if statusErr := s.setDownloadStatus(statusCtx, &job, downloadNeedsDestination, reconcileErr.Error(), verified); statusErr != nil {
					return downloadFailure("error", "Unable to save download state", job.ID, errors.Join(err, reconcileErr, statusErr))
				}
			}
			return downloadFailure("error", "Unable to reconcile saved download", job.ID, errors.Join(err, reconcileErr))
		}
		if complete {
			return DownloadResult{Status: "success", Message: "Download complete", SavedPath: job.Destination, JobID: job.ID}
		}
	}
	if statusErr := s.setDownloadStatus(statusCtx, &job, status, err.Error(), verified); statusErr != nil {
		return downloadFailure("error", "Unable to save download state", job.ID, errors.Join(err, statusErr))
	}
	return downloadFailure(resultStatus, err.Error(), job.ID, err)
}

func (s *Service) transferResumableDownload(ctx context.Context, job *downloadJob) (int64, error) {
	current, found, err := projection.FileDownloadRefContext(ctx, s.DB, job.ChannelID, job.File.LogicalMsgID)
	if err != nil {
		return 0, err
	}
	if !found || !reflect.DeepEqual(current, job.File) {
		return 0, errDownloadSourceChanged
	}
	if s.Peers == nil || s.TG == nil {
		return 0, fmt.Errorf("download connection unavailable")
	}
	peer, err := s.Peers.ResolvePeer(ctx, job.ChannelID)
	if err != nil {
		return 0, err
	}
	ranges, ok := s.TG.(tgclient.RangeClient)
	if !ok {
		return 0, fmt.Errorf("download client does not support ranged reads")
	}
	stage, err := s.openOrResetDownloadStage(ctx, job.StagePath, job.ID)
	if err != nil {
		return 0, fmt.Errorf("open partial download: %w", err)
	}
	defer stage.Close()
	blockBytes := s.downloadBlockBytes
	if blockBytes == 0 {
		blockBytes = tgclient.RangeReadMaxBytes
	}
	blocks, messages, err := planDownloadBlocks(job.File, blockBytes)
	if err != nil {
		return 0, err
	}
	committed, err := s.validDownloadBlocks(ctx, job.ID, stage, blocks)
	if err != nil {
		return 0, err
	}
	var verified int64
	var pending []downloadBlock
	for _, block := range blocks {
		if committed[block] {
			verified += int64(block.length)
		} else {
			pending = append(pending, block)
		}
	}
	verified = min(verified, job.File.StoredSize)
	if len(pending) > 0 {
		if err := s.setDownloadStatus(ctx, job, downloadDownloading, "", verified); err != nil {
			return verified, err
		}
		refs := make([]tgclient.DocumentRef, len(messages))
		for i, message := range messages {
			ref, err := ranges.ResolveDocument(ctx, peer, message)
			if err != nil {
				return verified, fmt.Errorf("resolve download part %d: %w", i, err)
			}
			if ref.Size != downloadPartSize(job.File, i) {
				return verified, errDownloadSourceChanged
			}
			refs[i] = ref
		}
		var refMu sync.Mutex
		var done atomic.Int64
		done.Store(verified)
		var next atomic.Int64
		group, groupCtx := errgroup.WithContext(ctx)
		concurrency := s.downloadConcurrency
		if concurrency <= 0 {
			concurrency = 3
		}
		for range min(concurrency, len(pending)) {
			group.Go(func() error {
				buf := make([]byte, tgclient.RangeReadMaxBytes)
				for {
					const checkpointBlocks = 8
					first := int(next.Add(checkpointBlocks) - checkpointBlocks)
					if first >= len(pending) {
						return nil
					}
					batch := pending[first:min(first+checkpointBlocks, len(pending))]
					hashes := make([]string, 0, len(batch))
					var batchBytes int64
					for _, block := range batch {
						refMu.Lock()
						ref := refs[block.part]
						refMu.Unlock()
						data := buf[:block.length]
						if err := s.readDownloadBlock(groupCtx, ranges, &ref, peer, block.remote, data); err != nil {
							return err
						}
						refMu.Lock()
						refs[block.part] = ref
						refMu.Unlock()
						if n, err := stage.WriteAt(data, block.local); err != nil {
							return fmt.Errorf("write partial download: %w", err)
						} else if n != len(data) {
							return io.ErrShortWrite
						}
						checksum := sha256.Sum256(data)
						hashes = append(hashes, hex.EncodeToString(checksum[:]))
						batchBytes += int64(block.length)
					}
					if err := stage.Sync(); err != nil {
						return fmt.Errorf("sync partial download: %w", err)
					}
					tx, err := s.DB.BeginTx(groupCtx, nil)
					if err != nil {
						return fmt.Errorf("begin download checkpoint: %w", err)
					}
					for i, block := range batch {
						if _, err := tx.ExecContext(groupCtx, `INSERT OR REPLACE INTO resumable_download_blocks
							(job_id, part_index, block_index, byte_count, sha256) VALUES (?, ?, ?, ?, ?)`,
							job.ID, block.part, block.index, block.length, hashes[i]); err != nil {
							_ = tx.Rollback()
							return fmt.Errorf("save downloaded block: %w", err)
						}
					}
					if err := tx.Commit(); err != nil {
						return fmt.Errorf("commit download checkpoint: %w", err)
					}
					count := min(done.Add(batchBytes), job.File.StoredSize)
					s.emitEvent("download_resume_progress", job.ID, count, job.File.StoredSize)
				}
			})
		}
		if err := group.Wait(); err != nil {
			return done.Load(), err
		}
		verified = done.Load()
	}
	if err := ctx.Err(); err != nil {
		return verified, err
	}
	if err := stage.Truncate(job.File.StoredSize); err != nil {
		return verified, err
	}
	if err := stage.Sync(); err != nil {
		return verified, err
	}
	if err := s.setDownloadStatus(ctx, job, downloadVerifying, "", verified); err != nil {
		return verified, err
	}
	if err := s.publishResumableDownload(ctx, job, stage, verified); err != nil {
		return verified, err
	}
	return verified, nil
}

func openDownloadStage(path string) (*os.File, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("partial download is not a regular file")
	}
	return os.OpenFile(path, os.O_RDWR, 0)
}

func (s *Service) openOrResetDownloadStage(ctx context.Context, path, jobID string) (*os.File, error) {
	stage, err := openDownloadStage(path)
	if !errors.Is(err, os.ErrNotExist) {
		return stage, err
	}
	// A missing private stage cannot make old block receipts trustworthy.
	if _, err := s.DB.ExecContext(ctx, `DELETE FROM resumable_download_blocks WHERE job_id = ?`, jobID); err != nil {
		return nil, err
	}
	return os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0o600)
}

func planDownloadBlocks(file projection.DownloadFile, blockBytes int64) ([]downloadBlock, []int64, error) {
	if blockBytes <= 0 || blockBytes > tgclient.RangeReadMaxBytes || blockBytes%tgclient.RangeReadAlignment != 0 || tgclient.RangeReadMaxBytes%blockBytes != 0 {
		return nil, nil, fmt.Errorf("invalid download block size")
	}
	if file.StoredSize < 0 || file.StoredSize > LargeFileMaxBytes || len(file.Parts) > MaxParts {
		return nil, nil, fmt.Errorf("download exceeds supported size or part count")
	}
	parts := file.Parts
	if len(parts) == 0 {
		message := file.ContentMsgID
		if message <= 0 {
			message = file.LogicalMsgID
		}
		parts = []projection.FilePart{{PartIndex: 0, MsgID: message, Size: file.StoredSize}}
	}
	var blocks []downloadBlock
	messages := make([]int64, 0, len(parts))
	var base int64
	for i, part := range parts {
		if part.MsgID <= 0 || part.Size < 0 || (len(file.Parts) > 0 && part.PartIndex != i) {
			return nil, nil, fmt.Errorf("invalid download part metadata")
		}
		messages = append(messages, part.MsgID)
		for offset := int64(0); offset < part.Size; offset += blockBytes {
			length := int(min(blockBytes, part.Size-offset))
			blocks = append(blocks, downloadBlock{part: i, index: offset / blockBytes,
				remote: offset, local: base + offset, length: length})
		}
		base += part.Size
	}
	if base != file.StoredSize {
		return nil, nil, fmt.Errorf("invalid download size metadata")
	}
	return blocks, messages, nil
}

func downloadPartSize(file projection.DownloadFile, part int) int64 {
	if len(file.Parts) == 0 {
		return file.StoredSize
	}
	return file.Parts[part].Size
}

// validDownloadBlocks verifies every journaled block against the private stage.
// A torn or edited block is forgotten and fetched again instead of trusted.
func (s *Service) validDownloadBlocks(ctx context.Context, jobID string, stage *os.File, planned []downloadBlock) (map[downloadBlock]bool, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT part_index, block_index, byte_count, sha256
		FROM resumable_download_blocks WHERE job_id = ?`, jobID)
	if err != nil {
		return nil, err
	}
	type record struct {
		part   int
		index  int64
		length int
		hash   string
	}
	var records []record
	for rows.Next() {
		var r record
		if err := rows.Scan(&r.part, &r.index, &r.length, &r.hash); err != nil {
			_ = rows.Close()
			return nil, err
		}
		records = append(records, r)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, err
	}
	_ = rows.Close()
	lookup := make(map[[2]int64]downloadBlock, len(planned))
	for _, block := range planned {
		lookup[[2]int64{int64(block.part), block.index}] = block
	}
	valid := make(map[downloadBlock]bool, len(records))
	buf := make([]byte, tgclient.RangeReadMaxBytes)
	for _, r := range records {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		block, exists := lookup[[2]int64{int64(r.part), r.index}]
		if exists && block.length == r.length {
			if _, err := stage.ReadAt(buf[:r.length], block.local); err == nil {
				hash := sha256.Sum256(buf[:r.length])
				valid[block] = hex.EncodeToString(hash[:]) == r.hash
			}
		}
		if !valid[block] {
			if _, err := s.DB.ExecContext(ctx, `DELETE FROM resumable_download_blocks
				WHERE job_id = ? AND part_index = ? AND block_index = ?`, jobID, r.part, r.index); err != nil {
				return nil, err
			}
		}
	}
	return valid, nil
}

func (s *Service) readDownloadBlock(ctx context.Context, client tgclient.RangeClient, ref *tgclient.DocumentRef, peer tgclient.InputPeer, offset int64, dst []byte) error {
	return s.sendRetryPolicy().Do(ctx, func() error {
		n, err := client.ReadDocumentRange(ctx, *ref, offset, dst)
		if tgclient.IsFileReferenceError(err) {
			fresh, resolveErr := client.ResolveDocument(ctx, peer, ref.MsgID)
			if resolveErr != nil {
				return resolveErr
			}
			if fresh.Size != ref.Size || fresh.DocumentID != ref.DocumentID {
				return errDownloadSourceChanged
			}
			*ref = fresh
			n, err = client.ReadDocumentRange(ctx, *ref, offset, dst)
		}
		if err != nil {
			return err
		}
		if n != len(dst) {
			return io.ErrUnexpectedEOF
		}
		return nil
	})
}

func (s *Service) publishResumableDownload(ctx context.Context, job *downloadJob, stage *os.File, verified int64) error {
	if err := checkDownloadDestination(*job); err != nil {
		return err
	}
	outputPath := filepath.Join(filepath.Dir(job.Destination), ".tdrive-download-"+job.ID+".tmp")
	if info, err := os.Lstat(outputPath); err == nil {
		if !info.Mode().IsRegular() {
			return fmt.Errorf("download output staging path is not a regular file")
		}
		if err := os.Remove(outputPath); err != nil {
			return err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	output, err := os.OpenFile(outputPath, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0o600)
	if err != nil {
		return fmt.Errorf("prepare downloaded file: %w", err)
	}
	defer os.Remove(outputPath)
	defer output.Close()
	if _, err := stage.Seek(0, io.SeekStart); err != nil {
		return err
	}
	hash := sha256.New()
	verifiedOutput := io.MultiWriter(output, hash)
	if job.File.Encrypted {
		key, err := s.requireEncryptionKey(true)
		defer clearOwnedKey(key)
		if err != nil {
			return errors.Join(errDownloadUnlockRequired, err)
		}
		if _, err := tdcrypto.DecryptStream(io.LimitReader(stage, job.File.StoredSize), verifiedOutput, key); err != nil {
			return fmt.Errorf("verify encrypted download: %w", err)
		}
	} else {
		if _, err := io.CopyN(verifiedOutput, &contextReader{ctx: ctx, source: stage}, job.File.StoredSize); err != nil {
			return fmt.Errorf("copy downloaded file: %w", err)
		}
	}
	if err := verifyOpenFileSize(output, job.File.OutputSize); err != nil {
		return fmt.Errorf("verify downloaded file: %w", err)
	}
	outputHash := hex.EncodeToString(hash.Sum(nil))
	if !job.File.Encrypted {
		if expected, ok := trustedDownloadHash(job.File.ContentHash); ok && outputHash != expected {
			return fmt.Errorf("download content checksum mismatch")
		}
	}
	if err := output.Sync(); err != nil {
		return err
	}
	if err := output.Close(); err != nil {
		return err
	}
	if err := checkDownloadDestination(*job); err != nil {
		return err
	}
	if _, err := s.DB.ExecContext(ctx, `UPDATE resumable_downloads SET status = ?, detail = '', output_sha256 = ?
		WHERE job_id = ? AND account_namespace = ?`, downloadSaving, outputHash, job.ID, s.CacheNamespace); err != nil {
		return err
	}
	job.OutputHash = outputHash
	job.Status = downloadSaving
	s.emitDownloadChanged(*job, verified)
	if s.afterDownloadSaving != nil {
		if err := s.afterDownloadSaving(); err != nil {
			return err
		}
	}
	if err := publishResumableOutput(*job, outputPath); err != nil {
		return fmt.Errorf("publish downloaded file: %w", err)
	}
	if err := syncDownloadDirectory(filepath.Dir(job.Destination)); err != nil {
		return fmt.Errorf("sync download destination: %w", err)
	}
	if err := s.setDownloadStatus(ctx, job, downloadCompleted, "", verified); err != nil {
		return fmt.Errorf("record completed download: %w", err)
	}
	_ = os.Remove(downloadBackupPath(*job))
	return nil
}

func downloadBackupPath(job downloadJob) string {
	return filepath.Join(filepath.Dir(job.Destination), ".tdrive-download-"+job.ID+".backup")
}

func publishResumableOutput(job downloadJob, outputPath string) error {
	backup := downloadBackupPath(job)
	if _, err := os.Lstat(backup); err == nil {
		return fmt.Errorf("%w: previous file backup remains at %s; choose another location",
			errDownloadDestinationChanged, backup)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.Rename(outputPath, job.Destination); err == nil {
		return nil
	} else if !job.DestExists {
		return err
	}
	if err := os.Rename(job.Destination, backup); err != nil {
		return err
	}
	if err := os.Rename(outputPath, job.Destination); err != nil {
		return errors.Join(err, os.Rename(backup, job.Destination))
	}
	return nil
}

func checkDownloadDestination(job downloadJob) error {
	info, err := os.Lstat(job.Destination)
	if errors.Is(err, os.ErrNotExist) {
		if job.DestExists {
			return fmt.Errorf("%w: destination was removed", errDownloadDestinationChanged)
		}
		return nil
	}
	if err != nil {
		return err
	}
	if !job.DestExists || !info.Mode().IsRegular() || info.Size() != job.DestSize || info.ModTime().UnixNano() != job.DestMTimeNS {
		return fmt.Errorf("%w: destination was replaced", errDownloadDestinationChanged)
	}
	return nil
}

func trustedDownloadHash(reference string) (string, bool) {
	hexHash := strings.TrimPrefix(reference, "sha256:")
	if len(hexHash) != sha256.Size*2 {
		return "", false
	}
	_, err := hex.DecodeString(hexHash)
	return strings.ToLower(hexHash), err == nil
}

func hashDownloadOutput(file *os.File) (string, error) {
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return "", err
	}
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func publishedDownloadMatches(job downloadJob) (bool, error) {
	if job.OutputHash == "" {
		return false, nil
	}
	file, err := os.Open(job.Destination)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return false, err
	}
	if !info.Mode().IsRegular() || info.Size() != job.File.OutputSize {
		return false, nil
	}
	hash, err := hashDownloadOutput(file)
	return hash == job.OutputHash, err
}

func syncDownloadDirectory(dir string) error {
	if runtime.GOOS == "windows" {
		return nil // Windows does not provide a portable directory fsync.
	}
	f, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}

// A crash may occur after the atomic destination rename and before the
// completed row commits. A durable digest of the output lets recovery prove
// that this job published the destination rather than overwriting it again.
func (s *Service) reconcilePublishedDownload(ctx context.Context, job *downloadJob) (bool, error) {
	backup := downloadBackupPath(*job)
	if _, err := os.Lstat(job.Destination); errors.Is(err, os.ErrNotExist) {
		if _, backupErr := os.Lstat(backup); backupErr == nil {
			// The process died after moving the previous destination aside.
			if err := os.Rename(backup, job.Destination); err != nil {
				return false, err
			}
		} else if !errors.Is(backupErr, os.ErrNotExist) {
			return false, backupErr
		}
	} else if err != nil {
		return false, err
	}
	match, err := publishedDownloadMatches(*job)
	if err != nil {
		return false, err
	}
	if !match {
		if _, err := os.Lstat(backup); err == nil {
			return false, fmt.Errorf("%w: previous file backup remains at %s; choose another location",
				errDownloadDestinationChanged, backup)
		} else if !errors.Is(err, os.ErrNotExist) {
			return false, err
		}
		return false, nil
	}
	if err := syncDownloadDirectory(filepath.Dir(job.Destination)); err != nil {
		return false, err
	}
	if err := s.setDownloadStatus(ctx, job, downloadCompleted, "", job.File.StoredSize); err != nil {
		return false, err
	}
	_ = os.Remove(backup)
	_ = os.Remove(job.StagePath)
	return true, nil
}
