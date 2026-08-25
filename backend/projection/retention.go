package projection

import (
	"database/sql"
	"fmt"
	"log/slog"
)

const retainedFileRevisionCount = 5

// ListPrunableFileRevisions returns expired immutable bodies outside the latest
// five content revisions of each logical file. It does not delete metadata or
// Telegram bodies; cleanup workers remove the remote body first and then call
// DeletePrunableFileRevision.
func ListPrunableFileRevisions(db *sql.DB, now int64, limit int) ([]FileRevisionRef, error) {
	if db == nil {
		return nil, fmt.Errorf("projection: list prunable revisions: db is nil")
	}
	if now <= 0 {
		return nil, fmt.Errorf("projection: list prunable revisions: current time required")
	}
	if limit <= 0 || limit > 1000 {
		return nil, fmt.Errorf("projection: list prunable revisions: limit must be 1..1000")
	}
	rows, err := db.Query(`
		WITH ranked AS (
			SELECT channel_id, file_msg_id, revision, content_msg_id,
			       upload_uuid, part_count, retained_until,
			       ROW_NUMBER() OVER (
				   PARTITION BY channel_id, file_msg_id
				   ORDER BY revision DESC
			       ) AS newest_rank
			FROM file_revisions
		)
		SELECT channel_id, file_msg_id, revision, content_msg_id,
		       upload_uuid, part_count, retained_until
		FROM ranked
		WHERE newest_rank > ? AND retained_until > 0 AND retained_until <= ?
		ORDER BY retained_until, channel_id, file_msg_id, revision
		LIMIT ?
	`, retainedFileRevisionCount, now, limit)
	if err != nil {
		return nil, fmt.Errorf("projection: list prunable revisions: %w", err)
	}
	defer rows.Close()
	var refs []FileRevisionRef
	for rows.Next() {
		var ref FileRevisionRef
		if err := rows.Scan(
			&ref.ChannelID, &ref.FileMsgID, &ref.Revision, &ref.ContentMsgID,
			&ref.UploadUUID, &ref.PartCount, &ref.RetainedUntil,
		); err != nil {
			return nil, fmt.Errorf("projection: scan prunable revision: %w", err)
		}
		refs = append(refs, ref)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("projection: iterate prunable revisions: %w", err)
	}
	slog.Debug("projection: listed prunable file revisions", "now", now, "limit", limit, "found", len(refs))
	return refs, nil
}

// DeletePrunableFileRevision removes one already-cleaned revision only if its
// retention expired and it is still outside the latest-five safety window.
func DeletePrunableFileRevision(db *sql.DB, ref FileRevisionRef, now int64) (bool, error) {
	if db == nil {
		return false, fmt.Errorf("projection: delete prunable revision: db is nil")
	}
	if ref.ChannelID == 0 || ref.FileMsgID <= 0 || ref.Revision <= 0 || now <= 0 {
		return false, fmt.Errorf("projection: delete prunable revision: invalid identity or time")
	}
	result, err := db.Exec(`
		DELETE FROM file_revisions
		WHERE channel_id=? AND file_msg_id=? AND revision=?
		  AND retained_until > 0 AND retained_until <= ?
		  AND revision IN (
			SELECT revision FROM (
				SELECT revision,
				       ROW_NUMBER() OVER (ORDER BY revision DESC) AS newest_rank
				FROM file_revisions
				WHERE channel_id=? AND file_msg_id=?
			) WHERE newest_rank > ?
		  )
	`, ref.ChannelID, ref.FileMsgID, ref.Revision, now,
		ref.ChannelID, ref.FileMsgID, retainedFileRevisionCount)
	if err != nil {
		return false, fmt.Errorf("projection: delete prunable revision: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("projection: count deleted revision: %w", err)
	}
	return count == 1, nil
}
