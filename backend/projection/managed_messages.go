package projection

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

const managedMessageBatchSize = 200

// ManagedMsgIDsIn reports which candidate Telegram messages are already owned
// by this channel's projection or durable cleanup state. Tombstones and old
// revisions remain owned: showing them as loose root files would resurrect
// deleted or overwritten content. Batches bound SQL parameters and result memory
// without loading every message ID in a large drive or issuing per-file queries.
func ManagedMsgIDsIn(ctx context.Context, db *sql.DB, channelID int64, candidates []int64) (map[int64]struct{}, error) {
	if err := validateContext(ctx, "read managed messages"); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if db == nil || channelID == 0 {
		return nil, fmt.Errorf("projection: database and channel required for managed messages")
	}
	owned := make(map[int64]struct{})
	for start := 0; start < len(candidates); start += managedMessageBatchSize {
		batch := candidates[start:min(start+managedMessageBatchSize, len(candidates))]
		if err := managedMessageBatch(ctx, db, channelID, batch, owned); err != nil {
			return nil, fmt.Errorf("projection: read managed messages: %w", err)
		}
	}
	return owned, nil
}

func managedMessageBatch(ctx context.Context, db *sql.DB, channelID int64, candidates []int64, owned map[int64]struct{}) error {
	args := make([]any, 1, len(candidates)+1)
	args[0] = channelID
	placeholders := make([]string, len(candidates))
	for i, id := range candidates {
		args = append(args, id)
		placeholders[i] = fmt.Sprintf("?%d", i+2)
	}
	ids := strings.Join(placeholders, ",")
	// Numbered placeholders reuse the same bounded candidate list for every
	// ownership source. UNION removes IDs shared by a file and its replay row.
	var query strings.Builder
	for i, source := range []struct{ table, column string }{
		{"files", "msg_id"}, {"files", "content_msg_id"},
		{"file_revisions", "content_msg_id"}, {"file_parts", "msg_id"},
		{"file_renditions", "msg_id"}, {"replay_log", "msg_id"},
		{"pending_part_cleanup", "msg_id"}, {"hard_delete_plan_items", "msg_id"},
	} {
		if i > 0 {
			query.WriteString(" UNION ")
		}
		fmt.Fprintf(&query, "SELECT %s FROM %s WHERE channel_id=?1 AND %s>0 AND %s IN (%s)", source.column, source.table, source.column, source.column, ids)
	}
	rows, err := db.QueryContext(ctx, query.String(), args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return err
		}
		owned[id] = struct{}{}
	}
	return rows.Err()
}
