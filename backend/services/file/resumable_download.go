package file

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"TDrive/backend/datadir"
	"TDrive/backend/projection"
)

// ResumableDownload is the account-scoped view of a download job.
// VerifiedBytes counts blocks committed to the journal, not in-flight reads.
type ResumableDownload struct {
	JobID         string `json:"job_id"`
	ChannelID     int64  `json:"channel_id"`
	LogicalMsgID  int64  `json:"logical_msg_id"`
	Name          string `json:"name"`
	Status        string `json:"status"`
	VerifiedBytes int64  `json:"verified_bytes"`
	TotalBytes    int64  `json:"total_bytes"`
	Encrypted     bool   `json:"encrypted"`
	Error         string `json:"error"`
	SavedPath     string `json:"saved_path,omitempty"`
}

const (
	downloadPaused           = "paused"
	downloadDownloading      = "downloading"
	downloadWaitingNetwork   = "waiting_network"
	downloadWaitingUnlock    = "waiting_unlock"
	downloadNeedsDestination = "needs_destination"
	downloadSourceChanged    = "source_changed"
	downloadVerifying        = "verifying"
	downloadSaving           = "saving"
	downloadCompleted        = "completed"
	downloadError            = "error"
)

type downloadJob struct {
	ID          string
	ChannelID   int64
	Namespace   string
	File        projection.DownloadFile
	Destination string
	StagePath   string
	Status      string
	Detail      string
	DestExists  bool
	DestSize    int64
	DestMTimeNS int64
	OutputHash  string
}

type downloadRun struct {
	cancel context.CancelFunc
	done   chan struct{}
}

func (s *Service) downloadStageRoot() (string, error) {
	root := s.DownloadStagingDir
	if root == "" {
		dataDir, err := datadir.Dir()
		if err != nil {
			return "", err
		}
		root = filepath.Join(dataDir, "downloads-in-progress")
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		return "", err
	}
	info, err := os.Lstat(root)
	if err != nil {
		return "", err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("download staging root is not a directory")
	}
	if err := os.Chmod(root, 0o700); err != nil {
		return "", err
	}
	return root, nil
}

// The journal and staged bytes have different durability roles. A block row
// exists only after its bytes have been synced. SQLite must never get ahead of
// the file, or recovery could publish unwritten bytes after a crash.
func (s *Service) ensureDownloadJournal() error {
	if s.DB == nil || strings.TrimSpace(s.CacheNamespace) == "" {
		return fmt.Errorf("download journal: database or account unavailable")
	}
	s.downloadResumeOnce.Do(func() {
		_, s.downloadResumeErr = s.DB.Exec(`CREATE TABLE IF NOT EXISTS resumable_downloads (
			job_id TEXT PRIMARY KEY,
			account_namespace TEXT NOT NULL,
			channel_id INTEGER NOT NULL,
			file_json TEXT NOT NULL,
			destination TEXT NOT NULL,
			stage_path TEXT NOT NULL,
			status TEXT NOT NULL,
			detail TEXT NOT NULL DEFAULT '',
			dest_exists INTEGER NOT NULL,
			dest_size INTEGER NOT NULL,
			dest_mtime_ns INTEGER NOT NULL,
			output_sha256 TEXT NOT NULL DEFAULT ''
		)`)
		if s.downloadResumeErr != nil {
			return
		}
		_, s.downloadResumeErr = s.DB.Exec(`CREATE TABLE IF NOT EXISTS resumable_download_blocks (
			job_id TEXT NOT NULL,
			part_index INTEGER NOT NULL,
			block_index INTEGER NOT NULL,
			byte_count INTEGER NOT NULL,
			sha256 TEXT NOT NULL,
			PRIMARY KEY (job_id, part_index, block_index),
			FOREIGN KEY (job_id) REFERENCES resumable_downloads(job_id) ON DELETE CASCADE
		)`)
		if s.downloadResumeErr != nil {
			return
		}
		_, s.downloadResumeErr = s.DB.Exec(`CREATE INDEX IF NOT EXISTS idx_resumable_downloads_account
			ON resumable_downloads(account_namespace, channel_id, status)`)
		if s.downloadResumeErr != nil {
			return
		}
		_, s.downloadResumeErr = s.DB.Exec(`UPDATE resumable_downloads SET status = ?
			WHERE account_namespace = ? AND status IN (?, ?)`,
			downloadPaused, s.CacheNamespace, downloadDownloading, downloadVerifying)
	})
	return s.downloadResumeErr
}

// StartResumableDownload pins the projected file before opening the save dialog.
// The resulting job survives cancellation, network errors and process restart.
func (s *Service) StartResumableDownload(ctx context.Context, channelID int64, msgID, lookupID int, chooseSavePath ChooseSavePathFunc) DownloadResult {
	if err := s.ready(); err != nil {
		return downloadFailure("error", "Download unavailable", "", err)
	}
	if channelID <= 0 || s.TG == nil || s.Peers == nil || chooseSavePath == nil {
		return downloadFailure("error", "Download unavailable", "", fmt.Errorf("invalid download request"))
	}
	fileID := int64(lookupID)
	if fileID == 0 {
		fileID = int64(msgID)
	}
	file, found, err := projection.FileDownloadRefContext(ctx, s.DB, channelID, fileID)
	if err != nil {
		return downloadFailure("error", "Unable to find file", "", err)
	}
	if !found {
		return downloadFailure("error", "File deleted or not found", "", os.ErrNotExist)
	}
	// Do not make a locked-vault user choose a path they will have to choose again.
	key, err := s.requireEncryptionKey(file.Encrypted)
	clearOwnedKey(key)
	if err != nil {
		return downloadFailure("error", err.Error(), "", err)
	}
	destination, err := chooseSavePath(file.Name)
	if err != nil {
		return downloadFailure("error", "Unable to choose download location", "", err)
	}
	if destination == "" {
		return downloadFailure("canceled", "Download canceled", "", context.Canceled)
	}
	return s.startResumableProjectedFile(ctx, channelID, file, destination)
}

func (s *Service) startResumableProjectedFile(ctx context.Context, channelID int64, file projection.DownloadFile, destination string) DownloadResult {
	if err := s.ensureDownloadJournal(); err != nil {
		return downloadFailure("error", "Unable to save download progress", "", err)
	}
	if channelID <= 0 || file.LogicalMsgID <= 0 || strings.TrimSpace(destination) == "" {
		return downloadFailure("error", "Invalid download destination", "", fmt.Errorf("invalid download identity or destination"))
	}
	root, err := s.downloadStageRoot()
	if err != nil {
		return downloadFailure("error", "Unable to prepare download storage", "", err)
	}
	info, err := os.Lstat(destination)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return downloadFailure("error", "Unable to inspect download destination", "", err)
	}
	if err == nil && !info.Mode().IsRegular() {
		return downloadFailure("error", "Download destination is not a regular file", "", fmt.Errorf("destination is not a regular file"))
	}
	id := projection.NewUploadUUID()
	stagePath := filepath.Join(root, id+".partial")
	stage, err := os.OpenFile(stagePath, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0o600)
	if err != nil {
		return downloadFailure("error", "Unable to create download staging file", "", err)
	}
	if err := stage.Close(); err != nil {
		_ = os.Remove(stagePath)
		return downloadFailure("error", "Unable to create download staging file", "", err)
	}
	job := downloadJob{ID: id, ChannelID: channelID, Namespace: s.CacheNamespace,
		File: file, Destination: destination, StagePath: stagePath, Status: downloadPaused}
	if info != nil {
		job.DestExists, job.DestSize, job.DestMTimeNS = true, info.Size(), info.ModTime().UnixNano()
	}
	fileJSON, err := json.Marshal(file)
	if err == nil {
		_, err = s.DB.ExecContext(ctx, `INSERT INTO resumable_downloads
			(job_id, account_namespace, channel_id, file_json, destination, stage_path,
			status, dest_exists, dest_size, dest_mtime_ns)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			id, s.CacheNamespace, channelID, string(fileJSON), destination, stagePath,
			job.Status, job.DestExists, job.DestSize, job.DestMTimeNS)
	}
	if err != nil {
		_ = os.Remove(stagePath)
		return downloadFailure("error", "Unable to save download progress", "", err)
	}
	s.emitDownloadChanged(job, 0)
	return s.runResumableDownload(ctx, job)
}

func (s *Service) ResumeDownload(ctx context.Context, jobID string) DownloadResult {
	job, err := s.loadDownloadJob(ctx, jobID)
	if err != nil {
		return downloadFailure("error", "Download job not found", jobID, err)
	}
	return s.runResumableDownload(ctx, job)
}

func (s *Service) ListResumableDownloads(ctx context.Context) ([]ResumableDownload, error) {
	if err := s.ensureDownloadJournal(); err != nil {
		return nil, err
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT d.job_id, d.channel_id, d.file_json, d.status, d.detail,
		COALESCE(SUM(b.byte_count), 0), d.destination
		FROM resumable_downloads d LEFT JOIN resumable_download_blocks b ON b.job_id = d.job_id
		WHERE d.account_namespace = ?
		GROUP BY d.job_id ORDER BY d.rowid DESC`, s.CacheNamespace)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var jobs []ResumableDownload
	for rows.Next() {
		var view ResumableDownload
		var fileJSON, destination string
		if err := rows.Scan(&view.JobID, &view.ChannelID, &fileJSON, &view.Status, &view.Error, &view.VerifiedBytes, &destination); err != nil {
			return nil, err
		}
		var file projection.DownloadFile
		if err := json.Unmarshal([]byte(fileJSON), &file); err != nil {
			return nil, fmt.Errorf("decode download job %s: %w", view.JobID, err)
		}
		view.LogicalMsgID, view.Name, view.TotalBytes, view.Encrypted = file.LogicalMsgID, file.Name, file.StoredSize, file.Encrypted
		view.VerifiedBytes = min(max(view.VerifiedBytes, 0), file.StoredSize)
		if view.Status == downloadCompleted {
			view.SavedPath = destination
			view.VerifiedBytes = file.StoredSize
		}
		jobs = append(jobs, view)
	}
	return jobs, rows.Err()
}

// PauseResumableDownload requests cancellation of one active run. Its journal
// and staged blocks remain available; the caller may wait for the active app
// operation to return before starting another operation on the same job.
func (s *Service) PauseResumableDownload(jobID string) bool {
	s.downloadResumeMu.Lock()
	run := s.downloadRuns[jobID]
	s.downloadResumeMu.Unlock()
	if run == nil {
		return false
	}
	run.cancel()
	return true
}

// ChangeResumableDownloadDestination preserves verified blocks when the
// original save location disappeared or was changed by another process.
func (s *Service) ChangeResumableDownloadDestination(ctx context.Context, jobID, destination string) error {
	job, err := s.loadDownloadJob(ctx, jobID)
	if err != nil {
		return err
	}
	if job.Status != downloadNeedsDestination {
		return fmt.Errorf("download does not need a new location")
	}
	if !filepath.IsAbs(destination) || strings.TrimSpace(destination) == "" {
		return fmt.Errorf("download destination must be an absolute path")
	}
	destination = filepath.Clean(destination)
	info, err := os.Lstat(destination)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err == nil && !info.Mode().IsRegular() {
		return fmt.Errorf("download destination is not a regular file")
	}
	exists := err == nil
	var size, mtime int64
	if exists {
		size, mtime = info.Size(), info.ModTime().UnixNano()
	}
	s.downloadResumeMu.Lock()
	if s.downloadRuns[jobID] != nil || s.downloadDiscards[jobID] {
		s.downloadResumeMu.Unlock()
		return fmt.Errorf("download is still running")
	}
	changed, err := s.DB.ExecContext(ctx, `UPDATE resumable_downloads SET destination = ?, dest_exists = ?,
		dest_size = ?, dest_mtime_ns = ?, status = ?, detail = '', output_sha256 = ''
		WHERE job_id = ? AND account_namespace = ? AND status = ?`, destination, exists, size, mtime,
		downloadPaused, jobID, s.CacheNamespace, downloadNeedsDestination)
	if err != nil {
		s.downloadResumeMu.Unlock()
		return err
	}
	rows, err := changed.RowsAffected()
	if err != nil || rows != 1 {
		s.downloadResumeMu.Unlock()
		return fmt.Errorf("download state changed before its location was updated")
	}
	s.downloadResumeMu.Unlock()
	job.Destination, job.DestExists, job.DestSize, job.DestMTimeNS = destination, exists, size, mtime
	job.Status, job.Detail, job.OutputHash = downloadPaused, "", ""
	confirmed, err := s.confirmedDownloadBytes(ctx, jobID)
	if err != nil {
		return err
	}
	s.emitDownloadChanged(job, confirmed)
	return nil
}

func (s *Service) DiscardResumableDownload(ctx context.Context, jobID string) error {
	job, err := s.loadDownloadJob(ctx, jobID)
	if err != nil {
		return err
	}
	s.downloadResumeMu.Lock()
	if s.downloadDiscards == nil {
		s.downloadDiscards = make(map[string]bool)
	}
	if s.downloadDiscards[jobID] {
		s.downloadResumeMu.Unlock()
		return fmt.Errorf("download is already being discarded")
	}
	s.downloadDiscards[jobID] = true
	run := s.downloadRuns[jobID]
	s.downloadResumeMu.Unlock()
	defer func() {
		s.downloadResumeMu.Lock()
		delete(s.downloadDiscards, jobID)
		s.downloadResumeMu.Unlock()
	}()
	if run != nil {
		run.cancel()
		select {
		case <-run.done:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	if job.Status == downloadSaving && job.OutputHash != "" {
		if _, err := s.reconcilePublishedDownload(ctx, &job); err != nil {
			return err
		}
		if _, err := os.Lstat(downloadBackupPath(job)); err == nil {
			return fmt.Errorf("previous destination backup remains at %s; keep the job until it is recovered", downloadBackupPath(job))
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	if err := os.Remove(job.StagePath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if _, err := s.DB.ExecContext(ctx, `DELETE FROM resumable_download_blocks WHERE job_id = ?`, jobID); err != nil {
		return err
	}
	if _, err := s.DB.ExecContext(ctx, `DELETE FROM resumable_downloads WHERE job_id = ? AND account_namespace = ?`, jobID, s.CacheNamespace); err != nil {
		return err
	}
	return nil
}

func (s *Service) loadDownloadJob(ctx context.Context, jobID string) (downloadJob, error) {
	if err := s.ensureDownloadJournal(); err != nil {
		return downloadJob{}, err
	}
	var job downloadJob
	var fileJSON string
	err := s.DB.QueryRowContext(ctx, `SELECT job_id, account_namespace, channel_id, file_json,
		destination, stage_path, status, detail, dest_exists, dest_size, dest_mtime_ns, output_sha256
		FROM resumable_downloads WHERE job_id = ? AND account_namespace = ?`, jobID, s.CacheNamespace).Scan(
		&job.ID, &job.Namespace, &job.ChannelID, &fileJSON,
		&job.Destination, &job.StagePath, &job.Status, &job.Detail,
		&job.DestExists, &job.DestSize, &job.DestMTimeNS, &job.OutputHash)
	if err != nil {
		return downloadJob{}, err
	}
	if err := json.Unmarshal([]byte(fileJSON), &job.File); err != nil {
		return downloadJob{}, err
	}
	root, err := s.downloadStageRoot()
	if err != nil {
		return downloadJob{}, err
	}
	if filepath.Dir(job.StagePath) != root || filepath.Base(job.StagePath) != job.ID+".partial" {
		return downloadJob{}, fmt.Errorf("download job has invalid staging path")
	}
	return job, nil
}

func (s *Service) setDownloadStatus(ctx context.Context, job *downloadJob, status, detail string, verified int64) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE resumable_downloads SET status = ?, detail = ?
		WHERE job_id = ? AND account_namespace = ?`, status, detail, job.ID, s.CacheNamespace)
	if err != nil {
		return err
	}
	job.Status, job.Detail = status, detail
	s.emitDownloadChanged(*job, verified)
	return nil
}

func (s *Service) confirmedDownloadBytes(ctx context.Context, jobID string) (int64, error) {
	var bytes int64
	err := s.DB.QueryRowContext(ctx, `SELECT COALESCE(SUM(byte_count), 0)
		FROM resumable_download_blocks WHERE job_id = ?`, jobID).Scan(&bytes)
	return bytes, err
}

func (s *Service) emitDownloadChanged(job downloadJob, verified int64) {
	view := ResumableDownload{JobID: job.ID, ChannelID: job.ChannelID,
		LogicalMsgID: job.File.LogicalMsgID, Name: job.File.Name, Status: job.Status,
		VerifiedBytes: verified, TotalBytes: job.File.StoredSize, Encrypted: job.File.Encrypted, Error: job.Detail}
	if job.Status == downloadCompleted {
		view.SavedPath = job.Destination
		view.VerifiedBytes = job.File.StoredSize
	}
	s.emitEvent("resumable_download_changed", view)
}

func downloadFailure(status, message, jobID string, err error) DownloadResult {
	return DownloadResult{Status: status, Message: message, JobID: jobID, Err: err}
}
