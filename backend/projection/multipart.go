package projection

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"strings"
)

// FilePart is one stored segment of a multipart large file, ordered by
// PartIndex. MsgID is the Telegram document message holding the part bytes;
// Size is that document's stored (ciphertext) size.
type FilePart struct {
	PartIndex int
	MsgID     int64
	Size      int64
}

// MultipartParts returns the ordered parts of the multipart file identified by
// its manifest msg_id. It returns nil (and no error) for a normal single-message
// file, i.e. one whose files row carries no upload_uuid — callers treat a nil
// result as "not multipart, use the single-message path".
func MultipartParts(db *sql.DB, channelID int64, fileMsgID int64) ([]FilePart, error) {
	return MultipartPartsContext(context.Background(), db, channelID, fileMsgID)
}

// MultipartPartsContext is MultipartParts with cancellation propagated to all
// SQLite queries used to resolve the manifest and its ordered parts.
func MultipartPartsContext(ctx context.Context, db *sql.DB, channelID int64, fileMsgID int64) ([]FilePart, error) {
	if err := validateContext(ctx, "resolve multipart parts"); err != nil {
		return nil, err
	}
	var uuid string
	err := db.QueryRowContext(ctx, `
		SELECT upload_uuid FROM files
		WHERE channel_id = ? AND msg_id = ?
	`, channelID, fileMsgID).Scan(&uuid)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("projection: load multipart upload id: %w", err)
	}
	if uuid == "" {
		return nil, nil
	}
	return PartsForUUIDContext(ctx, db, channelID, uuid)
}

// PartsForUUID returns the parts of an upload grouped by upload_uuid, ordered by
// part_index ascending.
func PartsForUUID(db *sql.DB, channelID int64, uuid string) ([]FilePart, error) {
	return PartsForUUIDContext(context.Background(), db, channelID, uuid)
}

// PartsForUUIDContext is PartsForUUID with cancellation propagated to SQLite.
func PartsForUUIDContext(ctx context.Context, db *sql.DB, channelID int64, uuid string) ([]FilePart, error) {
	if err := validateContext(ctx, "list multipart parts"); err != nil {
		return nil, err
	}
	rows, err := db.QueryContext(ctx, `
		SELECT part_index, msg_id, size FROM file_parts
		WHERE channel_id = ? AND upload_uuid = ?
		ORDER BY part_index ASC
	`, channelID, uuid)
	if err != nil {
		return nil, fmt.Errorf("projection: list multipart parts: %w", err)
	}
	defer rows.Close()
	var out []FilePart
	for rows.Next() {
		var p FilePart
		if err := rows.Scan(&p.PartIndex, &p.MsgID, &p.Size); err != nil {
			return nil, fmt.Errorf("projection: scan multipart part: %w", err)
		}
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("projection: iterate multipart parts: %w", err)
	}
	return out, nil
}

// OrphanPartMessages returns Telegram msg_ids of multipart parts whose manifest
// row exists but is tombstoned — i.e. a deleted file whose part-body cleanup
// didn't finish. The GC sweep deletes these from Telegram and drops the rows.
//
// It deliberately does NOT return parts that have *no* manifest row: those could
// be an upload still in flight (this client, another instance, or another user
// on a shared drive) whose manifest simply hasn't landed yet, and deleting them
// would corrupt that upload. A failed/canceled upload from this client cleans up
// its own parts inline (see uploadMultipart's abort), so the only orphans we act
// on here are the safe, definitely-dead ones behind a tombstone.
func OrphanPartMessages(db *sql.DB, channelID int64) ([]int64, error) {
	rows, err := db.Query(`
		SELECT fp.msg_id FROM file_parts fp
		WHERE fp.channel_id = ?
		  AND EXISTS (
			SELECT 1 FROM files f
			WHERE f.channel_id = fp.channel_id
			  AND f.upload_uuid = fp.upload_uuid
			  AND f.tombstoned = 1
		  )
		ORDER BY fp.msg_id ASC
	`, channelID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(ids) > 0 {
		slog.Debug("projection: found orphan part messages for tombstoned uploads", "channel_id", channelID, "count", len(ids))
	}
	return ids, nil
}

// DeleteFileParts removes all file_parts rows for an upload uuid. Used by the
// upload orchestrator's clean-up-and-fail path.
func DeleteFileParts(db *sql.DB, channelID int64, uuid string) error {
	_, err := db.Exec(`
		DELETE FROM file_parts WHERE channel_id = ? AND upload_uuid = ?
	`, channelID, uuid)
	return err
}

// DeleteFilePartsByMsgIDs removes file_parts rows by their Telegram msg_ids.
// Used by the GC sweep after the corresponding messages are deleted from
// Telegram. Chunked to stay under SQLite's bound-variable limit.
func DeleteFilePartsByMsgIDs(db *sql.DB, channelID int64, msgIDs []int64) error {
	return deleteByMsgIDs(db, "file_parts", channelID, msgIDs)
}

// MultipartPartMsgIDsForFiles returns all part document msg_ids behind the given
// live multipart manifest msg_ids. It is chunked so folder-delete cleanup can
// collect part bodies with one set-based query per chunk instead of one
// MultipartParts lookup per file.
func MultipartPartMsgIDsForFiles(db *sql.DB, channelID int64, fileMsgIDs []int64) ([]int64, error) {
	const chunk = 500
	var out []int64
	for start := 0; start < len(fileMsgIDs); start += chunk {
		end := min(start+chunk, len(fileMsgIDs))
		batch := fileMsgIDs[start:end]
		placeholders := make([]string, len(batch))
		args := make([]any, 0, len(batch)+1)
		args = append(args, channelID)
		for i, id := range batch {
			placeholders[i] = "?"
			args = append(args, id)
		}
		rows, err := db.Query(`
			SELECT fp.msg_id
			FROM files f
			JOIN file_parts fp
			  ON fp.channel_id = f.channel_id
			 AND fp.upload_uuid = f.upload_uuid
			WHERE f.channel_id = ?
			  AND f.msg_id IN (`+strings.Join(placeholders, ",")+`)
			  AND f.upload_uuid != ''
			ORDER BY f.msg_id ASC, fp.part_index ASC
		`, args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var id int64
			if err := rows.Scan(&id); err != nil {
				_ = rows.Close()
				return nil, err
			}
			out = append(out, id)
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return nil, err
		}
		_ = rows.Close()
	}
	return out, nil
}

func deleteByMsgIDs(db *sql.DB, table string, channelID int64, msgIDs []int64) error {
	const chunk = 500
	for start := 0; start < len(msgIDs); start += chunk {
		end := min(start+chunk, len(msgIDs))
		batch := msgIDs[start:end]
		placeholders := make([]string, len(batch))
		args := make([]any, 0, len(batch)+1)
		args = append(args, channelID)
		for i, id := range batch {
			placeholders[i] = "?"
			args = append(args, id)
		}
		// table is an internal constant, never user input.
		q := "DELETE FROM " + table + " WHERE channel_id = ? AND msg_id IN (" + strings.Join(placeholders, ",") + ")"
		if _, err := db.Exec(q, args...); err != nil {
			return err
		}
	}
	return nil
}

// MultipartComplete verifies that a multipart file's parts form a complete,
// contiguous, correctly-sized set per the manifest, so a sync gap or a missing
// part can't yield a silently-truncated download. parts must be ordered by
// part_index ascending (as PartsForUUID returns them).
func MultipartComplete(db *sql.DB, channelID, fileMsgID int64, parts []FilePart) error {
	return MultipartCompleteContext(context.Background(), db, channelID, fileMsgID, parts)
}

// MultipartCompleteContext is MultipartComplete with cancellation propagated
// to the manifest metadata lookup.
func MultipartCompleteContext(ctx context.Context, db *sql.DB, channelID, fileMsgID int64, parts []FilePart) error {
	if err := validateContext(ctx, "validate multipart completeness"); err != nil {
		return err
	}
	var (
		partCount int
		size      int64
	)
	if err := db.QueryRowContext(ctx, `
		SELECT part_count, size FROM files WHERE channel_id = ? AND msg_id = ?
	`, channelID, fileMsgID).Scan(&partCount, &size); err != nil {
		return fmt.Errorf("projection: load multipart manifest: %w", err)
	}
	if len(parts) != partCount {
		return fmt.Errorf("multipart file is incomplete: have %d of %d parts", len(parts), partCount)
	}
	if size < 0 {
		return fmt.Errorf("multipart file is invalid: negative size %d", size)
	}
	var sum int64
	for i, p := range parts {
		if p.PartIndex != i {
			return fmt.Errorf("multipart file is incomplete: missing part %d", i)
		}
		if p.Size < 0 {
			return fmt.Errorf("multipart file is invalid: part %d has negative size %d", i, p.Size)
		}
		if p.Size > math.MaxInt64-sum {
			return fmt.Errorf("multipart file is invalid: parts exceed expected size %d", size)
		}
		sum += p.Size
		if sum > size {
			return fmt.Errorf("multipart file is invalid: parts exceed expected size %d", size)
		}
	}
	if sum != size {
		return fmt.Errorf("multipart file is incomplete: parts total %d bytes, expected %d", sum, size)
	}
	slog.Debug("projection: multipart file verified complete", "channel_id", channelID, "file_msg_id", fileMsgID, "parts", partCount, "size", size)
	return nil
}

// QueuePartCleanup records part message ids whose bodies couldn't be deleted
// during a failed multipart upload's cleanup, so a later sweep can retry them.
// pending_part_cleanup is local-only (never synced, never replayed), so it can
// only ever hold this client's own abandoned parts and cannot cause another
// client's in-flight upload to be swept. It also survives a projection rebuild.
func QueuePartCleanup(db *sql.DB, channelID int64, msgIDs []int64) error {
	for _, id := range msgIDs {
		if _, err := db.Exec(`INSERT OR IGNORE INTO pending_part_cleanup (channel_id, msg_id) VALUES (?, ?)`, channelID, id); err != nil {
			return err
		}
	}
	return nil
}

// PendingPartCleanup returns the queued part message ids awaiting a retry delete.
func PendingPartCleanup(db *sql.DB, channelID int64) ([]int64, error) {
	rows, err := db.Query(`SELECT msg_id FROM pending_part_cleanup WHERE channel_id = ? ORDER BY msg_id ASC`, channelID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// ClearPartCleanup removes queued ids once their bodies are deleted.
func ClearPartCleanup(db *sql.DB, channelID int64, msgIDs []int64) error {
	return deleteByMsgIDs(db, "pending_part_cleanup", channelID, msgIDs)
}
