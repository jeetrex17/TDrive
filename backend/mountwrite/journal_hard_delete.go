package mountwrite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

const maxHardDeleteJournalBatch = 1000

func (j *SQLiteJournal) AppendHardDeletePlan(
	ctx context.Context,
	operationID string,
	messageIDs []int64,
	total int64,
	done bool,
) error {
	if j == nil || j.db == nil || ctx == nil || operationID == "" || !validOperationID(operationID) ||
		total < 0 || len(messageIDs) > maxHardDeleteJournalBatch || !validSortedMessageIDs(messageIDs) {
		return ErrInvalidRequest
	}
	tx, err := j.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin hard-delete plan append: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var kind MutationKind
	var state JournalState
	if err := tx.QueryRowContext(ctx, `
		SELECT kind, state FROM mount_write_journal WHERE operation_id = ?`, operationID,
	).Scan(&kind, &state); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return fmt.Errorf("load hard-delete journal operation: %w", err)
	}
	if kind != MutationHardDelete || state != StateDeletePlanPending {
		return ErrInvalidTransition
	}

	expected, sealed, found, err := hardDeletePlanHeader(ctx, tx, operationID)
	if err != nil {
		return err
	}
	if found && expected != total {
		return ErrConflict
	}
	if !found {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO mount_write_hard_delete_plans (operation_id, expected_count, sealed)
			VALUES (?, ?, 0)`, operationID, total); err != nil {
			return fmt.Errorf("create hard-delete plan: %w", err)
		}
	}
	if sealed {
		matches, err := countPlannedMessageIDs(ctx, tx, operationID, messageIDs)
		if err != nil {
			return err
		}
		if matches == int64(len(messageIDs)) && done {
			return tx.Commit()
		}
		return ErrConflict
	}

	for _, messageID := range messageIDs {
		if _, err := tx.ExecContext(ctx, `
			INSERT OR IGNORE INTO mount_write_hard_delete_messages (operation_id, message_id, completed)
			VALUES (?, ?, 0)`, operationID, messageID); err != nil {
			return fmt.Errorf("append hard-delete message: %w", err)
		}
	}
	var planned int64
	if err := tx.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM mount_write_hard_delete_messages WHERE operation_id = ?`, operationID,
	).Scan(&planned); err != nil {
		return fmt.Errorf("count hard-delete plan: %w", err)
	}
	if planned > total || (done && planned != total) {
		return ErrConflict
	}
	if done {
		if _, err := tx.ExecContext(ctx, `
			UPDATE mount_write_hard_delete_plans SET sealed = 1 WHERE operation_id = ?`, operationID); err != nil {
			return fmt.Errorf("seal hard-delete plan: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit hard-delete plan append: %w", err)
	}
	return nil
}

func (j *SQLiteJournal) NextHardDeleteBatch(ctx context.Context, operationID string, limit int) ([]int64, error) {
	if j == nil || j.db == nil || ctx == nil || operationID == "" || !validOperationID(operationID) ||
		limit <= 0 || limit > maxHardDeleteJournalBatch {
		return nil, ErrInvalidRequest
	}
	rows, err := j.db.QueryContext(ctx, `
		SELECT message_id
		FROM mount_write_hard_delete_messages
		WHERE operation_id = ? AND completed = 0
		ORDER BY message_id
		LIMIT ?`, operationID, limit)
	if err != nil {
		return nil, fmt.Errorf("list pending hard-delete messages: %w", err)
	}
	defer rows.Close()
	messageIDs := make([]int64, 0, limit)
	for rows.Next() {
		var messageID int64
		if err := rows.Scan(&messageID); err != nil {
			return nil, fmt.Errorf("scan pending hard-delete message: %w", err)
		}
		messageIDs = append(messageIDs, messageID)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate pending hard-delete messages: %w", err)
	}
	return messageIDs, nil
}

func (j *SQLiteJournal) MarkHardDeleteBatchDone(ctx context.Context, operationID string, messageIDs []int64) error {
	if j == nil || j.db == nil || ctx == nil || operationID == "" || !validOperationID(operationID) ||
		len(messageIDs) == 0 || len(messageIDs) > maxHardDeleteJournalBatch || !validSortedMessageIDs(messageIDs) {
		return ErrInvalidRequest
	}
	tx, err := j.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin hard-delete checkpoint: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	_, sealed, found, err := hardDeletePlanHeader(ctx, tx, operationID)
	if err != nil {
		return err
	}
	if !found {
		return ErrNotFound
	}
	if !sealed {
		return ErrInvalidTransition
	}
	matches, err := countPlannedMessageIDs(ctx, tx, operationID, messageIDs)
	if err != nil {
		return err
	}
	if matches != int64(len(messageIDs)) {
		return ErrConflict
	}
	for _, messageID := range messageIDs {
		if _, err := tx.ExecContext(ctx, `
			UPDATE mount_write_hard_delete_messages
			SET completed = 1
			WHERE operation_id = ? AND message_id = ?`, operationID, messageID); err != nil {
			return fmt.Errorf("checkpoint hard-delete message: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit hard-delete checkpoint: %w", err)
	}
	return nil
}

func (j *SQLiteJournal) HardDeletePlanStatus(
	ctx context.Context,
	operationID string,
) (HardDeletePlanStatus, bool, error) {
	if j == nil || j.db == nil || ctx == nil || operationID == "" || !validOperationID(operationID) {
		return HardDeletePlanStatus{}, false, ErrInvalidRequest
	}
	return hardDeletePlanStatusTx(ctx, j.db, operationID)
}

// CompactHardDeletePlan removes per-message progress after remote finalization.
// StateDeleteFinalizing is itself durable, so a crash after compaction safely
// repeats this idempotent step without retaining one local row per deleted
// Telegram body forever.
func (j *SQLiteJournal) CompactHardDeletePlan(ctx context.Context, operationID string) error {
	if j == nil || j.db == nil || ctx == nil || operationID == "" || !validOperationID(operationID) {
		return ErrInvalidRequest
	}
	tx, err := j.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin hard-delete plan compaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var kind MutationKind
	var state JournalState
	if err := tx.QueryRowContext(ctx, `
		SELECT kind, state FROM mount_write_journal WHERE operation_id = ?`, operationID,
	).Scan(&kind, &state); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return fmt.Errorf("load hard-delete operation for compaction: %w", err)
	}
	if kind != MutationHardDelete || state != StateDeleteFinalizing {
		return ErrInvalidTransition
	}

	status, found, err := hardDeletePlanStatusTx(ctx, tx, operationID)
	if err != nil {
		return err
	}
	if found {
		if !status.Sealed || status.PlannedCount != status.ExpectedCount ||
			status.CompletedCount != status.ExpectedCount {
			return ErrConflict
		}
		if _, err := tx.ExecContext(ctx, `
			DELETE FROM mount_write_hard_delete_messages WHERE operation_id = ?`, operationID); err != nil {
			return fmt.Errorf("compact hard-delete messages: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `
			DELETE FROM mount_write_hard_delete_plans WHERE operation_id = ?`, operationID); err != nil {
			return fmt.Errorf("compact hard-delete plan: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit hard-delete plan compaction: %w", err)
	}
	return nil
}

func hardDeletePlanStatusTx(
	ctx context.Context,
	query queryRower,
	operationID string,
) (HardDeletePlanStatus, bool, error) {
	var status HardDeletePlanStatus
	var sealed int
	err := query.QueryRowContext(ctx, `
		SELECT p.expected_count,
		       COUNT(m.message_id),
		       COALESCE(SUM(CASE WHEN m.completed = 1 THEN 1 ELSE 0 END), 0),
		       COALESCE(MAX(m.message_id), 0),
		       p.sealed
		FROM mount_write_hard_delete_plans AS p
		LEFT JOIN mount_write_hard_delete_messages AS m ON m.operation_id = p.operation_id
		WHERE p.operation_id = ?
		GROUP BY p.operation_id, p.expected_count, p.sealed`, operationID,
	).Scan(&status.ExpectedCount, &status.PlannedCount, &status.CompletedCount, &status.Cursor, &sealed)
	if errors.Is(err, sql.ErrNoRows) {
		return HardDeletePlanStatus{}, false, nil
	}
	if err != nil {
		return HardDeletePlanStatus{}, false, fmt.Errorf("read hard-delete plan status: %w", err)
	}
	status.Sealed = sealed == 1
	return status, true, nil
}

type queryRower interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func hardDeletePlanHeader(ctx context.Context, query queryRower, operationID string) (int64, bool, bool, error) {
	var expected int64
	var sealed int
	err := query.QueryRowContext(ctx, `
		SELECT expected_count, sealed
		FROM mount_write_hard_delete_plans
		WHERE operation_id = ?`, operationID).Scan(&expected, &sealed)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, false, nil
	}
	if err != nil {
		return 0, false, false, fmt.Errorf("read hard-delete plan header: %w", err)
	}
	return expected, sealed == 1, true, nil
}

func countPlannedMessageIDs(ctx context.Context, tx *sql.Tx, operationID string, messageIDs []int64) (int64, error) {
	var matches int64
	for _, messageID := range messageIDs {
		var present int
		err := tx.QueryRowContext(ctx, `
			SELECT 1 FROM mount_write_hard_delete_messages
			WHERE operation_id = ? AND message_id = ?`, operationID, messageID).Scan(&present)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return 0, fmt.Errorf("validate hard-delete message: %w", err)
		}
		matches++
	}
	return matches, nil
}

func validSortedMessageIDs(messageIDs []int64) bool {
	for index, messageID := range messageIDs {
		if messageID <= 0 || (index > 0 && messageIDs[index-1] >= messageID) {
			return false
		}
	}
	return true
}

var _ HardDeleteJournal = (*SQLiteJournal)(nil)
