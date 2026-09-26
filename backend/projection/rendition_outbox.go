package projection

import (
	"context"
	"database/sql"
	"fmt"
)

const maxRenditionOutboxBytes int64 = 64 << 20

// PendingRendition is an immutable send intent. Only small derivative payloads
// enter this outbox; originals always use the existing streaming upload path.
type PendingRendition struct {
	ChannelID int64
	JobID     string
	RandomID  int64
	Header    string
	Payload   []byte
	CreatedAt int64
}

func QueueRendition(ctx context.Context, db *sql.DB, job PendingRendition) error {
	if ctx == nil || len(job.JobID) != 64 || job.RandomID <= 0 || job.CreatedAt <= 0 {
		return fmt.Errorf("projection: invalid rendition send intent")
	}
	op, err := Parse(job.Header)
	if err != nil || op.Rendition == nil || op.Rendition.ChannelID != job.ChannelID || op.FileSize != int64(len(job.Payload)) {
		return fmt.Errorf("projection: invalid rendition outbox payload")
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// Existing intent wins, including its independently generated ciphertext.
	var exists int
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM pending_rendition_uploads WHERE channel_id=? AND job_id=?)`, job.ChannelID, job.JobID).Scan(&exists); err != nil {
		return err
	}
	if exists != 0 {
		return nil
	}
	var used int64
	var count int
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(SUM(LENGTH(payload)),0),COUNT(*) FROM pending_rendition_uploads`).Scan(&used, &count); err != nil {
		return err
	}
	if count >= 128 || int64(len(job.Payload)) > maxRenditionOutboxBytes-used {
		return fmt.Errorf("projection: pending rendition storage budget reached")
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO pending_rendition_uploads(channel_id,job_id,random_id,header,payload,created_at) VALUES(?,?,?,?,?,?)`, job.ChannelID, job.JobID, job.RandomID, job.Header, job.Payload, job.CreatedAt)
	if err != nil {
		return err
	}
	return tx.Commit()
}

// PendingRenditionIDs never loads payloads. Workers pull one bounded blob at a
// time rather than materializing a whole offline preparation backlog in RAM.
func PendingRenditionIDs(ctx context.Context, db *sql.DB, channelID int64, limit int) ([]string, error) {
	if limit < 1 || limit > 128 {
		return nil, fmt.Errorf("projection: invalid rendition resume limit")
	}
	rows, err := db.QueryContext(ctx, `SELECT job_id FROM pending_rendition_uploads WHERE channel_id=? ORDER BY created_at,job_id LIMIT ?`, channelID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}
func LoadPendingRendition(ctx context.Context, db *sql.DB, channelID int64, jobID string) (PendingRendition, error) {
	var job PendingRendition
	err := db.QueryRowContext(ctx, `SELECT channel_id,job_id,random_id,header,payload,created_at FROM pending_rendition_uploads WHERE channel_id=? AND job_id=?`, channelID, jobID).Scan(&job.ChannelID, &job.JobID, &job.RandomID, &job.Header, &job.Payload, &job.CreatedAt)
	return job, err
}
func CompletePendingRendition(ctx context.Context, db *sql.DB, channelID int64, jobID string) error {
	_, err := db.ExecContext(ctx, `DELETE FROM pending_rendition_uploads WHERE channel_id=? AND job_id=?`, channelID, jobID)
	return err
}
