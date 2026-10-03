package file

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"

	"TDrive/backend/projection"
	"TDrive/backend/tgclient"
)

// ResumableUpload is the public, path-free view of a local multipart job.
// ConfirmedBytes counts durable part receipts, not bytes in the current send.
type ResumableUpload struct {
	JobID          string `json:"job_id"`
	ChannelID      int64  `json:"channel_id"`
	Name           string `json:"name"`
	ParentID       string `json:"parent_id"`
	Size           int64  `json:"size"`
	StoredSize     int64  `json:"stored_size"`
	ConfirmedBytes int64  `json:"confirmed_bytes"`
	Status         string `json:"status"`
	Error          string `json:"error"`
	UploadID       *int   `json:"upload_id,omitempty"`
}

const (
	resumePaused            = "paused"
	resumeUploading         = "uploading"
	resumeNeedsFile         = "needs_source"
	resumeUncertain         = "uncertain"
	resumeManifestUncertain = "uncertain_manifest"
	resumeRestartRequired   = "restart_required"
	resumeWaitingNetwork    = "waiting_network"
	resumeComplete          = "completed"
	resumeCanceling         = "canceling"
)

type uploadJob struct {
	ID          string
	ChannelID   int64
	Namespace   string
	Path        string
	Name        string
	Parent      string
	Size        int64
	Hash        string
	PartHashes  []string
	ModTimeNS   int64
	PartSize    int64
	PartCount   int
	HistoryBase int64
	UploadTime  int64
	ActorID     int64
	Status      string
	ManifestID  int64
}

// ensureUploadJournal is called before every journal read/write. The startup
// transition is local to this Service instance, so listing jobs does not pause
// uploads that this same process is currently running.
func (s *Service) ensureUploadJournal() error {
	if s.DB == nil {
		return fmt.Errorf("upload journal: database unavailable")
	}
	s.resumeOnce.Do(func() {
		_, s.resumeErr = s.DB.Exec(`CREATE TABLE IF NOT EXISTS resumable_uploads (
			job_id TEXT PRIMARY KEY,
			channel_id INTEGER NOT NULL,
			account_namespace TEXT NOT NULL,
			source_path TEXT NOT NULL,
			name TEXT NOT NULL,
			parent_id TEXT NOT NULL,
			source_size INTEGER NOT NULL,
			source_sha256 TEXT NOT NULL,
			part_hashes_json TEXT NOT NULL,
			source_mtime_ns INTEGER NOT NULL,
			part_size INTEGER NOT NULL,
			part_count INTEGER NOT NULL,
			history_base INTEGER NOT NULL,
			upload_time INTEGER NOT NULL,
			actor_id INTEGER NOT NULL,
			status TEXT NOT NULL,
			error TEXT NOT NULL DEFAULT '',
			manifest_msg_id INTEGER NOT NULL DEFAULT 0
		)`)
		if s.resumeErr == nil {
			_, s.resumeErr = s.DB.Exec(`CREATE INDEX IF NOT EXISTS idx_resumable_uploads_channel
				ON resumable_uploads(account_namespace, channel_id, status)`)
		}
		if s.resumeErr == nil {
			_, s.resumeErr = s.DB.Exec(`UPDATE resumable_uploads SET status = ?, error = '' WHERE status = ? AND account_namespace = ?`,
				resumePaused, resumeUploading, s.CacheNamespace)
		}
	})
	return s.resumeErr
}

func (s *Service) createUploadJob(ctx context.Context, path, name, parent string, size int64, channelID, actorID int64, plan uploadPartPlan, peer tgclient.InputPeer) (uploadJob, error) {
	if actorID <= 0 || channelID == 0 {
		return uploadJob{}, fmt.Errorf("upload account or channel is unavailable")
	}
	if err := s.ensureUploadJournal(); err != nil {
		return uploadJob{}, err
	}
	hash, partHashes, info, err := hashUploadPlan(ctx, path, size, plan)
	if err != nil {
		return uploadJob{}, err
	}
	partHashesJSON, err := json.Marshal(partHashes)
	if err != nil {
		return uploadJob{}, err
	}
	history, err := s.TG.GetHistory(ctx, peer, 0, 0, 1)
	if err != nil {
		return uploadJob{}, fmt.Errorf("read channel history before upload: %w", err)
	}
	base := int64(0)
	if len(history) > 0 {
		base = history[0].MsgID
	}
	job := uploadJob{
		ID: projection.NewUploadUUID(), ChannelID: channelID, Namespace: s.CacheNamespace,
		Path: path, Name: name, Parent: parent, Size: size, Hash: hash, PartHashes: partHashes,
		ModTimeNS: info.ModTime().UnixNano(), PartSize: plan.partSize,
		PartCount: plan.partCount, HistoryBase: base,
		UploadTime: s.now().Unix(), ActorID: actorID, Status: resumePaused,
	}
	_, err = s.DB.ExecContext(ctx, `INSERT INTO resumable_uploads
		(job_id, channel_id, account_namespace, source_path, name, parent_id, source_size,
		source_sha256, part_hashes_json, source_mtime_ns, part_size, part_count, history_base, upload_time,
		actor_id, status) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		job.ID, job.ChannelID, job.Namespace, job.Path, job.Name, job.Parent,
		job.Size, job.Hash, string(partHashesJSON), job.ModTimeNS, job.PartSize, job.PartCount,
		job.HistoryBase, job.UploadTime, job.ActorID, job.Status)
	if err != nil {
		return uploadJob{}, fmt.Errorf("save upload job: %w", err)
	}
	return job, nil
}

func hashUploadPlan(ctx context.Context, path string, size int64, plan uploadPartPlan) (string, []string, os.FileInfo, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", nil, nil, err
	}
	defer f.Close()
	before, err := f.Stat()
	if err != nil {
		return "", nil, nil, err
	}
	if !before.Mode().IsRegular() || before.Size() != size {
		return "", nil, nil, fmt.Errorf("source file changed or is not regular")
	}
	whole := sha256.New()
	parts := make([]string, plan.partCount)
	for i := range plan.partCount {
		_, length, err := plan.window(size, i)
		if err != nil {
			return "", nil, nil, err
		}
		part := sha256.New()
		if _, err := io.CopyN(io.MultiWriter(whole, part), &contextReader{ctx: ctx, source: f}, length); err != nil {
			return "", nil, nil, fmt.Errorf("hash source part %d: %w", i, err)
		}
		parts[i] = hex.EncodeToString(part.Sum(nil))
	}
	after, err := f.Stat()
	if err != nil {
		return "", nil, nil, err
	}
	if after.Size() != before.Size() || !after.ModTime().Equal(before.ModTime()) {
		return "", nil, nil, fmt.Errorf("source changed while hashing")
	}
	return hex.EncodeToString(whole.Sum(nil)), parts, before, nil
}

func hashUploadSource(ctx context.Context, path string, expectedSize int64) (string, os.FileInfo, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", nil, err
	}
	defer f.Close()
	return hashOpenUploadSource(ctx, f, expectedSize)
}

func hashOpenUploadSource(ctx context.Context, f *os.File, expectedSize int64) (string, os.FileInfo, error) {
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return "", nil, err
	}
	info, err := f.Stat()
	if err != nil {
		return "", nil, err
	}
	if !info.Mode().IsRegular() || info.Size() != expectedSize {
		return "", nil, fmt.Errorf("source file changed or is not a regular file")
	}
	h := sha256.New()
	if _, err := io.Copy(h, &contextReader{ctx: ctx, source: f}); err != nil {
		return "", nil, fmt.Errorf("hash source: %w", err)
	}
	after, err := f.Stat()
	if err != nil {
		return "", nil, err
	}
	if after.Size() != info.Size() || !after.ModTime().Equal(info.ModTime()) {
		return "", nil, fmt.Errorf("source file changed while hashing")
	}
	return hex.EncodeToString(h.Sum(nil)), info, nil
}

func (s *Service) loadUploadJob(ctx context.Context, jobID string) (uploadJob, error) {
	if err := s.ensureUploadJournal(); err != nil {
		return uploadJob{}, err
	}
	var job uploadJob
	var partHashesJSON string
	err := s.DB.QueryRowContext(ctx, `SELECT job_id, channel_id, account_namespace, source_path,
		name, parent_id, source_size, source_sha256, part_hashes_json, source_mtime_ns, part_size, part_count,
		history_base, upload_time, actor_id, status, manifest_msg_id
		FROM resumable_uploads WHERE job_id = ? AND account_namespace = ?`, jobID, s.CacheNamespace).Scan(
		&job.ID, &job.ChannelID, &job.Namespace, &job.Path, &job.Name, &job.Parent,
		&job.Size, &job.Hash, &partHashesJSON, &job.ModTimeNS, &job.PartSize, &job.PartCount,
		&job.HistoryBase, &job.UploadTime, &job.ActorID, &job.Status, &job.ManifestID)
	if errors.Is(err, sql.ErrNoRows) {
		return uploadJob{}, fmt.Errorf("upload job not found")
	}
	if err != nil {
		return uploadJob{}, fmt.Errorf("load upload job: %w", err)
	}
	if err := json.Unmarshal([]byte(partHashesJSON), &job.PartHashes); err != nil || len(job.PartHashes) != job.PartCount {
		return uploadJob{}, fmt.Errorf("invalid upload part hashes")
	}
	return job, nil
}

func (s *Service) setUploadJobStatus(ctx context.Context, jobID, status, detail string) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE resumable_uploads SET status = ?, error = ?
		WHERE job_id = ? AND account_namespace = ?`, status, detail, jobID, s.CacheNamespace)
	return err
}

func (s *Service) resumableUploadView(ctx context.Context, job uploadJob) (ResumableUpload, error) {
	var confirmed sql.NullInt64
	err := s.DB.QueryRowContext(ctx, `SELECT SUM(size) FROM file_parts WHERE channel_id = ? AND upload_uuid = ?`,
		job.ChannelID, job.ID).Scan(&confirmed)
	if err != nil {
		return ResumableUpload{}, err
	}
	var detail string
	err = s.DB.QueryRowContext(ctx, `SELECT error FROM resumable_uploads WHERE job_id = ?`, job.ID).Scan(&detail)
	if err != nil {
		return ResumableUpload{}, err
	}
	return ResumableUpload{
		JobID: job.ID, ChannelID: job.ChannelID, Name: job.Name, ParentID: job.Parent,
		Size: job.Size, StoredSize: job.Size, ConfirmedBytes: confirmed.Int64,
		Status: job.Status, Error: detail,
	}, nil
}

func (s *Service) emitUploadJob(ctx context.Context, job uploadJob, uploadID *int) {
	view, err := s.resumableUploadView(ctx, job)
	if err != nil {
		return
	}
	view.UploadID = uploadID
	s.emitEvent("resumable_upload_changed", view)
}

// ListResumableUploads returns unfinished jobs for the authenticated account.
func (s *Service) ListResumableUploads(ctx context.Context, channelID int64) ([]ResumableUpload, error) {
	if err := s.ensureUploadJournal(); err != nil {
		return nil, err
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT job_id FROM resumable_uploads
		WHERE account_namespace = ? AND channel_id = ? AND status != ? ORDER BY rowid DESC`,
		s.CacheNamespace, channelID, resumeComplete)
	if err != nil {
		return nil, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, err
	}
	_ = rows.Close()
	jobs := make([]ResumableUpload, 0, len(ids))
	for _, id := range ids {
		job, err := s.loadUploadJob(ctx, id)
		if err != nil {
			return nil, err
		}
		view, err := s.resumableUploadView(ctx, job)
		if err != nil {
			return nil, err
		}
		jobs = append(jobs, view)
	}
	return jobs, nil
}

// UploadJobSource is for the native app's private source-file lifecycle. It is
// deliberately separate from ResumableUpload, which is exposed to the UI and
// must never contain a device path.
func (s *Service) UploadJobSource(ctx context.Context, channelID int64, jobID string) (string, error) {
	job, err := s.loadUploadJob(ctx, jobID)
	if err != nil {
		return "", err
	}
	if channelID == 0 || job.ChannelID != channelID {
		return "", fmt.Errorf("upload job belongs to another channel")
	}
	return job.Path, nil
}

// StagedSourceReferenced includes every account and channel in this database:
// a local source must not be removed while any unfinished job still needs it.
func (s *Service) StagedSourceReferenced(ctx context.Context, path string) (bool, error) {
	if err := s.ensureUploadJournal(); err != nil {
		return false, err
	}
	var exists bool
	err := s.DB.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM resumable_uploads
		WHERE source_path = ? AND status != ?)`, path, resumeComplete).Scan(&exists)
	return exists, err
}

func (s *Service) trackResumeCancel(jobID string, cancel context.CancelFunc) func() {
	s.resumeMu.Lock()
	if s.resumeCancels == nil {
		s.resumeCancels = make(map[string]context.CancelFunc)
	}
	s.resumeCancels[jobID] = cancel
	s.resumeMu.Unlock()
	return func() {
		s.resumeMu.Lock()
		delete(s.resumeCancels, jobID)
		s.resumeMu.Unlock()
	}
}

// PauseUpload stops the active send; confirmed part messages remain reusable.
func (s *Service) PauseUpload(expectedChannelID int64, jobID string) bool {
	job, err := s.loadUploadJob(context.Background(), jobID)
	if err != nil || expectedChannelID == 0 || job.ChannelID != expectedChannelID {
		return false
	}
	s.resumeMu.Lock()
	cancel := s.resumeCancels[jobID]
	s.resumeMu.Unlock()
	if cancel == nil {
		return false
	}
	cancel()
	return true
}
