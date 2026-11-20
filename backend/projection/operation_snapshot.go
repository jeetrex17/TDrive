package projection

import (
	"context"
	"database/sql"
	"fmt"
)

// OperationSnapshot reads an operation outcome and its replay boundary
// together. A zero Operation.MsgID means there is no local outcome.
type OperationSnapshot struct {
	Operation ProjectionOperation
	Watermark int64
	Ordered   bool
}

type operationSnapshotReader interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

// ReadOperationSnapshot can certify outcomes only after an initial scan with
// no repair pending and no locally projected operations beyond the watermark.
// An empty opID reads the boundary without looking up a specific operation.
func ReadOperationSnapshot(ctx context.Context, db operationSnapshotReader, channelID int64, opID string) (OperationSnapshot, error) {
	snapshot := OperationSnapshot{
		Operation: ProjectionOperation{ChannelID: channelID, OpID: opID},
	}
	err := db.QueryRowContext(ctx, `
		SELECT c.last_synced_msg, c.initial_sync_done = 1 AND c.needs_projection_rebuild = 0
			AND NOT EXISTS (
				SELECT 1 FROM replay_log r
				WHERE r.channel_id=c.channel_id AND r.msg_id>c.last_synced_msg
			),
			COALESCE(o.msg_id, 0), COALESCE(o.op_type, ''), COALESCE(o.outcome, ''), COALESCE(o.error, '')
		FROM channels c
		LEFT JOIN projection_operations o ON o.channel_id=c.channel_id AND o.op_id=?
		WHERE c.channel_id=?
	`, opID, channelID).Scan(&snapshot.Watermark, &snapshot.Ordered, &snapshot.Operation.MsgID,
		&snapshot.Operation.OpType, &snapshot.Operation.Outcome, &snapshot.Operation.Error)
	if err != nil {
		return OperationSnapshot{}, fmt.Errorf("projection: read operation snapshot: %w", err)
	}
	return snapshot, nil
}
