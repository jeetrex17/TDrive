package mountwrite

import (
	"context"
	"errors"
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
		return RecoveryReport{}, newOperationError("recovery", MutationPut, err)
	}
	report := RecoveryReport{Examined: len(records)}
	failures := make([]error, 0)
	for _, listed := range records {
		state, err := c.recoverOne(ctx, listed.OperationID)
		if err != nil {
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
	if record.Staged != nil {
		if err := c.staging.Remove(maintenanceCtx, *record.Staged); err != nil {
			return operationError(record, err)
		}
	}
	// A receipt-only result means the visibility commit was rejected and its
	// hidden body still needs cleanup. Confirmed mutations always have a stable
	// logical object identity.
	if record.Result != nil && record.Result.ObjectID != "" {
		_, err := c.transition(maintenanceCtx, record, StateDone, JournalPatch{Result: record.Result})
		return operationError(record, err)
	}
	if err := c.remote.DiscardHidden(maintenanceCtx, record.OperationID, record.Body); err != nil {
		return operationError(record, err)
	}
	_, err := c.transition(maintenanceCtx, record, StateAborted, JournalPatch{ErrorCode: "aborted during recovery"})
	return operationError(record, err)
}
