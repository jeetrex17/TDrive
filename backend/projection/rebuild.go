package projection

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"
)

type rawReplayRow struct {
	msgID         int64
	opType        string
	opPayloadJSON string
	actorUserID   int64
}

// RebuildProjection is a full delete-and-replay of channelID's projection
// tables from replay_log. It is expensive (proportional to the channel's
// entire history) and otherwise invisible, so its start/end/outcome is
// always logged at Info level regardless of the configured verbosity.
func RebuildProjection(db *sql.DB, channelID int64) error {
	start := time.Now()
	slog.Info("projection: rebuild starting", "channel_id", channelID)
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("projection: rebuild begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	applied, rejected, err := rebuildProjectionTx(tx, channelID)
	if err != nil {
		slog.Error("projection: rebuild failed", "channel_id", channelID, "elapsed", time.Since(start), "error", err)
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("projection: rebuild commit: %w", err)
	}
	slog.Info("projection: rebuild completed", "channel_id", channelID, "elapsed", time.Since(start), "applied", applied, "rejected", rejected)
	return nil
}

func rebuildProjectionTx(tx *sql.Tx, channelID int64) (applied, rejected int, err error) {
	if _, err := tx.Exec(`DELETE FROM files WHERE channel_id = ?`, channelID); err != nil {
		return 0, 0, fmt.Errorf("projection: rebuild clear files: %w", err)
	}
	if _, err := tx.Exec(`DELETE FROM folders WHERE channel_id = ?`, channelID); err != nil {
		return 0, 0, fmt.Errorf("projection: rebuild clear folders: %w", err)
	}
	if _, err := tx.Exec(`DELETE FROM encryption WHERE channel_id = ?`, channelID); err != nil {
		return 0, 0, fmt.Errorf("projection: rebuild clear encryption: %w", err)
	}
	if _, err := tx.Exec(`DELETE FROM file_parts WHERE channel_id = ?`, channelID); err != nil {
		return 0, 0, fmt.Errorf("projection: rebuild clear file_parts: %w", err)
	}
	if _, err := tx.Exec(`DELETE FROM replay_log_rejects WHERE channel_id = ?`, channelID); err != nil {
		return 0, 0, fmt.Errorf("projection: rebuild clear replay_log_rejects: %w", err)
	}
	for table, label := range map[string]string{
		"dirents":               "dirents",
		"file_revisions":        "file revisions",
		"projection_operations": "projection operations",
		"trash_entries":         "trash entries",
	} {
		if _, err := tx.Exec(`DELETE FROM `+table+` WHERE channel_id = ?`, channelID); err != nil {
			return 0, 0, fmt.Errorf("projection: rebuild clear %s: %w", label, err)
		}
	}

	rows, err := tx.Query(`
		SELECT msg_id, op_type, op_payload_json, actor_user_id
		FROM replay_log
		WHERE channel_id = ?
		ORDER BY msg_id ASC
	`, channelID)
	if err != nil {
		return 0, 0, fmt.Errorf("projection: rebuild scan replay_log: %w", err)
	}

	var queue []rawReplayRow
	for rows.Next() {
		var r rawReplayRow
		if err := rows.Scan(&r.msgID, &r.opType, &r.opPayloadJSON, &r.actorUserID); err != nil {
			_ = rows.Close()
			return 0, 0, fmt.Errorf("projection: rebuild row scan: %w", err)
		}
		queue = append(queue, r)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return 0, 0, err
	}
	_ = rows.Close()

	for _, r := range queue {
		var op Op
		if err := json.Unmarshal([]byte(r.opPayloadJSON), &op); err != nil {
			return applied, rejected, fmt.Errorf("projection: rebuild parse op msg=%d: %w", r.msgID, err)
		}
		if string(op.Type) == "" {
			op.Type = OpType(r.opType)
		}
		if err := ApplyOp(tx, channelID, r.msgID, op, r.actorUserID); err != nil {
			if isSkippableApplyError(err) {
				slog.Warn("projection: rebuild rejected op, continuing", "channel_id", channelID, "msg_id", r.msgID, "op_type", op.Type, "error", err)
				if recErr := recordSkippedOp(tx, channelID, r.msgID, op, err); recErr != nil {
					return applied, rejected, recErr
				}
				rejected++
				continue
			}
			return applied, rejected, fmt.Errorf("projection: rebuild apply msg=%d: %w", r.msgID, err)
		}
		applied++
	}
	return applied, rejected, nil
}

// RebuildProjectionTx replays a channel's whole replay_log inside a caller's
// transaction, and clears the rebuild flag that requested it.
//
// A history scan that reaches ops it had never seen before cannot simply apply
// them: ops already in the log were skipped as "already applied" the first time
// round, including the renames and deletes that silently no-oped because their
// target had not been read yet. Replaying the completed log from scratch is the
// only way to land those in order. Sharing the caller's transaction keeps the
// repair atomic with the scan that earned it.
func RebuildProjectionTx(tx *sql.Tx, channelID int64) (applied, rejected int, err error) {
	applied, rejected, err = rebuildProjectionTx(tx, channelID)
	if err != nil {
		return 0, 0, err
	}
	if _, err := tx.Exec(
		`UPDATE channels SET needs_projection_rebuild = 0 WHERE channel_id = ?`, channelID,
	); err != nil {
		return 0, 0, fmt.Errorf("projection: clear rebuild flag: %w", err)
	}
	return applied, rejected, nil
}
