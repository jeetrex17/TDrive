package projection

import (
	"database/sql"
	"fmt"
)

// FileMessageRefs is one live file's backing Telegram message IDs: its own
// root message, any separate hidden content message, and, for a multipart
// upload, every part message. Used by external-delete reconciliation to check
// whether a file's content is still present on Telegram.
type FileMessageRefs struct {
	FileMsgID     int64
	BackingMsgIDs []int64
}

// LiveFileMessageIDs returns every live (non-tombstoned) file in channelID
// together with all Telegram message IDs that back its content.
func LiveFileMessageIDs(db *sql.DB, channelID int64) ([]FileMessageRefs, error) {
	rows, err := db.Query(`
		SELECT msg_id, content_msg_id FROM files
		WHERE channel_id = ? AND tombstoned = 0
	`, channelID)
	if err != nil {
		return nil, fmt.Errorf("projection: list live file msg ids: %w", err)
	}
	defer rows.Close()

	type liveFile struct {
		msgID        int64
		contentMsgID int64
	}
	var files []liveFile
	for rows.Next() {
		var file liveFile
		if err := rows.Scan(&file.msgID, &file.contentMsgID); err != nil {
			return nil, fmt.Errorf("projection: scan live file msg id: %w", err)
		}
		files = append(files, file)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("projection: list live file msg ids: %w", err)
	}

	out := make([]FileMessageRefs, 0, len(files))
	for _, file := range files {
		backing := []int64{file.msgID}
		if file.contentMsgID != 0 && file.contentMsgID != file.msgID {
			backing = append(backing, file.contentMsgID)
		}
		parts, err := MultipartParts(db, channelID, file.msgID)
		if err != nil {
			return nil, fmt.Errorf("projection: load multipart parts for msg=%d: %w", file.msgID, err)
		}
		for _, p := range parts {
			backing = append(backing, p.MsgID)
		}
		out = append(out, FileMessageRefs{FileMsgID: file.msgID, BackingMsgIDs: backing})
	}
	return out, nil
}
