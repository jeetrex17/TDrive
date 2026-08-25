package projection

import (
	"database/sql"
	"encoding/json"
	"fmt"
)

type migrationReplayRow struct {
	channelID   int64
	msgID       int64
	opType      string
	payloadJSON string
	actorUserID int64
}

type replayedObjectRevision struct {
	channelID int64
	objectID  string
	revision  int64
	kind      string
}

// backfillLegacyRevisionsFromReplay derives the v8 CAS baseline with the same
// projector used by RebuildProjection. A savepoint makes the replay a shadow
// operation: the user's pre-v8 projection is restored byte-for-byte, then only
// the reconstructed revision counters are copied onto matching objects.
//
// Counting replay rows is not sufficient because legacy rename/move/delete
// operations targeting missing or already-tombstoned objects are successful
// no-ops. Shadow replay preserves those historical semantics without weakening
// CAS or inventing a second projector just for migration.
func backfillLegacyRevisionsFromReplay(tx *sql.Tx) error {
	queue, err := migrationReplayQueue(tx)
	if err != nil {
		return err
	}
	if len(queue) == 0 {
		return nil
	}

	const savepoint = "tdrive_v8_revision_replay"
	if _, err := tx.Exec(`SAVEPOINT ` + savepoint); err != nil {
		return fmt.Errorf("projection: start legacy revision replay: %w", err)
	}
	for _, table := range []string{
		"files", "folders", "encryption", "file_parts", "dirents",
		"file_revisions", "projection_operations", "trash_entries",
	} {
		if _, err := tx.Exec(`DELETE FROM ` + table); err != nil {
			return fmt.Errorf("projection: clear %s for legacy revision replay: %w", table, err)
		}
	}

	for _, row := range queue {
		var op Op
		if err := json.Unmarshal([]byte(row.payloadJSON), &op); err != nil {
			return fmt.Errorf("projection: parse legacy revision op msg=%d: %w", row.msgID, err)
		}
		if op.Type == "" {
			op.Type = OpType(row.opType)
		}
		if err := ApplyOp(tx, row.channelID, row.msgID, op, row.actorUserID); err != nil {
			if !isSkippableApplyError(err) {
				return fmt.Errorf("projection: replay legacy revision op msg=%d: %w", row.msgID, err)
			}
			if recErr := recordProjectionOperationTx(
				tx, row.channelID, row.msgID, op, OperationRejected, err,
			); recErr != nil {
				return fmt.Errorf("projection: record legacy revision rejection: %w", recErr)
			}
		}
	}

	revisions, err := collectReplayedObjectRevisions(tx)
	if err != nil {
		return err
	}
	if _, err := tx.Exec(`ROLLBACK TO ` + savepoint); err != nil {
		return fmt.Errorf("projection: restore projection after legacy revision replay: %w", err)
	}
	if _, err := tx.Exec(`RELEASE ` + savepoint); err != nil {
		return fmt.Errorf("projection: release legacy revision replay: %w", err)
	}

	for _, object := range revisions {
		var (
			query string
			id    any = object.objectID
		)
		switch object.kind {
		case ObjectKindFile:
			query = `UPDATE files SET revision=? WHERE channel_id=? AND msg_id=?`
			fileMsgID, err := parseFileMsgID(object.objectID)
			if err != nil {
				return fmt.Errorf("projection: restore replayed file revision: %w", err)
			}
			id = fileMsgID
		case ObjectKindFolder:
			query = `UPDATE folders SET revision=? WHERE channel_id=? AND id=?`
		default:
			return fmt.Errorf("projection: unknown replayed object kind %q", object.kind)
		}
		if _, err := tx.Exec(query, object.revision, object.channelID, id); err != nil {
			return fmt.Errorf("projection: backfill %s revision: %w", object.kind, err)
		}
	}
	return nil
}

func migrationReplayQueue(tx *sql.Tx) ([]migrationReplayRow, error) {
	rows, err := tx.Query(`
		SELECT channel_id, msg_id, op_type, op_payload_json, actor_user_id
		FROM replay_log
		ORDER BY channel_id, msg_id
	`)
	if err != nil {
		return nil, fmt.Errorf("projection: read legacy revision replay: %w", err)
	}
	defer rows.Close()

	var queue []migrationReplayRow
	for rows.Next() {
		var row migrationReplayRow
		if err := rows.Scan(
			&row.channelID, &row.msgID, &row.opType, &row.payloadJSON, &row.actorUserID,
		); err != nil {
			return nil, fmt.Errorf("projection: scan legacy revision replay: %w", err)
		}
		queue = append(queue, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("projection: iterate legacy revision replay: %w", err)
	}
	return queue, nil
}

func collectReplayedObjectRevisions(tx *sql.Tx) ([]replayedObjectRevision, error) {
	queries := []struct {
		kind  string
		query string
	}{
		{ObjectKindFolder, `SELECT channel_id, id, revision FROM folders ORDER BY channel_id, id`},
		{ObjectKindFile, `SELECT channel_id, 'f:' || msg_id, revision FROM files ORDER BY channel_id, msg_id`},
	}
	var revisions []replayedObjectRevision
	for _, source := range queries {
		rows, err := tx.Query(source.query)
		if err != nil {
			return nil, fmt.Errorf("projection: read replayed %s revisions: %w", source.kind, err)
		}
		for rows.Next() {
			var object replayedObjectRevision
			object.kind = source.kind
			if err := rows.Scan(&object.channelID, &object.objectID, &object.revision); err != nil {
				_ = rows.Close()
				return nil, fmt.Errorf("projection: scan replayed %s revision: %w", source.kind, err)
			}
			revisions = append(revisions, object)
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("projection: iterate replayed %s revisions: %w", source.kind, err)
		}
		if err := rows.Close(); err != nil {
			return nil, fmt.Errorf("projection: close replayed %s revisions: %w", source.kind, err)
		}
	}
	return revisions, nil
}
