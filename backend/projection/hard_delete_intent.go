package projection

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

var ErrHardDeleteIntentApplied = errors.New("projection: hard-delete intent already has a projected marker")

// RegisterHardDeleteIntent records that this installation owns cleanup for a
// hard-delete marker it is about to send. Telegram replay is global, while the
// write journal is local; this explicit boundary prevents other installations
// from accumulating actionable deletion plans for the same marker.
func RegisterHardDeleteIntent(
	ctx context.Context,
	db *sql.DB,
	channelID int64,
	opID string,
	objectID string,
	expectedRevision int64,
) (err error) {
	if err := validateHardDeleteIntentRequest(ctx, db, channelID, opID, objectID, expectedRevision); err != nil {
		return err
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("projection: begin hard-delete intent: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if err := validateHardDeleteChannel(tx, channelID); err != nil {
		return err
	}
	// The existing-claim check comes before the target read on purpose: receipt
	// reconciliation re-registers after the marker has already hidden the
	// object, and an exact retry has to stay idempotent rather than fail on a
	// target that is no longer live.
	claimed, err := hardDeleteIntentClaimedTx(tx, channelID, opID, objectID, expectedRevision)
	if err != nil {
		return err
	}
	if !claimed {
		var currentRevision int64
		err = tx.QueryRowContext(ctx, `
			SELECT revision FROM dirents
			WHERE channel_id=? AND object_id=? AND tombstoned=0
		`, channelID, objectID).Scan(&currentRevision)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrObjectNotFound
		}
		if err != nil {
			return fmt.Errorf("projection: inspect hard-delete intent target: %w", err)
		}
		if currentRevision != expectedRevision {
			return ErrRevisionConflict
		}
		if err := recordHardDeleteIntentTx(tx, channelID, opID, objectID, expectedRevision); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("projection: commit hard-delete intent: %w", err)
	}
	return nil
}

// hardDeleteIntentClaimedTx reports whether this operation id is already this
// installation's, refusing an id that was claimed for a different object or a
// different revision.
func hardDeleteIntentClaimedTx(tx *sql.Tx, channelID int64, opID, objectID string, expectedRevision int64) (bool, error) {
	storedObjectID, storedRevision, exists, err := loadHardDeleteIntentTx(tx, channelID, opID)
	if err != nil || !exists {
		return false, err
	}
	if storedObjectID != objectID || storedRevision != expectedRevision {
		return false, fmt.Errorf("%w: hard-delete intent operation id targets another object", ErrBadOp)
	}
	return true, nil
}

// recordHardDeleteIntentTx takes exclusive local ownership of the physical
// cleanup behind one operation id. It refuses an id whose marker already
// produced a cleanup job, because that plan is immutable once captured.
func recordHardDeleteIntentTx(tx *sql.Tx, channelID int64, opID, objectID string, expectedRevision int64) error {
	if _, jobExists, err := hardDeleteJobStateTx(tx, channelID, opID, objectID); err != nil {
		return err
	} else if jobExists {
		return ErrHardDeleteIntentApplied
	}
	if _, err := tx.Exec(`
		INSERT INTO hard_delete_intents
		  (channel_id, op_id, root_object_id, expected_revision)
		VALUES (?, ?, ?, ?)
	`, channelID, opID, objectID, expectedRevision); err != nil {
		return fmt.Errorf("projection: register hard-delete intent: %w", err)
	}
	return nil
}

// AbandonHardDeleteIntent removes a registered intent only while no marker has
// projected a cleanup job. Callers use it after a definitive pre-send failure;
// uncertain sends retain the intent for receipt reconciliation and restart.
func AbandonHardDeleteIntent(ctx context.Context, db *sql.DB, channelID int64, opID string) (err error) {
	if err := validateHardDeletePlanRequest(ctx, db, channelID, opID); err != nil {
		return err
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("projection: begin abandon hard-delete intent: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var one int
	err = tx.QueryRowContext(ctx, `
		SELECT 1 FROM hard_delete_jobs WHERE channel_id=? AND op_id=?
	`, channelID, opID).Scan(&one)
	if err == nil {
		return ErrHardDeleteIntentApplied
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("projection: inspect hard-delete intent job: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		DELETE FROM hard_delete_intents WHERE channel_id=? AND op_id=?
	`, channelID, opID); err != nil {
		return fmt.Errorf("projection: abandon hard-delete intent: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("projection: commit abandoned hard-delete intent: %w", err)
	}
	return nil
}

func hardDeleteIntentMatchesTx(tx *sql.Tx, channelID int64, op Op) (bool, error) {
	objectID, expectedRevision, exists, err := loadHardDeleteIntentTx(tx, channelID, op.OpID)
	if err != nil || !exists {
		return false, err
	}
	if objectID != op.Obj || expectedRevision != op.ExpectedRevision {
		return false, fmt.Errorf("%w: hard-delete marker does not match local intent", ErrBadOp)
	}
	return true, nil
}

func loadHardDeleteIntentTx(tx *sql.Tx, channelID int64, opID string) (objectID string, expectedRevision int64, exists bool, err error) {
	err = tx.QueryRow(`
		SELECT root_object_id, expected_revision FROM hard_delete_intents
		WHERE channel_id=? AND op_id=?
	`, channelID, opID).Scan(&objectID, &expectedRevision)
	if errors.Is(err, sql.ErrNoRows) {
		return "", 0, false, nil
	}
	if err != nil {
		return "", 0, false, fmt.Errorf("projection: inspect hard-delete intent: %w", err)
	}
	return objectID, expectedRevision, true, nil
}

func validateHardDeleteIntentRequest(
	ctx context.Context,
	db *sql.DB,
	channelID int64,
	opID string,
	objectID string,
	expectedRevision int64,
) error {
	if err := validateHardDeletePlanRequest(ctx, db, channelID, opID); err != nil {
		return err
	}
	if _, err := objectKind(objectID); err != nil {
		return err
	}
	if expectedRevision <= 0 {
		return fmt.Errorf("%w: hard-delete intent requires expected revision", ErrBadOp)
	}
	return validateVersionedWritableOp(Op{ProtocolVersion: 1, OpID: opID})
}
