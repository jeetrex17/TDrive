package file

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"TDrive/backend/projection"
	"TDrive/backend/tgclient"
)

var errResumablePartMismatch = errors.New("upload part bytes differ from original source")

type partHashWriter struct {
	writer io.Writer
	count  int64
	limit  int64
}

func (w *partHashWriter) Write(p []byte) (int, error) {
	if int64(len(p)) > w.limit-w.count {
		return 0, fmt.Errorf("upload part exceeds expected size")
	}
	n, err := w.writer.Write(p)
	w.count += int64(n)
	return n, err
}

func (s *Service) verifyRemotePart(ctx context.Context, peer tgclient.InputPeer, msgID, size int64, expectedHash string) error {
	h := sha256.New()
	sink := &partHashWriter{writer: h, limit: size}
	if err := s.TG.DownloadFile(ctx, peer, msgID, sink, nil); err != nil {
		return fmt.Errorf("verify remote part %d: %w", msgID, err)
	}
	if sink.count != size || hex.EncodeToString(h.Sum(nil)) != expectedHash {
		return fmt.Errorf("%w: remote message %d", errResumablePartMismatch, msgID)
	}
	return nil
}

func uploadMessageIsOwn(msg tgclient.HistoryMessage, actorID int64) bool {
	return actorID > 0 && (msg.FromID == actorID || (msg.FromID == 0 && msg.Outgoing))
}

func (s *Service) startResumableUpload(ctx context.Context, uploadID int, sourcePath, name string, size int64, parent string, channelID int64, peer tgclient.InputPeer, observer uploadObserver) (Metadata, projection.Op, string, error) {
	if !supportsIdempotentSends(s.TG) {
		return Metadata{}, projection.Op{}, "", fmt.Errorf("resumable upload requires Telegram idempotent sends")
	}
	plan, err := s.buildResumablePartPlan(size)
	if err != nil {
		return Metadata{}, projection.Op{}, "", err
	}
	actorID, err := s.ActorID(ctx)
	if err != nil {
		return Metadata{}, projection.Op{}, "", err
	}
	job, err := s.createUploadJob(ctx, sourcePath, name, parent, size, channelID, actorID, plan, peer)
	if err != nil {
		return Metadata{}, projection.Op{}, "", err
	}
	s.emitUploadJob(ctx, job, &uploadID)
	meta, err := s.runResumableUpload(ctx, job, sourcePath, peer, observer, &uploadID)
	if meta.MsgID > 0 && err != nil {
		op := uploadJobManifest(job)
		return meta, op, projection.Format(op), err
	}
	return meta, projection.Op{}, "", err
}

// buildResumablePartPlan limits the worst-case retransmission to about 512 MiB
// for ordinary large files, while retaining the existing 32-part wire limit.
// The chosen size is persisted, so later app versions cannot change a job.
func (s *Service) buildResumablePartPlan(size int64) (uploadPartPlan, error) {
	ceiling := s.maxPartBytes()
	if size <= ceiling || size > s.largeFileMaxBytes() || ceiling <= 0 {
		return uploadPartPlan{}, fmt.Errorf("file is outside resumable multipart limits")
	}
	const target int64 = 512 << 20
	minimum := size / MaxParts
	if size%MaxParts != 0 {
		minimum++
	}
	partSize := max(min(ceiling, target), minimum)
	if partSize > ceiling {
		return uploadPartPlan{}, fmt.Errorf("file would exceed %d upload parts", MaxParts)
	}
	partCount := size / partSize
	if size%partSize != 0 {
		partCount++
	}
	return uploadPartPlan{partSize: partSize, partCount: int(partCount)}, nil
}

// ResumeUpload resumes an interrupted plain multipart upload. An empty path
// uses the original source path; a replacement must have identical bytes.
func (s *Service) ResumeUpload(ctx context.Context, expectedChannelID int64, jobID, sourcePath string) (Metadata, error) {
	job, err := s.loadUploadJob(ctx, jobID)
	if err != nil {
		return Metadata{}, err
	}
	if expectedChannelID == 0 || job.ChannelID != expectedChannelID {
		return Metadata{}, fmt.Errorf("upload job belongs to another channel")
	}
	if job.Status == resumeCanceling || job.Status == resumeComplete || job.Status == resumeRestartRequired {
		return Metadata{}, fmt.Errorf("upload job cannot be resumed from %s", job.Status)
	}
	if err := s.ready(); err != nil {
		return Metadata{}, err
	}
	if s.TG == nil || s.Peers == nil || s.ActorID == nil {
		return Metadata{}, fmt.Errorf("upload service not ready")
	}
	if !supportsIdempotentSends(s.TG) {
		return Metadata{}, fmt.Errorf("resumable upload requires Telegram idempotent sends")
	}
	actorID, err := s.ActorID(ctx)
	if err != nil {
		return Metadata{}, err
	}
	if actorID != job.ActorID || job.Namespace != s.CacheNamespace {
		return Metadata{}, fmt.Errorf("upload belongs to another account")
	}
	peer, err := s.Peers.ResolvePeer(ctx, job.ChannelID)
	if err != nil {
		return Metadata{}, err
	}
	if sourcePath == "" {
		sourcePath = job.Path
	}
	release, err := s.acquireUploadSlot(ctx)
	if err != nil {
		return Metadata{}, err
	}
	defer release()
	return s.runResumableUpload(ctx, job, sourcePath, peer, detailedUploadObserver{service: s}, nil)
}

func (s *Service) runResumableUpload(ctx context.Context, job uploadJob, sourcePath string, peer tgclient.InputPeer, observer uploadObserver, uploadID *int) (Metadata, error) {
	if job.ID == "" || job.ChannelID == 0 || job.ActorID <= 0 || job.Size <= 0 || job.PartSize <= 0 || job.PartCount < 2 || job.PartCount > MaxParts || job.Hash == "" || len(job.PartHashes) != job.PartCount {
		return Metadata{}, fmt.Errorf("invalid resumable upload job")
	}
	plan := uploadPartPlan{partSize: job.PartSize, partCount: job.PartCount}
	if last, length, err := plan.window(job.Size, job.PartCount-1); err != nil || length <= 0 || last+length != job.Size {
		return Metadata{}, fmt.Errorf("invalid resumable upload part plan")
	}
	previousStatus := job.Status
	claim, err := s.DB.ExecContext(ctx, `UPDATE resumable_uploads SET status = ?, error = ''
		WHERE job_id = ? AND account_namespace = ? AND status IN (?, ?, ?, ?)`,
		resumeUploading, job.ID, s.CacheNamespace, resumePaused, resumeNeedsFile, resumeUncertain, resumeManifestUncertain)
	if err != nil {
		return Metadata{}, err
	}
	rows, err := claim.RowsAffected()
	if err != nil || rows != 1 {
		return Metadata{}, fmt.Errorf("upload is already active or no longer resumable")
	}
	job.Status = resumeUploading
	s.emitUploadJob(ctx, job, uploadID)
	runCtx, cancel := context.WithCancel(ctx)
	untrack := s.trackResumeCancel(job.ID, cancel)
	defer func() { untrack(); cancel() }()
	fail := func(status string, cause error) (Metadata, error) {
		if errors.Is(cause, context.Canceled) && status != resumeManifestUncertain {
			status = resumePaused
		}
		// The caller context can be canceled after Telegram accepts a message.
		// Persist the recovery state with a bounded independent context.
		writeCtx, end := context.WithTimeout(context.Background(), 5*time.Second)
		defer end()
		if err := s.setUploadJobStatus(writeCtx, job.ID, status, cause.Error()); err != nil {
			return Metadata{}, errors.Join(cause, fmt.Errorf("save upload state: %w", err))
		}
		job.Status = status
		s.emitUploadJob(writeCtx, job, uploadID)
		return Metadata{}, cause
	}

	parts, manifest, err := s.reconcileUploadMessages(runCtx, job, peer, true)
	if err != nil {
		status := resumeUncertain
		if previousStatus == resumeManifestUncertain {
			status = resumeManifestUncertain
		} else if errors.Is(err, errResumablePartMismatch) {
			status = resumeRestartRequired
		}
		return fail(status, err)
	}
	if manifest != 0 {
		return s.finishResumableUpload(job, manifest, uploadID)
	}
	if previousStatus == resumeManifestUncertain {
		if len(parts) != job.PartCount {
			return fail(resumeManifestUncertain, fmt.Errorf("manifest outcome is unresolved and the part set is incomplete"))
		}
		if _, err := s.validParent(job.ChannelID, job.Parent, "parent"); err != nil {
			return fail(resumeManifestUncertain, err)
		}
		// This is an explicit user retry after a full remote history scan. Reuse
		// the persisted UUID's random ID; do not depend on the source file once
		// every part has a confirmed, verified receipt.
		op := uploadJobManifest(job)
		msgID, _, err := s.commitMultipartManifest(runCtx, job.ChannelID, op, projection.Format(op), job.ActorID, peer, job.ID)
		if err != nil {
			_, failure := fail(resumeManifestUncertain, fmt.Errorf("manifest outcome requires reconciliation: %w", err))
			if msgID > 0 {
				return Metadata{Name: job.Name, Size: job.Size, MsgID: int(msgID), ParentID: job.Parent,
					UploadTime: job.UploadTime, PlaintextSize: job.Size}, failure
			}
			return Metadata{}, failure
		}
		return s.finishResumableUpload(job, msgID, uploadID)
	}
	if _, err := s.validParent(job.ChannelID, job.Parent, "parent"); err != nil {
		return fail(resumePaused, err)
	}
	hash, info, err := hashUploadSource(runCtx, sourcePath, job.Size)
	if err != nil {
		return fail(resumeNeedsFile, err)
	}
	if hash != job.Hash {
		return fail(resumeNeedsFile, fmt.Errorf("selected source differs from the original upload"))
	}
	if sourcePath != job.Path {
		if _, err := s.DB.ExecContext(runCtx, `UPDATE resumable_uploads SET source_path = ?, source_mtime_ns = ? WHERE job_id = ?`,
			sourcePath, info.ModTime().UnixNano(), job.ID); err != nil {
			return fail(resumePaused, err)
		}
		job.Path = sourcePath
	}
	f, err := os.Open(sourcePath)
	if err != nil {
		return fail(resumeNeedsFile, err)
	}
	defer f.Close()
	for i := len(parts); i < job.PartCount; i++ {
		if err := runCtx.Err(); err != nil {
			return fail(resumePaused, err)
		}
		offset, length, err := plan.window(job.Size, i)
		if err != nil {
			return fail(resumeUncertain, err)
		}
		partOp := projection.Op{Type: projection.OpFilePart, UploadUUID: job.ID, PartIndex: i, FileSize: length}
		caption := projection.Format(partOp)
		randomID, err := tgclient.StableRandomID(job.ID, fmt.Sprintf("part:%d", i))
		if err != nil {
			return fail(resumeUncertain, err)
		}
		var result tgclient.SendFileResult
		var sentHash string
		var sentBytes int64
		attempts := 0
		err = s.retryVisibleSend(runCtx, true, func() error {
			attempts++
			if _, err := f.Seek(offset, io.SeekStart); err != nil {
				return err
			}
			h := sha256.New()
			sink := &partHashWriter{writer: h, limit: length}
			result, err = tgclient.SendFileIdempotent(runCtx, s.TG, peer,
				io.TeeReader(io.LimitReader(f, length), sink),
				partAttachmentName(job.Name, i, job.PartCount), caption, length,
				func(sent, _ int64) {
					if uploadID != nil {
						observer.Progress(*uploadID, float64(offset+sent)/float64(job.Size)*100)
					}
				}, randomID)
			sentBytes = sink.count
			sentHash = hex.EncodeToString(h.Sum(nil))
			return err
		})
		if err != nil {
			return fail(resumeUncertain, err)
		}
		if result.MsgID <= 0 {
			return fail(resumeUncertain, fmt.Errorf("part %d returned no message ID", i))
		}
		if sentBytes == length && sentHash != job.PartHashes[i] {
			return fail(resumeRestartRequired, fmt.Errorf("%w at part %d", errResumablePartMismatch, i))
		}
		if sentBytes != length || attempts > 1 {
			if err := s.verifyRemotePart(runCtx, peer, result.MsgID, length, job.PartHashes[i]); err != nil {
				return fail(resumeRestartRequired, err)
			}
		}
		if s.afterVisiblePartSend != nil {
			s.afterVisiblePartSend(i, result.MsgID)
		}
		if _, err := projection.ProjectFromOp(s.DB, job.ChannelID, result.MsgID, partOp, job.ActorID, caption); err != nil {
			return fail(resumeUncertain, fmt.Errorf("record part %d: %w", i, err))
		}
		parts = append(parts, projection.FilePart{PartIndex: i, MsgID: result.MsgID, Size: length})
		s.emitUploadJob(runCtx, job, uploadID)
	}
	// Rehash the source before publication. A mutation during any part send
	// must not create a manifest linking bytes from different file versions.
	finalHash, _, err := hashOpenUploadSource(runCtx, f, job.Size)
	if err != nil || finalHash != job.Hash {
		if err == nil {
			err = fmt.Errorf("source changed during upload")
		}
		return fail(resumeRestartRequired, err)
	}
	manifestOp := uploadJobManifest(job)
	header := projection.Format(manifestOp)
	manifestID, attempted, err := s.commitMultipartManifest(runCtx, job.ChannelID, manifestOp, header, job.ActorID, peer, job.ID)
	if err != nil {
		if attempted {
			_, failure := fail(resumeManifestUncertain, fmt.Errorf("manifest outcome requires reconciliation: %w", err))
			if manifestID > 0 {
				return Metadata{Name: job.Name, Size: job.Size, MsgID: int(manifestID), ParentID: job.Parent,
					UploadTime: job.UploadTime, PlaintextSize: job.Size}, failure
			}
			return Metadata{}, failure
		}
		return fail(resumePaused, err)
	}
	return s.finishResumableUpload(job, manifestID, uploadID)
}

func uploadJobManifest(job uploadJob) projection.Op {
	return projection.Op{
		Type: projection.OpFileManifest, UploadUUID: job.ID, Parent: job.Parent,
		Name: job.Name, FileSize: job.Size, FileUploadTime: job.UploadTime,
		PartCount: job.PartCount,
	}
}

func (s *Service) finishResumableUpload(job uploadJob, manifestID int64, uploadID *int) (Metadata, error) {
	if manifestID <= 0 {
		return Metadata{}, fmt.Errorf("manifest message ID missing")
	}
	_, err := s.DB.Exec(`UPDATE resumable_uploads SET status = ?, error = '', manifest_msg_id = ?
		WHERE job_id = ? AND account_namespace = ?`, resumeComplete, manifestID, job.ID, s.CacheNamespace)
	if err != nil {
		return Metadata{}, err
	}
	job.Status = resumeComplete
	s.emitUploadJob(context.Background(), job, uploadID)
	if uploadID != nil {
		detailedUploadObserver{service: s}.Progress(*uploadID, 100)
	}
	return Metadata{Name: job.Name, Size: job.Size, MsgID: int(manifestID), ParentID: job.Parent,
		UploadTime: job.UploadTime, PlaintextSize: job.Size}, nil
}

// reconcileUploadMessages verifies the entire confirmed prefix against direct
// Telegram history and projects at most one accepted, unrecorded part. It also
// detects an accepted manifest before any new send or cleanup.
func (s *Service) reconcileUploadMessages(ctx context.Context, job uploadJob, peer tgclient.InputPeer, verifyContent bool) ([]projection.FilePart, int64, error) {
	projected, err := projection.PartsForUUIDContext(ctx, s.DB, job.ChannelID, job.ID)
	if err != nil {
		return nil, 0, err
	}
	plan := uploadPartPlan{partSize: job.PartSize, partCount: job.PartCount}
	if _, err := validateHiddenPartPrefix(projected, plan, job.Size); err != nil {
		return nil, 0, err
	}
	seenParts := make(map[int]tgclient.HistoryMessage, job.PartCount)
	var manifest tgclient.HistoryMessage
	var offset int64
	for {
		page, err := s.TG.GetHistory(ctx, peer, job.HistoryBase, offset, 100)
		if err != nil {
			return nil, 0, fmt.Errorf("reconcile upload history: %w", err)
		}
		for _, msg := range page {
			if msg.Placeholder {
				continue
			}
			op, err := projection.Parse(msg.Text)
			if err != nil || op.UploadUUID != job.ID {
				continue
			}
			if !uploadMessageIsOwn(msg, job.ActorID) {
				return nil, 0, fmt.Errorf("upload message %d belongs to another sender", msg.MsgID)
			}
			switch op.Type {
			case projection.OpFilePart:
				if op.PartIndex < 0 || op.PartIndex >= job.PartCount || !msg.HasMedia {
					return nil, 0, fmt.Errorf("invalid remote part for upload")
				}
				_, size, err := plan.window(job.Size, op.PartIndex)
				if err != nil || op.FileSize != size || msg.MediaSize != size ||
					msg.DocumentName != partAttachmentName(job.Name, op.PartIndex, job.PartCount) {
					return nil, 0, fmt.Errorf("remote part %d does not match job", op.PartIndex)
				}
				if previous, ok := seenParts[op.PartIndex]; ok && previous.MsgID != msg.MsgID {
					return nil, 0, fmt.Errorf("duplicate remote part %d", op.PartIndex)
				}
				seenParts[op.PartIndex] = msg
			case projection.OpFileManifest:
				if op.Parent != job.Parent || op.Name != job.Name || op.FileSize != job.Size ||
					op.PartCount != job.PartCount || op.FileUploadTime != job.UploadTime {
					return nil, 0, fmt.Errorf("remote manifest does not match job")
				}
				if manifest.MsgID != 0 && manifest.MsgID != msg.MsgID {
					return nil, 0, fmt.Errorf("duplicate remote manifest")
				}
				manifest = msg
			}
		}
		if len(page) == 0 {
			break
		}
		nextOffset := page[len(page)-1].MsgID
		if nextOffset == offset {
			return nil, 0, fmt.Errorf("channel history did not advance during upload reconciliation")
		}
		offset = nextOffset
		if offset <= job.HistoryBase {
			break
		}
	}
	for i, part := range projected {
		msg, ok := seenParts[i]
		if !ok || msg.MsgID != part.MsgID {
			return nil, 0, fmt.Errorf("confirmed remote part %d is missing or replaced", i)
		}
	}
	for i := len(projected); i < len(seenParts); i++ {
		msg, ok := seenParts[i]
		if !ok {
			return nil, 0, fmt.Errorf("remote part receipts are not contiguous")
		}
		_, size, err := plan.window(job.Size, i)
		if err != nil {
			return nil, 0, err
		}
		if verifyContent {
			if err := s.verifyRemotePart(ctx, peer, msg.MsgID, size, job.PartHashes[i]); err != nil {
				return nil, 0, err
			}
		}
		op := projection.Op{Type: projection.OpFilePart, UploadUUID: job.ID, PartIndex: i, FileSize: size}
		if _, err := projection.ProjectFromOp(s.DB, job.ChannelID, msg.MsgID, op, job.ActorID, projection.Format(op)); err != nil {
			return nil, 0, fmt.Errorf("project recovered part: %w", err)
		}
		projected = append(projected, projection.FilePart{PartIndex: i, MsgID: msg.MsgID, Size: size})
	}
	if manifest.MsgID != 0 {
		if len(projected) != job.PartCount {
			return nil, 0, fmt.Errorf("remote manifest exists but parts are incomplete")
		}
		if !verifyContent {
			// Cleanup only needs the manifest identity to refuse deletion. Its
			// parts may be corrupt, so publishing it here would expose bad data.
			return projected, manifest.MsgID, nil
		}
		op := uploadJobManifest(job)
		if _, err := projection.ProjectFromOp(s.DB, job.ChannelID, manifest.MsgID, op, job.ActorID, projection.Format(op)); err != nil {
			return nil, 0, fmt.Errorf("project recovered manifest: %w", err)
		}
		return projected, manifest.MsgID, nil
	}
	return projected, 0, nil
}

func (s *Service) verifiedCancelIDs(ctx context.Context, job uploadJob, peer tgclient.InputPeer, parts []projection.FilePart) ([]int64, error) {
	plan := uploadPartPlan{partSize: job.PartSize, partCount: job.PartCount}
	wanted := make(map[int64]projection.FilePart, len(parts))
	for _, part := range parts {
		if part.MsgID <= 0 || part.PartIndex < 0 || part.PartIndex >= job.PartCount {
			return nil, fmt.Errorf("invalid saved part receipt")
		}
		_, size, err := plan.window(job.Size, part.PartIndex)
		if err != nil || size != part.Size {
			return nil, fmt.Errorf("invalid saved part size")
		}
		if _, duplicate := wanted[part.MsgID]; duplicate {
			return nil, fmt.Errorf("duplicate saved part message ID")
		}
		wanted[part.MsgID] = part
	}
	var ids []int64
	var offset int64
	for {
		page, err := s.TG.GetHistory(ctx, peer, job.HistoryBase, offset, 100)
		if err != nil {
			return nil, err
		}
		for _, msg := range page {
			op, parseErr := projection.Parse(msg.Text)
			if parseErr == nil && op.UploadUUID == job.ID && op.Type == projection.OpFileManifest {
				return nil, fmt.Errorf("upload manifest may already be published")
			}
			part, ok := wanted[msg.MsgID]
			if !ok {
				continue
			}
			if msg.Placeholder {
				continue // Already deleted; deleting it again is unnecessary.
			}
			if parseErr != nil || op.Type != projection.OpFilePart || op.UploadUUID != job.ID ||
				op.PartIndex != part.PartIndex || op.FileSize != part.Size || !msg.HasMedia ||
				msg.MediaSize != part.Size || msg.DocumentName != partAttachmentName(job.Name, part.PartIndex, job.PartCount) ||
				!uploadMessageIsOwn(msg, job.ActorID) {
				return nil, fmt.Errorf("saved part message %d no longer belongs to this upload", msg.MsgID)
			}
			ids = append(ids, msg.MsgID)
		}
		if len(page) == 0 {
			break
		}
		nextOffset := page[len(page)-1].MsgID
		if nextOffset == offset {
			return nil, fmt.Errorf("channel history did not advance during cleanup")
		}
		offset = nextOffset
		if offset <= job.HistoryBase {
			break
		}
	}
	return ids, nil
}

// CancelResumableUpload removes only messages whose upload UUID and structure
// have been verified in channel history. A discovered manifest blocks cleanup.
func (s *Service) CancelResumableUpload(ctx context.Context, expectedChannelID int64, jobID string) error {
	job, err := s.loadUploadJob(ctx, jobID)
	if err != nil {
		return err
	}
	if expectedChannelID == 0 || job.ChannelID != expectedChannelID {
		return fmt.Errorf("upload job belongs to another channel")
	}
	if job.Status == resumeComplete {
		return fmt.Errorf("completed upload cannot be canceled")
	}
	if s.ActorID == nil {
		return fmt.Errorf("actor resolver not ready")
	}
	actorID, err := s.ActorID(ctx)
	if err != nil {
		return err
	}
	if actorID != job.ActorID {
		return fmt.Errorf("upload belongs to another account")
	}
	if s.PauseUpload(expectedChannelID, jobID) {
		return fmt.Errorf("upload is stopping; retry cancellation when paused")
	}
	if job.Status == resumeUploading {
		return fmt.Errorf("upload is still active; retry cancellation when paused")
	}
	if s.TG == nil || s.Peers == nil {
		return fmt.Errorf("upload service not ready")
	}
	peer, err := s.Peers.ResolvePeer(ctx, job.ChannelID)
	if err != nil {
		return err
	}
	var parts []projection.FilePart
	if job.Status == resumeCanceling {
		parts, err = projection.PartsForUUIDContext(ctx, s.DB, job.ChannelID, job.ID)
		if err != nil {
			return err
		}
	} else {
		var manifest int64
		parts, manifest, err = s.reconcileUploadMessages(ctx, job, peer, job.Status != resumeRestartRequired)
		if err != nil {
			return err
		}
		if manifest != 0 || job.Status == resumeManifestUncertain {
			return fmt.Errorf("manifest may already be published; reconcile it before canceling")
		}
		if err := s.setUploadJobStatus(ctx, job.ID, resumeCanceling, ""); err != nil {
			return err
		}
	}
	job.Status = resumeCanceling
	s.emitUploadJob(ctx, job, nil)
	ids, err := s.verifiedCancelIDs(ctx, job, peer, parts)
	if err != nil {
		return err
	}
	if err := s.deleteMessagesChunked(ctx, peer, ids); err != nil {
		_ = projection.QueuePartCleanup(s.DB, job.ChannelID, ids)
		return fmt.Errorf("delete canceled upload parts: %w", err)
	}
	if err := projection.DeleteFileParts(s.DB, job.ChannelID, job.ID); err != nil {
		return err
	}
	if err := projection.ClearPartCleanup(s.DB, job.ChannelID, ids); err != nil {
		return err
	}
	_, err = s.DB.ExecContext(ctx, `DELETE FROM resumable_uploads WHERE job_id = ? AND account_namespace = ?`, job.ID, s.CacheNamespace)
	return err
}
