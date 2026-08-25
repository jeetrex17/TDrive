package mountwrite

import (
	"context"
	"errors"
	"log/slog"
)

type RecoveryReport struct {
	Examined  int
	Completed int
	Pending   int
	Aborted   int
	Failed    int
}

// Recover reconciles potentially-visible commits and cleans all pre-commit
// work. It never resumes a receiving, staged, uploading, or uploaded mutation.
func (c *Coordinator) Recover(ctx context.Context) (RecoveryReport, error) {
	if c == nil || ctx == nil {
		return RecoveryReport{}, ErrInvalidRequest
	}
	records, err := c.journal.ListRecoverable(ctx)
	if err != nil {
		slog.Error("mountwrite: recovery failed to list recoverable operations", "error", err)
		return RecoveryReport{}, newOperationError("recovery", MutationPut, err)
	}
	slog.Info("mountwrite: recovery starting", "examined", len(records))
	report := RecoveryReport{Examined: len(records)}
	failures := make([]error, 0)
	for _, listed := range records {
		state, err := c.recoverOne(ctx, listed.OperationID)
		if err != nil {
			// Hidden-body cleanup cannot change the visible namespace. When its
			// exact, durable receipt or local cleanup work is temporarily
			// unavailable, retain the stage and retry on a later recovery without
			// blocking the whole mount.
			// Validation/corruption errors and every commit-state failure remain
			// fatal so uncertain namespace changes still fail closed.
			if state == StateCleanupPending && errors.Is(err, ErrUnavailable) {
				slog.Warn("mountwrite: recovery cleanup temporarily unavailable, will retry later", "operation_id", listed.OperationID, "error", err)
				report.Pending++
				continue
			}
			slog.Error("mountwrite: recovery failed for operation", "operation_id", listed.OperationID, "state", state, "error", err)
			report.Failed++
			failures = append(failures, err)
			continue
		}
		switch state {
		case StateDone:
			report.Completed++
		case StateAborted:
			report.Aborted++
		default:
			report.Pending++
		}
	}
	slog.Info("mountwrite: recovery complete", "examined", report.Examined, "completed", report.Completed,
		"aborted", report.Aborted, "pending", report.Pending, "failed", report.Failed)
	return report, errors.Join(failures...)
}

func (c *Coordinator) recoverOne(ctx context.Context, operationID string) (JournalState, error) {
	finish, err := c.begin(ctx)
	if err != nil {
		return "", newOperationError(operationID, MutationPut, err)
	}
	defer finish()
	release, err := c.locks.Lock(ctx, "operation:"+operationID)
	if err != nil {
		return "", newOperationError(operationID, MutationPut, err)
	}
	defer release()
	record, found, err := c.journal.Get(ctx, operationID)
	if err != nil {
		return "", newOperationError(operationID, MutationPut, err)
	}
	if !found {
		return StateAborted, nil
	}
	if isTerminal(record.State) {
		return record.State, nil
	}
	if err := c.resumeRecovery(ctx, record); err != nil {
		return record.State, err
	}
	latest, found, err := c.journal.Get(ctx, operationID)
	if err != nil {
		return record.State, operationError(record, err)
	}
	if !found {
		return StateAborted, nil
	}
	return latest.State, nil
}

func (c *Coordinator) resumeRecovery(ctx context.Context, record JournalRecord) error {
	slog.Debug("mountwrite: resuming interrupted operation", "operation_id", record.OperationID, "state", record.State, "kind", record.Mutation.Kind)
	switch record.State {
	case StateReceiving, StateStaged, StateUploading, StateUploaded:
		c.markAborted(ctx, record, ErrCanceled)
		return nil
	case StateCommitting, StateReconciling:
		return c.recoverCommit(ctx, record)
	case StateRemoteCommitted, StateProjectionPending:
		_, err := c.finalizeCommitted(ctx, record)
		return err
	case StateCleanupPending:
		return c.recoverCleanup(ctx, record)
	default:
		return operationError(record, ErrInvalidTransition)
	}
}

func (c *Coordinator) recoverCommit(ctx context.Context, record JournalRecord) error {
	current := record
	if current.State == StateCommitting {
		_, err := c.commit(ctx, current)
		return err
	}
	result, found, err := c.reconcile(ctx, current)
	if err != nil {
		if isDefiniteCommitRejection(err) {
			c.markAborted(ctx, current, err)
			return nil
		}
		return operationError(current, err)
	}
	if !found {
		return nil
	}
	_, err = c.confirmCommitted(ctx, current, normalizeResult(current, result))
	return err
}

func (c *Coordinator) recoverCleanup(ctx context.Context, record JournalRecord) error {
	maintenanceCtx, cancel := c.maintenanceContext(ctx)
	defer cancel()
	// A receipt-only result means the visibility commit was rejected and its
	// hidden body still needs cleanup. Confirmed mutations always have a stable
	// logical object identity.
	if record.Result != nil && record.Result.ObjectID != "" {
		if err := c.removeStagedForCleanup(maintenanceCtx, record); err != nil {
			return operationError(record, err)
		}
		_, err := c.transition(maintenanceCtx, record, StateDone, JournalPatch{Result: record.Result})
		return operationError(record, err)
	}

	current := record
	if current.Mutation.Kind == MutationPut && current.Body == nil && current.Staged != nil {
		prepared, err := c.prepareHiddenCleanupReceipt(maintenanceCtx, current)
		if err != nil {
			return operationError(current, err)
		}
		current = prepared
	}
	if current.Body != nil {
		if err := c.remote.DiscardHidden(maintenanceCtx, current.OperationID, current.Body); err != nil {
			return operationError(current, err)
		}
	} else if current.Mutation.Kind == MutationPut {
		// Legacy/corrupt journals without staging cannot close the historical
		// send-receipt gap, but can still delete every durably projected part.
		if err := c.remote.DiscardHidden(maintenanceCtx, current.OperationID, nil); err != nil {
			return operationError(current, err)
		}
	}
	if err := c.removeStagedForCleanup(maintenanceCtx, current); err != nil {
		return operationError(current, err)
	}
	_, err := c.transition(maintenanceCtx, current, StateAborted, JournalPatch{ErrorCode: "aborted during recovery"})
	return operationError(current, err)
}
