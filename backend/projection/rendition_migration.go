package projection

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// restoreRenditionExtension recovers additive metadata preserved by old readers
// in raw_header but absent from their serialized Op. Never reinterpret mutable
// network captions here: only the original locally canonical header is eligible.
func restoreRenditionExtension(op Op, header string) Op {
	if op.Type != OpFilePart || op.Rendition != nil || !strings.Contains(header, "|rend=") {
		return op
	}
	parsed, err := Parse(header)
	if err != nil || parsed.Type != op.Type || parsed.UploadUUID != op.UploadUUID || parsed.PartIndex != op.PartIndex || parsed.FileSize != op.FileSize {
		return op
	}
	op.Rendition = parsed.Rendition
	return op
}

// migrateRenditionReferences pages by replay_log's composite key. An old client
// can have synchronized hundreds of thousands of hidden parts; migration never
// retains their headers together in memory.
func migrateRenditionReferences(tx *sql.Tx) error {
	var channel, msg int64
	for {
		rows, err := tx.Query(`SELECT channel_id,msg_id,op_payload_json,raw_header,actor_user_id FROM replay_log
   WHERE op_type='part' AND (channel_id,msg_id)>(?,?) ORDER BY channel_id,msg_id LIMIT 128`, channel, msg)
		if err != nil {
			return err
		}
		type row struct {
			channel, id, actor int64
			payload, header    string
		}
		batch := make([]row, 0, 128)
		for rows.Next() {
			var item row
			if err := rows.Scan(&item.channel, &item.id, &item.payload, &item.header, &item.actor); err != nil {
				rows.Close()
				return err
			}
			batch = append(batch, item)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		if len(batch) == 0 {
			return nil
		}
		for _, item := range batch {
			channel, msg = item.channel, item.id
			var op Op
			if err := json.Unmarshal([]byte(item.payload), &op); err != nil {
				return fmt.Errorf("projection: restore rendition metadata: %w", err)
			}
			recovered := restoreRenditionExtension(op, item.header)
			if recovered.Rendition == nil {
				continue
			}
			if err := applyRendition(tx, channel, msg, recovered, item.actor); err != nil {
				if errors.Is(err, ErrBadOp) {
					continue
				}
				return err
			}
			encoded, err := json.Marshal(recovered)
			if err != nil {
				return err
			}
			if _, err := tx.Exec(`UPDATE replay_log SET op_payload_json=? WHERE channel_id=? AND msg_id=?`, encoded, channel, msg); err != nil {
				return err
			}
		}
	}
}
