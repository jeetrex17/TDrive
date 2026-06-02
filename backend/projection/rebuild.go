package projection

import (
	"database/sql"
	"encoding/json"
	"fmt"
)

type rawReplayRow struct {
	msgID         int64
	opType        string
	opPayloadJSON string
	actorUserID   int64
}

func RebuildProjection(db *sql.DB, channelID int64) error {
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("projection: rebuild begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.Exec(`DELETE FROM files WHERE channel_id = ?`, channelID); err != nil {
		return fmt.Errorf("projection: rebuild clear files: %w", err)
	}
	if _, err := tx.Exec(`DELETE FROM folders WHERE channel_id = ?`, channelID); err != nil {
		return fmt.Errorf("projection: rebuild clear folders: %w", err)
	}
	if _, err := tx.Exec(`DELETE FROM encryption WHERE channel_id = ?`, channelID); err != nil {
		return fmt.Errorf("projection: rebuild clear encryption: %w", err)
	}

	rows, err := tx.Query(`
		SELECT msg_id, op_type, op_payload_json, actor_user_id
		FROM replay_log
		WHERE channel_id = ?
		ORDER BY msg_id ASC
	`, channelID)
	if err != nil {
		return fmt.Errorf("projection: rebuild scan replay_log: %w", err)
	}

	var queue []rawReplayRow
	for rows.Next() {
		var r rawReplayRow
		if err := rows.Scan(&r.msgID, &r.opType, &r.opPayloadJSON, &r.actorUserID); err != nil {
			_ = rows.Close()
			return fmt.Errorf("projection: rebuild row scan: %w", err)
		}
		queue = append(queue, r)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return err
	}
	_ = rows.Close()

	for _, r := range queue {
		var op Op
		if err := json.Unmarshal([]byte(r.opPayloadJSON), &op); err != nil {
			return fmt.Errorf("projection: rebuild parse op msg=%d: %w", r.msgID, err)
		}
		if string(op.Type) == "" {
			op.Type = OpType(r.opType)
		}
		if err := ApplyOp(tx, channelID, r.msgID, op, r.actorUserID); err != nil {
			if isSkippableApplyError(err) {
				if recErr := recordReject(tx, channelID, r.msgID, err); recErr != nil {
					return recErr
				}
				continue
			}
			return fmt.Errorf("projection: rebuild apply msg=%d: %w", r.msgID, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("projection: rebuild commit: %w", err)
	}
	return nil
}
