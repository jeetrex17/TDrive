package projection

import (
	"database/sql"
	"fmt"
)

// FileMessageRefs is one live file's backing Telegram message IDs: its own
// root message plus, for a multipart upload, every part message. Used by
// external-delete reconciliation to check whether a file's content is still
// present on Telegram.
type FileMessageRefs struct {
	FileMsgID     int64
	BackingMsgIDs []int64
}

// LiveFileMessageIDs returns every live (non-tombstoned) file in channelID
// together with all Telegram message IDs that back its content.
func LiveFileMessageIDs(db *sql.DB, channelID int64) ([]FileMessageRefs, error) {
	rows, err := db.Query(`
		SELECT msg_id FROM files
		WHERE channel_id = ? AND tombstoned = 0
	`, channelID)
	if err != nil {
		return nil, fmt.Errorf("projection: list live file msg ids: %w", err)
	}
	defer rows.Close()

	var msgIDs []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("projection: scan live file msg id: %w", err)
		}
		msgIDs = append(msgIDs, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("projection: list live file msg ids: %w", err)
	}

	out := make([]FileMessageRefs, 0, len(msgIDs))
	for _, id := range msgIDs {
		backing := []int64{id}
		parts, err := MultipartParts(db, channelID, id)
		if err != nil {
			return nil, fmt.Errorf("projection: load multipart parts for msg=%d: %w", id, err)
		}
		for _, p := range parts {
			backing = append(backing, p.MsgID)
		}
		out = append(out, FileMessageRefs{FileMsgID: id, BackingMsgIDs: backing})
	}
	return out, nil
}
