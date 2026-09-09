package projection

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"strings"
)

const maxHardDeletePlanPageSize = 1000

var ErrHardDeletePlanNotFound = errors.New("projection: hard-delete plan not found")
var ErrHardDeletePlanMismatch = errors.New("projection: hard-delete plan does not contain every message")

type hardDeleteScope struct {
	channelID  int64
	objectID   string
	objectKind string
	fileMsgID  int64
	revision   int64
}

// applyHardDeleteTree validates the complete Telegram body set and hides the
// namespace subtree in one SQLite transaction. Locally originated mutations
// also capture an immutable cleanup plan; remote replay only compacts local
// pointers. The hard-delete control message stays in replay_log.
func applyHardDeleteTree(tx *sql.Tx, channelID, markerMsgID int64, op Op) error {
	if markerMsgID <= 0 || op.ExpectedRevision <= 0 {
		return fmt.Errorf("%w: hard delete requires marker message and expected revision", ErrBadOp)
	}
	if err := validateHardDeleteChannel(tx, channelID); err != nil {
		return err
	}
	scope, err := loadHardDeleteScope(tx, channelID, op)
	if err != nil {
		return err
	}

	_, exists, err := hardDeleteJobStateTx(tx, channelID, op.OpID, op.Obj)
	if err != nil {
		return err
	}
	localIntent, err := hardDeleteIntentMatchesTx(tx, channelID, op)
	if err != nil {
		return err
	}
	if !exists && localIntent {
		// Only the originating installation will physically delete Telegram
		// bodies, so only it needs a complete and exclusively owned allowlist.
		// A secondary installation may legitimately have an incomplete local
		// multipart index; it must still apply the namespace tombstone.
		if err := validateHardDeleteContent(tx, scope); err != nil {
			return err
		}
		if err := createHardDeletePlanTx(tx, scope, markerMsgID, op.OpID); err != nil {
			return err
		}
	}
	// The normalized plan is self-contained once captured. Remove multipart
	// pointers on every client so remote replay cannot retain stale local body
	// state. An origin with pending cleanup still reads IDs from its plan.
	if err := removeHardDeletedPartPointersTx(tx, scope); err != nil {
		return err
	}
	if err := clearHardDeleteRetentionTx(tx, scope); err != nil {
		return err
	}
	if err := removeHardDeleteTrashEntriesTx(tx, scope); err != nil {
		return err
	}
	if scope.objectKind == ObjectKindFile {
		return trashFileTreeRoot(tx, channelID, scope.objectID, scope.revision)
	}
	return trashFolderTree(tx, channelID, scope.objectID)
}

func validateHardDeleteChannel(tx *sql.Tx, channelID int64) error {
	var kind string
	if err := tx.QueryRow(`SELECT kind FROM channels WHERE channel_id=?`, channelID).Scan(&kind); err != nil {
		return fmt.Errorf("projection: read hard-delete channel: %w", err)
	}
	if kind != KindPersonal {
		return fmt.Errorf("%w: hard delete is restricted to personal drives", ErrBadOp)
	}
	return nil
}

func loadHardDeleteScope(tx *sql.Tx, channelID int64, op Op) (hardDeleteScope, error) {
	kind, err := objectKind(op.Obj)
	if err != nil {
		return hardDeleteScope{}, err
	}
	var revision int64
	err = tx.QueryRow(`
		SELECT revision FROM dirents
		WHERE channel_id=? AND object_id=? AND tombstoned=0
	`, channelID, op.Obj).Scan(&revision)
	if errors.Is(err, sql.ErrNoRows) {
		return hardDeleteScope{}, ErrObjectNotFound
	}
	if err != nil {
		return hardDeleteScope{}, fmt.Errorf("projection: read hard-delete root: %w", err)
	}
	if revision != op.ExpectedRevision {
		return hardDeleteScope{}, ErrRevisionConflict
	}

	scope := hardDeleteScope{
		channelID:  channelID,
		objectID:   op.Obj,
		objectKind: kind,
		revision:   revision,
	}
	if kind == ObjectKindFile {
		scope.fileMsgID, err = parseFileMsgID(op.Obj)
		if err != nil {
			return hardDeleteScope{}, err
		}
	}
	return scope, nil
}

// withAffectedFiles returns a statement prefix defining affected_files and
// the arguments consumed by that prefix. Folder traversal and file selection
// stay set-based, so deleting a large tree does not issue one query per file.
func (s hardDeleteScope) withAffectedFiles(body string) (string, []any) {
	if s.objectKind == ObjectKindFile {
		return `WITH affected_files(file_msg_id) AS (SELECT ?)
		` + body, []any{s.fileMsgID}
	}
	return `WITH RECURSIVE subtree(id) AS (
			SELECT id FROM folders
			WHERE channel_id=? AND id=?
			UNION
			SELECT child.id FROM folders child
			JOIN subtree parent ON child.parent_id=parent.id
			WHERE child.channel_id=?
		),
		affected_files(file_msg_id) AS (
			SELECT msg_id FROM files
			WHERE channel_id=?
			  AND parent_id IN (SELECT id FROM subtree)
		)
		` + body, []any{s.channelID, s.objectID, s.channelID, s.channelID}
}

func validateHardDeleteContent(tx *sql.Tx, scope hardDeleteScope) error {
	query, args := scope.withAffectedFiles(`
		, revision_stats AS (
			SELECT affected_files.file_msg_id,
			       COUNT(file_revisions.revision) AS revision_count,
			       COALESCE(SUM(CASE
					WHEN file_revisions.revision IS NULL THEN 0
					WHEN file_revisions.content_msg_id > 0
					 AND file_revisions.upload_uuid = ''
					 AND file_revisions.part_count = 0 THEN 0
					WHEN file_revisions.content_msg_id = 0
					 AND file_revisions.upload_uuid != ''
					 AND file_revisions.part_count > 0 THEN 0
					ELSE 1
				END), 0) AS invalid_references
			FROM affected_files
			LEFT JOIN file_revisions
			  ON file_revisions.channel_id=?
			 AND file_revisions.file_msg_id=affected_files.file_msg_id
			GROUP BY affected_files.file_msg_id
		)
		SELECT COUNT(*) FROM revision_stats
		WHERE revision_count=0 OR invalid_references>0
	`)
	args = append(args, scope.channelID)
	var invalid int
	if err := tx.QueryRow(query, args...).Scan(&invalid); err != nil {
		return fmt.Errorf("projection: validate hard-delete content references: %w", err)
	}
	if invalid != 0 {
		return ErrContentIncomplete
	}

	query, args = scope.withAffectedFiles(`
		, multipart_stats AS (
			SELECT revisions.file_msg_id, revisions.revision,
			       revisions.part_count, revisions.size,
			       COUNT(parts.msg_id) AS stored_count,
			       COALESCE(MIN(parts.part_index), -1) AS first_index,
			       COALESCE(MAX(parts.part_index), -1) AS last_index,
			       COALESCE(SUM(parts.size), 0) AS stored_size
			FROM file_revisions revisions
			JOIN affected_files
			  ON affected_files.file_msg_id=revisions.file_msg_id
			LEFT JOIN file_parts parts
			  ON parts.channel_id=revisions.channel_id
			 AND parts.upload_uuid=revisions.upload_uuid
			WHERE revisions.channel_id=? AND revisions.upload_uuid!=''
			GROUP BY revisions.file_msg_id, revisions.revision,
			         revisions.part_count, revisions.size
		)
		SELECT COUNT(*) FROM multipart_stats
		WHERE stored_count!=part_count
		   OR first_index!=0
		   OR last_index!=part_count-1
		   OR (size>0 AND stored_size!=size)
	`)
	args = append(args, scope.channelID)
	if err := tx.QueryRow(query, args...).Scan(&invalid); err != nil {
		return fmt.Errorf("projection: validate hard-delete multipart content: %w", err)
	}
	if invalid != 0 {
		return ErrContentIncomplete
	}
	if err := validateHardDeleteBodyTargets(tx, scope); err != nil {
		return err
	}
	return validateHardDeleteBodyOwnership(tx, scope)
}

func validateHardDeleteBodyTargets(tx *sql.Tx, scope hardDeleteScope) error {
	query, args := scope.withAffectedFiles(`
		, body_messages(msg_id) AS (
			SELECT revisions.content_msg_id
			FROM file_revisions revisions
			JOIN affected_files
			  ON affected_files.file_msg_id=revisions.file_msg_id
			WHERE revisions.channel_id=? AND revisions.content_msg_id>0
			UNION
			SELECT parts.msg_id
			FROM file_revisions revisions
			JOIN affected_files
			  ON affected_files.file_msg_id=revisions.file_msg_id
			JOIN file_parts parts
			  ON parts.channel_id=revisions.channel_id
			 AND parts.upload_uuid=revisions.upload_uuid
			WHERE revisions.channel_id=? AND revisions.upload_uuid!=''
		)
		SELECT COUNT(*)
		FROM body_messages bodies
		LEFT JOIN replay_log replay
		  ON replay.channel_id=? AND replay.msg_id=bodies.msg_id
		WHERE bodies.msg_id>? OR (
			replay.msg_id IS NOT NULL AND replay.op_type NOT IN (?, ?)
		)
	`)
	args = append(args, scope.channelID, scope.channelID, scope.channelID, int64(math.MaxInt32), string(OpFileUpload), string(OpFilePart))
	var invalid int
	if err := tx.QueryRow(query, args...).Scan(&invalid); err != nil {
		return fmt.Errorf("projection: validate hard-delete body targets: %w", err)
	}
	if invalid != 0 {
		return fmt.Errorf("%w: hard-delete plan contains a non-body or out-of-range message", ErrBadOp)
	}
	return nil
}

func validateHardDeleteBodyOwnership(tx *sql.Tx, scope hardDeleteScope) error {
	query, args := scope.withAffectedFiles(`
		, affected_body_messages(msg_id) AS (
			SELECT revisions.content_msg_id
			FROM file_revisions revisions
			JOIN affected_files
			  ON affected_files.file_msg_id=revisions.file_msg_id
			WHERE revisions.channel_id=? AND revisions.content_msg_id>0
			UNION
			SELECT parts.msg_id
			FROM file_revisions revisions
			JOIN affected_files
			  ON affected_files.file_msg_id=revisions.file_msg_id
			JOIN file_parts parts
			  ON parts.channel_id=revisions.channel_id
			 AND parts.upload_uuid=revisions.upload_uuid
			WHERE revisions.channel_id=? AND revisions.upload_uuid!=''
		),
		outside_body_messages(msg_id) AS (
			SELECT revisions.content_msg_id
			FROM file_revisions revisions
			WHERE revisions.channel_id=?
			  AND revisions.content_msg_id>0
			  AND revisions.file_msg_id NOT IN (
				SELECT file_msg_id FROM affected_files
			  )
			UNION
			SELECT parts.msg_id
			FROM file_revisions revisions
			JOIN file_parts parts
			  ON parts.channel_id=revisions.channel_id
			 AND parts.upload_uuid=revisions.upload_uuid
			WHERE revisions.channel_id=?
			  AND revisions.upload_uuid!=''
			  AND revisions.file_msg_id NOT IN (
				SELECT file_msg_id FROM affected_files
			  )
		)
		SELECT 1
		FROM affected_body_messages affected
		JOIN outside_body_messages outside ON outside.msg_id=affected.msg_id
		LIMIT 1
	`)
	args = append(args, scope.channelID, scope.channelID, scope.channelID, scope.channelID)
	var shared int
	err := tx.QueryRow(query, args...).Scan(&shared)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("projection: validate hard-delete body ownership: %w", err)
	}
	return ErrContentAlreadyCommitted
}

func hardDeleteJobStateTx(tx *sql.Tx, channelID int64, opID, objectID string) (completed, exists bool, err error) {
	var (
		storedObjectID string
		completedFlag  int
	)
	err = tx.QueryRow(`
		SELECT root_object_id, completed FROM hard_delete_jobs
		WHERE channel_id=? AND op_id=?
	`, channelID, opID).Scan(&storedObjectID, &completedFlag)
	if errors.Is(err, sql.ErrNoRows) {
		return false, false, nil
	}
	if err != nil {
		return false, false, fmt.Errorf("projection: inspect hard-delete job: %w", err)
	}
	if storedObjectID != objectID {
		return false, false, fmt.Errorf("%w: hard-delete operation id targets another object", ErrBadOp)
	}
	return completedFlag != 0, true, nil
}

func createHardDeletePlanTx(tx *sql.Tx, scope hardDeleteScope, markerMsgID int64, opID string) error {
	if _, err := tx.Exec(`
		INSERT INTO hard_delete_jobs
		  (channel_id, op_id, root_object_id, marker_msg_id, total_messages, completed)
		VALUES (?, ?, ?, ?, 0, 0)
	`, scope.channelID, opID, scope.objectID, markerMsgID); err != nil {
		return fmt.Errorf("projection: create hard-delete job: %w", err)
	}

	query, args := scope.withAffectedFiles(`
		, body_messages(msg_id) AS (
			SELECT revisions.content_msg_id
			FROM file_revisions revisions
			JOIN affected_files
			  ON affected_files.file_msg_id=revisions.file_msg_id
			WHERE revisions.channel_id=? AND revisions.content_msg_id>0
			UNION
			SELECT parts.msg_id
			FROM file_revisions revisions
			JOIN affected_files
			  ON affected_files.file_msg_id=revisions.file_msg_id
			JOIN file_parts parts
			  ON parts.channel_id=revisions.channel_id
			 AND parts.upload_uuid=revisions.upload_uuid
			WHERE revisions.channel_id=? AND revisions.upload_uuid!=''
		)
		INSERT INTO hard_delete_plan_items (channel_id, op_id, msg_id)
		SELECT ?, ?, msg_id FROM body_messages
		WHERE msg_id>0 AND msg_id!=?
	`)
	args = append(args, scope.channelID, scope.channelID, scope.channelID, opID, markerMsgID)
	if _, err := tx.Exec(query, args...); err != nil {
		return fmt.Errorf("projection: capture hard-delete body plan: %w", err)
	}
	if _, err := tx.Exec(`
		UPDATE hard_delete_jobs
		SET total_messages=(
			SELECT COUNT(*) FROM hard_delete_plan_items
			WHERE channel_id=? AND op_id=?
		)
		WHERE channel_id=? AND op_id=?
	`, scope.channelID, opID, scope.channelID, opID); err != nil {
		return fmt.Errorf("projection: count hard-delete body plan: %w", err)
	}
	return nil
}

func clearHardDeleteRetentionTx(tx *sql.Tx, scope hardDeleteScope) error {
	query, args := scope.withAffectedFiles(`
		UPDATE file_revisions SET retained_until=0
		WHERE channel_id=?
		  AND file_msg_id IN (SELECT file_msg_id FROM affected_files)
	`)
	args = append(args, scope.channelID)
	if _, err := tx.Exec(query, args...); err != nil {
		return fmt.Errorf("projection: clear hard-delete retention: %w", err)
	}
	return nil
}

func removeHardDeletedPartPointersTx(tx *sql.Tx, scope hardDeleteScope) error {
	query, args := scope.withAffectedFiles(`
		DELETE FROM file_parts
		WHERE channel_id=? AND upload_uuid IN (
			SELECT revisions.upload_uuid
			FROM file_revisions revisions
			JOIN affected_files
			  ON affected_files.file_msg_id=revisions.file_msg_id
			WHERE revisions.channel_id=? AND revisions.upload_uuid!=''
		)
	`)
	args = append(args, scope.channelID, scope.channelID)
	if _, err := tx.Exec(query, args...); err != nil {
		return fmt.Errorf("projection: clear hard-delete part pointers: %w", err)
	}
	return nil
}

func removeHardDeleteTrashEntriesTx(tx *sql.Tx, scope hardDeleteScope) error {
	if scope.objectKind == ObjectKindFile {
		if _, err := tx.Exec(`
			DELETE FROM trash_entries WHERE channel_id=? AND object_id=?
		`, scope.channelID, scope.objectID); err != nil {
			return fmt.Errorf("projection: clear hard-delete trash entry: %w", err)
		}
		return nil
	}
	if _, err := tx.Exec(`
		WITH RECURSIVE subtree(id) AS (
			SELECT id FROM folders
			WHERE channel_id=? AND id=?
			UNION
			SELECT child.id FROM folders child
			JOIN subtree parent ON child.parent_id=parent.id
			WHERE child.channel_id=?
		), affected_objects(object_id) AS (
			SELECT id FROM subtree
			UNION
			SELECT 'f:' || CAST(msg_id AS TEXT) FROM files
			WHERE channel_id=?
			  AND parent_id IN (SELECT id FROM subtree)
		)
		DELETE FROM trash_entries
		WHERE channel_id=? AND object_id IN (SELECT object_id FROM affected_objects)
	`, scope.channelID, scope.objectID, scope.channelID, scope.channelID, scope.channelID); err != nil {
		return fmt.Errorf("projection: clear hard-delete trash entries: %w", err)
	}
	return nil
}

// ValidateHardDeletePlanItems rechecks a journal-supplied batch against the
// projection-owned immutable allowlist immediately before physical deletion.
func ValidateHardDeletePlanItems(
	ctx context.Context,
	db *sql.DB,
	channelID int64,
	opID string,
	messageIDs []int64,
) error {
	if err := validateHardDeletePlanRequest(ctx, db, channelID, opID); err != nil {
		return err
	}
	if len(messageIDs) == 0 || len(messageIDs) > maxHardDeletePlanPageSize {
		return ErrHardDeletePlanMismatch
	}
	seen := make(map[int64]struct{}, len(messageIDs))
	for _, messageID := range messageIDs {
		if messageID <= 0 || messageID > math.MaxInt32 {
			return ErrHardDeletePlanMismatch
		}
		if _, exists := seen[messageID]; exists {
			return ErrHardDeletePlanMismatch
		}
		seen[messageID] = struct{}{}
	}

	var completed int
	if err := db.QueryRowContext(ctx, `
		SELECT completed FROM hard_delete_jobs
		WHERE channel_id=? AND op_id=?
	`, channelID, opID).Scan(&completed); errors.Is(err, sql.ErrNoRows) {
		return ErrHardDeletePlanNotFound
	} else if err != nil {
		return fmt.Errorf("projection: inspect hard-delete allowlist: %w", err)
	}
	if completed != 0 {
		return ErrHardDeletePlanMismatch
	}
	for _, messageID := range messageIDs {
		var present int
		err := db.QueryRowContext(ctx, `
			SELECT 1 FROM hard_delete_plan_items
			WHERE channel_id=? AND op_id=? AND msg_id=?
		`, channelID, opID, messageID).Scan(&present)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrHardDeletePlanMismatch
		}
		if err != nil {
			return fmt.Errorf("projection: validate hard-delete allowlist: %w", err)
		}
	}
	return nil
}

// HardDeletePlanPage returns a stable ascending page of Telegram body message
// IDs. total is the original deduplicated plan size; done reports that the
// returned page reached the immutable end of the plan.
func HardDeletePlanPage(ctx context.Context, db *sql.DB, channelID int64, opID string, afterMsgID int64, limit int) (ids []int64, total int64, done bool, err error) {
	if err := validateHardDeletePlanRequest(ctx, db, channelID, opID); err != nil {
		return nil, 0, false, err
	}
	if afterMsgID < 0 {
		return nil, 0, false, fmt.Errorf("projection: hard-delete plan cursor cannot be negative")
	}
	if limit <= 0 || limit > maxHardDeletePlanPageSize {
		return nil, 0, false, fmt.Errorf("projection: hard-delete plan limit must be between 1 and %d", maxHardDeletePlanPageSize)
	}
	err = db.QueryRowContext(ctx, `
		SELECT total_messages FROM hard_delete_jobs
		WHERE channel_id=? AND op_id=?
	`, channelID, opID).Scan(&total)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, 0, false, ErrHardDeletePlanNotFound
	}
	if err != nil {
		return nil, 0, false, fmt.Errorf("projection: read hard-delete plan: %w", err)
	}
	rows, err := db.QueryContext(ctx, `
		SELECT msg_id FROM hard_delete_plan_items
		WHERE channel_id=? AND op_id=? AND msg_id>?
		ORDER BY msg_id ASC
		LIMIT ?
	`, channelID, opID, afterMsgID, limit+1)
	if err != nil {
		return nil, 0, false, fmt.Errorf("projection: page hard-delete plan: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, 0, false, fmt.Errorf("projection: scan hard-delete plan: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, false, fmt.Errorf("projection: iterate hard-delete plan: %w", err)
	}
	if len(ids) <= limit {
		return ids, total, true, nil
	}
	return ids[:limit], total, false, nil
}

// CompleteHardDeletePlan atomically removes any remaining multipart pointers,
// compacts the plan and local intent, and retains a small completion receipt.
func CompleteHardDeletePlan(ctx context.Context, db *sql.DB, channelID int64, opID string) (err error) {
	if err := validateHardDeletePlanRequest(ctx, db, channelID, opID); err != nil {
		return err
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("projection: begin hard-delete completion: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var completed int
	err = tx.QueryRowContext(ctx, `
		SELECT completed FROM hard_delete_jobs
		WHERE channel_id=? AND op_id=?
	`, channelID, opID).Scan(&completed)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrHardDeletePlanNotFound
	}
	if err != nil {
		return fmt.Errorf("projection: inspect hard-delete completion: %w", err)
	}
	if completed == 0 {
		if _, err := tx.ExecContext(ctx, `
			DELETE FROM file_parts
			WHERE channel_id=? AND msg_id IN (
				SELECT msg_id FROM hard_delete_plan_items
				WHERE channel_id=? AND op_id=?
			)
		`, channelID, channelID, opID); err != nil {
			return fmt.Errorf("projection: clear hard-delete part pointers: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `
			DELETE FROM hard_delete_plan_items WHERE channel_id=? AND op_id=?
		`, channelID, opID); err != nil {
			return fmt.Errorf("projection: compact hard-delete plan: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE hard_delete_jobs SET completed=1
			WHERE channel_id=? AND op_id=? AND completed=0
		`, channelID, opID); err != nil {
			return fmt.Errorf("projection: complete hard-delete plan: %w", err)
		}
	}
	if _, err := tx.ExecContext(ctx, `
		DELETE FROM hard_delete_intents WHERE channel_id=? AND op_id=?
	`, channelID, opID); err != nil {
		return fmt.Errorf("projection: compact hard-delete intent: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("projection: commit hard-delete completion: %w", err)
	}
	return nil
}

func validateHardDeletePlanRequest(ctx context.Context, db *sql.DB, channelID int64, opID string) error {
	if err := validateContext(ctx, "access hard-delete plan"); err != nil {
		return err
	}
	if db == nil {
		return fmt.Errorf("projection: hard-delete plan db is nil")
	}
	if channelID <= 0 {
		return fmt.Errorf("projection: hard-delete plan channel id must be positive")
	}
	if strings.TrimSpace(opID) == "" {
		return fmt.Errorf("projection: hard-delete plan operation id is empty")
	}
	return nil
}
