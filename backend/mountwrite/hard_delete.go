package mountwrite

import (
	"context"
	"log/slog"
)

const (
	hardDeletePlanPageSize = 500
	hardDeleteBatchSize    = 100
)

// HardDelete commits a durable namespace marker, imports the exact backing
// message plan into the local journal, and synchronously deletes every body.
// A caller receives success only after the projection has finalized the plan.
func (c *Coordinator) HardDelete(ctx context.Context, request HardDeleteRequest) (MutationResult, error) {
	if c == nil || ctx == nil {
		return MutationResult{}, newOperationError(request.OperationID, MutationHardDelete, ErrInvalidRequest)
	}
	if err := request.Validate(); err != nil {
		return MutationResult{}, newOperationError(request.OperationID, MutationHardDelete, err)
	}
	if _, _, err := c.hardDeleteCapabilities(); err != nil {
		return MutationResult{}, newOperationError(request.OperationID, MutationHardDelete, err)
	}

	operationID := c.operationID(request.OperationID)
	mutation := request.mutation()
	result, err := c.withOperation(ctx, operationID, mutation, func(ctx context.Context, record JournalRecord) (MutationResult, error) {
		prepared, transitionErr := c.transition(ctx, record, StateStaged, JournalPatch{})
		if transitionErr != nil {
			c.markAborted(ctx, record, transitionErr)
			return MutationResult{}, operationError(record, transitionErr)
		}
		return c.commitPrepared(ctx, prepared)
	})
	if err != nil {
		slog.Warn("mountwrite: hard delete pending", "object_id", request.ObjectID, "operation_id", operationID, "error", err)
	}
	return result, err
}

func (c *Coordinator) hardDeleteCapabilities() (HardDeleteJournal, HardDeleteRemote, error) {
	journal, journalOK := c.journal.(HardDeleteJournal)
	remote, remoteOK := c.remote.(HardDeleteRemote)
	if !journalOK || !remoteOK {
		return nil, nil, ErrUnavailable
	}
	return journal, remote, nil
}

func (c *Coordinator) resumeHardDelete(ctx context.Context, record JournalRecord) (MutationResult, error) {
	if record.Mutation.Kind != MutationHardDelete {
		return MutationResult{}, operationError(record, ErrInvalidRequest)
	}
	result, err := requireResult(record)
	if err != nil {
		return MutationResult{}, operationError(record, err)
	}
	journal, remote, err := c.hardDeleteCapabilities()
	if err != nil {
		return MutationResult{}, operationError(record, err)
	}

	current := record
	if current.State == StateRemoteCommitted {
		if err := c.invalidator.Invalidate(ctx, c.exactInvalidation(current, result)); err != nil {
			return MutationResult{}, operationError(current, err)
		}
		current, err = c.transition(ctx, current, StateDeletePlanPending, JournalPatch{Result: &result})
		if err != nil {
			return MutationResult{}, operationError(record, err)
		}
	}
	if current.State == StateDeletePlanPending {
		if err := c.importHardDeletePlan(ctx, current, journal, remote); err != nil {
			return MutationResult{}, operationError(current, err)
		}
		current, err = c.transition(ctx, current, StateDeletingBodies, JournalPatch{})
		if err != nil {
			return MutationResult{}, operationError(current, err)
		}
	}
	if current.State == StateDeletingBodies {
		current, err = c.deleteHardDeleteBodies(ctx, current, journal, remote)
		if err != nil {
			return MutationResult{}, operationError(current, err)
		}
	}
	if current.State == StateDeleteFinalizing {
		if err := remote.FinalizeHardDelete(ctx, current.OperationID); err != nil {
			return MutationResult{}, operationError(current, err)
		}
		if err := journal.CompactHardDeletePlan(ctx, current.OperationID); err != nil {
			return MutationResult{}, operationError(current, err)
		}
		current, err = c.transition(ctx, current, StateDone, JournalPatch{Result: &result})
		if err != nil {
			return MutationResult{}, operationError(current, err)
		}
	}
	if current.State != StateDone {
		return MutationResult{}, operationError(current, ErrInvalidTransition)
	}
	return result, nil
}

func (c *Coordinator) importHardDeletePlan(
	ctx context.Context,
	record JournalRecord,
	journal HardDeleteJournal,
	remote HardDeleteRemote,
) error {
	status, found, err := journal.HardDeletePlanStatus(ctx, record.OperationID)
	if err != nil {
		return err
	}
	for !found || !status.Sealed {
		messageIDs, total, done, err := remote.HardDeletePlan(
			ctx,
			record.OperationID,
			status.Cursor,
			hardDeletePlanPageSize,
		)
		if err != nil {
			return err
		}
		if len(messageIDs) == 0 && !done {
			return ErrUnavailable
		}
		if len(messageIDs) > 0 && messageIDs[0] <= status.Cursor {
			return ErrInvalidRequest
		}
		if err := journal.AppendHardDeletePlan(ctx, record.OperationID, messageIDs, total, done); err != nil {
			return err
		}
		status, found, err = journal.HardDeletePlanStatus(ctx, record.OperationID)
		if err != nil {
			return err
		}
	}
	if status.PlannedCount != status.ExpectedCount || status.CompletedCount > status.PlannedCount {
		return ErrConflict
	}
	return nil
}

func (c *Coordinator) deleteHardDeleteBodies(
	ctx context.Context,
	record JournalRecord,
	journal HardDeleteJournal,
	remote HardDeleteRemote,
) (JournalRecord, error) {
	current := record
	for {
		messageIDs, err := journal.NextHardDeleteBatch(ctx, current.OperationID, hardDeleteBatchSize)
		if err != nil {
			return current, err
		}
		if len(messageIDs) == 0 {
			status, found, statusErr := journal.HardDeletePlanStatus(ctx, current.OperationID)
			if statusErr != nil {
				return current, statusErr
			}
			if !found || !status.Sealed || status.PlannedCount != status.ExpectedCount ||
				status.CompletedCount != status.ExpectedCount {
				return current, ErrConflict
			}
			return c.transition(ctx, current, StateDeleteFinalizing, JournalPatch{})
		}
		if err := remote.DeleteBodies(ctx, current.OperationID, messageIDs); err != nil {
			return current, err
		}
		if err := journal.MarkHardDeleteBatchDone(ctx, current.OperationID, messageIDs); err != nil {
			// Telegram deletion is idempotent. If this local checkpoint fails,
			// recovery safely repeats only this uncheckpointed batch.
			return current, err
		}
	}
}

func isHardDeleteState(state JournalState) bool {
	return state == StateDeletePlanPending || state == StateDeletingBodies || state == StateDeleteFinalizing
}
