package file

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"TDrive/backend/channelsource"
	"TDrive/backend/projection"
)

const (
	sourceCopyPending  = "pending"
	sourceCopySaved    = "saved"
	sourceCopyFailed   = "failed"
	sourceCopyCanceled = "canceled"
)

// sourceCopyJob retains only explicit source identity and destination receipts.
// It never holds Telegram file references or downloaded bytes.
type sourceCopyJob struct {
	Result    SourceCopyResult
	Source    channelsource.CopySource
	ChannelID int64
	BatchID   string
	StartedAt int64

	Parts []sourceCopyPart
	// Attempt pins a name and its operation ID before the commit write.
	Attempt    int
	Candidate  string
	CommitID   int64
	CleaningUp bool
}

type sourceCopyPart struct {
	SourceID   int64
	DocumentID int64
	Size       int64
	BodyID     int64
}

func (job sourceCopyJob) result() SourceCopyResult {
	result := job.Result
	if result.Status == sourceCopySaved {
		result.Name = job.Candidate
	}
	if result.Name == "" {
		result.Name = "Telegram file"
	}
	return result
}

func (s *Service) ensureSourceCopyJournal(ctx context.Context) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("open local save history: %w", err)
	}
	defer tx.Rollback()
	_, err = tx.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS source_copy_jobs (
			id TEXT PRIMARY KEY,
			channel_id INTEGER NOT NULL,
			account_id INTEGER NOT NULL,
			peer_kind TEXT NOT NULL,
			peer_id INTEGER NOT NULL,
			generation TEXT NOT NULL,
			message_id INTEGER NOT NULL,
			identity TEXT NOT NULL,
			parent_id TEXT NOT NULL,
			started_at INTEGER NOT NULL,
			name TEXT NOT NULL,
			status TEXT NOT NULL,
			status_message TEXT NOT NULL DEFAULT '',
			file_id INTEGER NOT NULL DEFAULT 0,
			document_id INTEGER NOT NULL DEFAULT 0,
			size INTEGER NOT NULL DEFAULT 0,
			body_id INTEGER NOT NULL DEFAULT 0,
			attempt INTEGER NOT NULL DEFAULT 0,
			candidate TEXT NOT NULL DEFAULT '',
			commit_id INTEGER NOT NULL DEFAULT 0,
			cleaning_up INTEGER NOT NULL DEFAULT 0,
			body_parts TEXT NOT NULL DEFAULT '',
			batch_id TEXT NOT NULL DEFAULT ''
		);
		DROP INDEX IF EXISTS source_copy_jobs_by_scope;
		CREATE INDEX IF NOT EXISTS source_copy_jobs_pending_by_scope
			ON source_copy_jobs (account_id, channel_id) WHERE status='pending';
		CREATE INDEX IF NOT EXISTS source_copy_jobs_finished_by_scope
			ON source_copy_jobs (account_id, channel_id) WHERE status<>'pending';
	`)
	if err != nil {
		return fmt.Errorf("open local save history: %w", err)
	}
	var hasParts, hasBatch bool
	err = tx.QueryRowContext(ctx, `SELECT
		EXISTS (SELECT 1 FROM pragma_table_info('source_copy_jobs') WHERE name='body_parts'),
		EXISTS (SELECT 1 FROM pragma_table_info('source_copy_jobs') WHERE name='batch_id')
	`).Scan(&hasParts, &hasBatch)
	if err != nil {
		return fmt.Errorf("read local save history version: %w", err)
	}
	if !hasParts {
		if _, err := tx.ExecContext(ctx, `ALTER TABLE source_copy_jobs ADD COLUMN body_parts TEXT NOT NULL DEFAULT ''`); err != nil {
			return fmt.Errorf("upgrade local save history: %w", err)
		}
	}
	if !hasBatch {
		if _, err := tx.ExecContext(ctx, `ALTER TABLE source_copy_jobs ADD COLUMN batch_id TEXT NOT NULL DEFAULT ''`); err != nil {
			return fmt.Errorf("upgrade local save batch history: %w", err)
		}
	}
	if _, err := tx.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS source_copy_jobs_by_batch
		ON source_copy_jobs (account_id, channel_id, batch_id) WHERE batch_id <> ''`); err != nil {
		return fmt.Errorf("index local save batches: %w", err)
	}
	return tx.Commit()
}

func (s *Service) createSourceCopyJobs(ctx context.Context, channelID, accountID int64, parent string, items []SourceCopyInput, batchID string) ([]sourceCopyJob, error) {
	if err := s.ensureSourceCopyJournal(ctx); err != nil {
		return nil, err
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var exists bool
	err = tx.QueryRowContext(ctx, `
		SELECT EXISTS (SELECT 1 FROM source_copy_jobs
		WHERE channel_id=? AND account_id=? AND batch_id=? AND batch_id<>'')
	`, channelID, accountID, batchID).Scan(&exists)
	if err != nil {
		return nil, fmt.Errorf("check local save batch: %w", err)
	}
	if exists {
		return nil, errors.New("This copy batch is already recorded. Check its results in Activity")
	}
	jobs := make([]sourceCopyJob, 0, len(items))
	for _, item := range items {
		job := sourceCopyJob{
			Result:    SourceCopyResult{ID: projection.NewUploadUUID(), Name: item.Name, ParentID: parent, Status: sourceCopyPending},
			Source:    item.Source,
			ChannelID: channelID,
			BatchID:   batchID,
			StartedAt: s.now().Unix(),
		}
		if job.Result.Name != "" {
			job.Result.Name = NormalizeSourceCopyName(job.Result.Name)
		}
		_, err := tx.ExecContext(ctx, `
			INSERT INTO source_copy_jobs(id, channel_id, account_id, peer_kind, peer_id, generation,
				message_id, identity, parent_id, started_at, name, status, batch_id)
			VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		`, job.Result.ID, channelID, accountID, job.Source.PeerKind, job.Source.PeerID, job.Source.Generation,
			job.Source.MsgID, job.Source.Identity, parent, job.StartedAt, job.Result.Name, job.Result.Status, batchID)
		if err != nil {
			return nil, fmt.Errorf("record selected Telegram file: %w", err)
		}
		jobs = append(jobs, job)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("record Telegram selection: %w", err)
	}
	return jobs, nil
}

const sourceCopyJobColumns = `
	id, channel_id, account_id, peer_kind, peer_id, generation, message_id, identity,
	parent_id, started_at, name, status, status_message, file_id,
	document_id, size, body_id, attempt, candidate, commit_id, cleaning_up, body_parts, batch_id
`

func scanSourceCopyJob(row interface{ Scan(...any) error }) (sourceCopyJob, error) {
	var job sourceCopyJob
	var singleton sourceCopyPart
	var parts string
	err := row.Scan(
		&job.Result.ID, &job.ChannelID, &job.Source.AccountID, &job.Source.PeerKind, &job.Source.PeerID,
		&job.Source.Generation, &job.Source.MsgID, &job.Source.Identity, &job.Result.ParentID, &job.StartedAt,
		&job.Result.Name, &job.Result.Status, &job.Result.Message, &job.Result.FileID,
		&singleton.DocumentID, &singleton.Size, &singleton.BodyID, &job.Attempt, &job.Candidate, &job.CommitID, &job.CleaningUp, &parts, &job.BatchID,
	)
	if err != nil {
		return job, err
	}
	if parts != "" {
		if err := json.Unmarshal([]byte(parts), &job.Parts); err != nil {
			return job, fmt.Errorf("read saved copy parts: %w", err)
		}
		return job, nil
	}
	// Older versions journaled only one document, using the file's source
	// message as its body message. Keep its exact send ID and receipt.
	if singleton.DocumentID != 0 {
		singleton.SourceID = job.Source.MsgID
		job.Parts = []sourceCopyPart{singleton}
	}
	return job, nil
}

func (s *Service) loadSourceCopyJob(ctx context.Context, channelID, accountID int64, id string) (sourceCopyJob, error) {
	if err := s.ensureSourceCopyJournal(ctx); err != nil {
		return sourceCopyJob{}, err
	}
	job, err := scanSourceCopyJob(s.DB.QueryRowContext(ctx, `
		SELECT `+sourceCopyJobColumns+` FROM source_copy_jobs
		WHERE channel_id=? AND account_id=? AND id=?
	`, channelID, accountID, id))
	if errors.Is(err, sql.ErrNoRows) {
		return sourceCopyJob{}, errors.New("Save operation not found")
	}
	return job, err
}

// Select receipt row IDs first so only pending jobs and the newest finished
// jobs reach the table lookup. Each scoped index orders ties by row ID.
const sourceCopyHistoryQuery = `
	SELECT id, name, parent_id, status, file_id, status_message, candidate FROM source_copy_jobs
	WHERE rowid IN (
		SELECT rowid FROM source_copy_jobs WHERE channel_id=? AND account_id=? AND status='pending'
		UNION ALL
		SELECT rowid FROM (
			SELECT rowid FROM source_copy_jobs WHERE channel_id=? AND account_id=? AND status<>'pending'
			ORDER BY rowid DESC LIMIT 100
		)
	)
	ORDER BY rowid DESC
`

func (s *Service) listSourceCopyJobs(ctx context.Context, channelID, accountID int64) ([]SourceCopyResult, error) {
	if err := s.ensureSourceCopyJournal(ctx); err != nil {
		return nil, err
	}
	rows, err := s.DB.QueryContext(ctx, sourceCopyHistoryQuery, channelID, accountID, channelID, accountID)
	return readSourceCopyResults(rows, err)
}

func (s *Service) listSourceCopyBatch(ctx context.Context, channelID, accountID int64, batchID string) ([]SourceCopyResult, error) {
	if err := s.ensureSourceCopyJournal(ctx); err != nil {
		return nil, err
	}
	rows, err := s.DB.QueryContext(ctx, `
		SELECT id, name, parent_id, status, file_id, status_message, candidate FROM source_copy_jobs
		WHERE channel_id=? AND account_id=? AND batch_id=? AND batch_id<>''
		ORDER BY rowid
	`, channelID, accountID, batchID)
	return readSourceCopyResults(rows, err)
}

func readSourceCopyResults(rows *sql.Rows, err error) ([]SourceCopyResult, error) {
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	results := make([]SourceCopyResult, 0)
	for rows.Next() {
		var job sourceCopyJob
		result := &job.Result
		err := rows.Scan(&result.ID, &result.Name, &result.ParentID, &result.Status, &result.FileID, &result.Message, &job.Candidate)
		if err != nil {
			return nil, err
		}
		results = append(results, job.result())
	}
	return results, rows.Err()
}

func (s *Service) persistSourceCopyJob(ctx context.Context, job sourceCopyJob) error {
	parts, err := json.Marshal(job.Parts)
	if err != nil {
		return fmt.Errorf("encode copy parts: %w", err)
	}
	var legacy sourceCopyPart
	if len(job.Parts) == 1 && job.Source.PeerKind != SharedCopyPeerKind {
		legacy = job.Parts[0]
	}
	result, err := s.DB.ExecContext(ctx, `
		UPDATE source_copy_jobs SET name=?, status=?, status_message=?, file_id=?, document_id=?, size=?,
			body_id=?, attempt=?, candidate=?, commit_id=?, cleaning_up=?, body_parts=?
		WHERE channel_id=? AND account_id=? AND id=?
	`, job.Result.Name, job.Result.Status, job.Result.Message, job.Result.FileID, legacy.DocumentID, legacy.Size,
		legacy.BodyID, job.Attempt, job.Candidate, job.CommitID, job.CleaningUp, string(parts),
		job.ChannelID, job.Source.AccountID, job.Result.ID)
	if err != nil {
		return fmt.Errorf("record Telegram save progress: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count != 1 {
		return errors.New("Save operation is unavailable")
	}
	return nil
}
