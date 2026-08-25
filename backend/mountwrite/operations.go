package mountwrite

import (
	"context"
	"errors"
	"io"
)

func (c *Coordinator) Put(ctx context.Context, request PutRequest, source io.Reader) (MutationResult, error) {
	if c == nil || ctx == nil {
		return MutationResult{}, newOperationError(request.OperationID, MutationPut, ErrInvalidRequest)
	}
	if err := request.Validate(); err != nil {
		return MutationResult{}, newOperationError(request.OperationID, MutationPut, err)
	}
	if source == nil {
		return MutationResult{}, newOperationError(request.OperationID, MutationPut, ErrInvalidRequest)
	}
	operationID := c.operationID(request.OperationID)
	mutation := request.mutation()
	stageRequest := request.stageRequest(operationID)
	defer clearBytes(stageRequest.MasterKey)
	return c.withOperation(ctx, operationID, mutation, func(ctx context.Context, record JournalRecord) (MutationResult, error) {
		staged, err := c.staging.Stage(ctx, stageRequest, source)
		if err != nil {
			c.markAborted(ctx, record, err)
			return MutationResult{}, operationError(record, err)
		}
		stagedRecord, err := c.transition(ctx, record, StateStaged, JournalPatch{Staged: &staged})
		if err != nil {
			maintenanceCtx, cancel := c.maintenanceContext(ctx)
			_ = c.staging.Remove(maintenanceCtx, staged)
			cancel()
			c.markAborted(ctx, record, err)
			return MutationResult{}, operationError(record, err)
		}
		return c.uploadStaged(ctx, stagedRecord)
	})
}

func (c *Coordinator) Mkdir(ctx context.Context, request MkdirRequest) (MutationResult, error) {
	if c == nil || ctx == nil {
		return MutationResult{}, newOperationError(request.OperationID, MutationMkdir, ErrInvalidRequest)
	}
	if err := request.Validate(); err != nil {
		return MutationResult{}, newOperationError(request.OperationID, MutationMkdir, err)
	}
	return c.executeMetadata(ctx, c.operationID(request.OperationID), request.mutation())
}

func (c *Coordinator) Move(ctx context.Context, request MoveRequest) (MutationResult, error) {
	if c == nil || ctx == nil {
		return MutationResult{}, newOperationError(request.OperationID, MutationMove, ErrInvalidRequest)
	}
	if err := request.Validate(); err != nil {
		return MutationResult{}, newOperationError(request.OperationID, MutationMove, err)
	}
	return c.executeMetadata(ctx, c.operationID(request.OperationID), request.mutation())
}

func (c *Coordinator) Delete(ctx context.Context, request DeleteRequest) (MutationResult, error) {
	if c == nil || ctx == nil {
		return MutationResult{}, newOperationError(request.OperationID, MutationDelete, ErrInvalidRequest)
	}
	if err := request.Validate(); err != nil {
		return MutationResult{}, newOperationError(request.OperationID, MutationDelete, err)
	}
	return c.executeMetadata(ctx, c.operationID(request.OperationID), request.mutation())
}

func (c *Coordinator) executeMetadata(
	ctx context.Context,
	operationID string,
	mutation Mutation,
) (MutationResult, error) {
	return c.withOperation(ctx, operationID, mutation, func(ctx context.Context, record JournalRecord) (MutationResult, error) {
		prepared, err := c.transition(ctx, record, StateStaged, JournalPatch{})
		if err != nil {
			c.markAborted(ctx, record, err)
			return MutationResult{}, operationError(record, err)
		}
		return c.commitPrepared(ctx, prepared)
	})
}

func (c *Coordinator) uploadStaged(ctx context.Context, record JournalRecord) (MutationResult, error) {
	if record.Staged == nil {
		c.markAborted(ctx, record, ErrNotFound)
		return MutationResult{}, operationError(record, ErrNotFound)
	}
	if err := validateStagedMutation(record.Mutation, *record.Staged); err != nil {
		c.markAborted(ctx, record, err)
		return MutationResult{}, operationError(record, err)
	}
	source, err := c.staging.Open(*record.Staged)
	if err != nil {
		c.markAborted(ctx, record, err)
		return MutationResult{}, operationError(record, err)
	}
	current := record
	if current.State == StateStaged {
		current, err = c.transition(ctx, current, StateUploading, JournalPatch{})
		if err != nil {
			_ = source.Close()
			c.markAborted(ctx, record, err)
			return MutationResult{}, operationError(record, err)
		}
	}
	body, uploadErr := c.remote.UploadHidden(ctx, hiddenUploadFromRecord(current), source)
	_ = source.Close()
	if uploadErr != nil {
		// Definite failures may still return exact receipts for the accepted
		// prefix/current part (for example, local projection failed after a
		// positive MsgID). Persist those before cleanup. Unknown outcomes omit
		// the uncertain receipt and must be reconciled from the staged source.
		if !errors.Is(uploadErr, ErrUploadOutcomeUnknown) && hasExactHiddenReceipt(body) {
			body = withStagedMetadata(body, *current.Staged)
			current.Body = &body
		}
		c.markAborted(ctx, current, uploadErr)
		return MutationResult{}, operationError(current, uploadErr)
	}
	body = withStagedMetadata(body, *current.Staged)
	uploaded, err := c.transition(ctx, current, StateUploaded, JournalPatch{Body: &body})
	if err != nil {
		current.Body = &body
		c.markAborted(ctx, current, err)
		return MutationResult{}, operationError(current, err)
	}
	return c.commitPrepared(ctx, uploaded)
}

func hasExactHiddenReceipt(body RemoteBody) bool {
	return body.ContentRef != "" || (body.UploadUUID != "" && body.PartCount > 0)
}

func hiddenUploadFromRecord(record JournalRecord) HiddenUpload {
	if record.Staged == nil {
		return HiddenUpload{}
	}
	return HiddenUpload{
		OperationID:       record.OperationID,
		DriveID:           record.Mutation.DriveID,
		ParentID:          record.Mutation.DestinationParentID,
		Name:              record.Mutation.DestinationName,
		PlaintextSize:     record.Staged.PlaintextSize,
		StoredSize:        record.Staged.StoredSize,
		SHA256:            record.Staged.SHA256,
		StoredSHA256:      record.Staged.StoredSHA256,
		EncryptionVersion: record.Staged.EncryptionVersion,
		Encrypted:         record.Staged.EncryptionVersion != EncryptionNone,
	}
}

func validateStagedMutation(mutation Mutation, staged StagedObject) error {
	if err := validateStagedObject(staged); err != nil {
		return err
	}
	if mutation.Kind != MutationPut || mutation.EncryptionVersion != staged.EncryptionVersion {
		return ErrInvalidRequest
	}
	if mutation.ContentLength >= 0 && mutation.ContentLength != staged.PlaintextSize {
		return ErrLengthMismatch
	}
	return nil
}

func withStagedMetadata(body RemoteBody, staged StagedObject) RemoteBody {
	body.PlaintextSize = staged.PlaintextSize
	body.StoredSize = staged.StoredSize
	body.EncryptionVersion = staged.EncryptionVersion
	body.Encrypted = staged.EncryptionVersion != EncryptionNone
	body.SHA256 = staged.SHA256
	body.StoredSHA256 = staged.StoredSHA256
	return body
}

func (c *Coordinator) commitPrepared(ctx context.Context, record JournalRecord) (MutationResult, error) {
	committing, err := c.transition(ctx, record, StateCommitting, JournalPatch{})
	if err != nil {
		c.markAborted(ctx, record, err)
		return MutationResult{}, operationError(record, err)
	}
	return c.commit(ctx, committing)
}

func (c *Coordinator) commit(ctx context.Context, record JournalRecord) (MutationResult, error) {
	current := record
	result, err := c.remote.Commit(ctx, CommitRequest{
		OperationID: record.OperationID,
		Mutation:    record.Mutation,
		Body:        record.Body,
		CommitTime:  record.CreatedAt,
		PersistCommitRef: func(commitRef string) error {
			if !validCommitRef(commitRef) {
				return ErrInvalidRequest
			}
			if current.State == StateReconciling {
				if current.Result != nil && current.Result.CommitRef == commitRef {
					return nil
				}
				return ErrConflict
			}
			if current.State != StateCommitting {
				return ErrInvalidTransition
			}
			uncertain := MutationResult{OperationID: current.OperationID, CommitRef: commitRef}
			next, persistErr := c.transition(ctx, current, StateReconciling, JournalPatch{
				Result:    &uncertain,
				ErrorCode: "commit accepted; projection pending",
			})
			if persistErr == nil {
				current = next
			}
			return persistErr
		},
	})
	if errors.Is(err, ErrCommitOutcomeUnknown) {
		return c.reconcileUnknown(ctx, current, result)
	}
	if err != nil {
		if current.State == StateReconciling {
			return c.reconcileUnknown(ctx, current, result)
		}
		c.markAborted(ctx, current, err)
		return MutationResult{}, operationError(current, err)
	}
	return c.confirmCommitted(ctx, current, normalizeResult(current, result))
}

func (c *Coordinator) reconcileUnknown(
	ctx context.Context,
	record JournalRecord,
	uncertain MutationResult,
) (MutationResult, error) {
	reconciling := record
	if record.State == StateReconciling {
		if uncertain.CommitRef != "" && (record.Result == nil || record.Result.CommitRef != uncertain.CommitRef) {
			return MutationResult{}, operationError(record, ErrConflict)
		}
	} else {
		patch := JournalPatch{ErrorCode: "commit outcome unknown"}
		if uncertain.CommitRef != "" {
			if !validCommitRef(uncertain.CommitRef) {
				return MutationResult{}, operationError(record, ErrInvalidRequest)
			}
			uncertain.OperationID = record.OperationID
			patch.Result = &uncertain
		}
		var err error
		reconciling, err = c.transition(ctx, record, StateReconciling, patch)
		if err != nil {
			return MutationResult{}, operationError(record, err)
		}
	}
	result, found, err := c.reconcile(ctx, reconciling)
	if err != nil {
		if isDefiniteCommitRejection(err) {
			c.markAborted(ctx, reconciling, err)
		}
		return MutationResult{}, operationError(reconciling, err)
	}
	if !found {
		return MutationResult{}, operationError(record, ErrUnavailable)
	}
	return c.confirmCommitted(ctx, reconciling, normalizeResult(reconciling, result))
}

func isDefiniteCommitRejection(err error) bool {
	return errors.Is(err, ErrInvalidRequest) ||
		errors.Is(err, ErrForbidden) ||
		errors.Is(err, ErrNotFound) ||
		errors.Is(err, ErrConflict) ||
		errors.Is(err, ErrPreconditionFailed)
}

func (c *Coordinator) reconcile(ctx context.Context, record JournalRecord) (MutationResult, bool, error) {
	if record.Result != nil && record.Result.CommitRef != "" {
		if !validCommitRef(record.Result.CommitRef) {
			return MutationResult{}, false, ErrInvalidRequest
		}
		if remote, ok := c.remote.(ReceiptReconciler); ok {
			var body *RemoteBody
			if record.Body != nil {
				copyOfBody := cloneBody(*record.Body)
				body = &copyOfBody
			}
			return remote.ReconcileReceipt(ctx, CommitRequest{
				OperationID: record.OperationID,
				Mutation:    record.Mutation,
				Body:        body,
				CommitTime:  record.CreatedAt,
			}, record.Result.CommitRef)
		}
	}
	return c.remote.Reconcile(ctx, record.OperationID)
}

func (c *Coordinator) confirmCommitted(
	ctx context.Context,
	record JournalRecord,
	result MutationResult,
) (MutationResult, error) {
	maintenanceCtx, cancel := c.maintenanceContext(ctx)
	defer cancel()
	committed, err := c.transition(maintenanceCtx, record, StateRemoteCommitted, JournalPatch{Result: &result})
	if err != nil {
		return c.finalizeWithoutDurableCommit(maintenanceCtx, record, result), nil
	}
	return c.finalizeCommitted(maintenanceCtx, committed)
}

func (c *Coordinator) finalizeCommitted(ctx context.Context, record JournalRecord) (MutationResult, error) {
	result, err := requireResult(record)
	if err != nil {
		return MutationResult{}, operationError(record, err)
	}
	maintenanceCtx, cancel := c.maintenanceContext(ctx)
	defer cancel()
	if err := c.invalidator.Invalidate(maintenanceCtx, c.exactInvalidation(record, result)); err != nil {
		result.ProjectionPending = true
		if record.State == StateRemoteCommitted {
			_, _ = c.transition(maintenanceCtx, record, StateProjectionPending, JournalPatch{
				Result:    &result,
				ErrorCode: "projection pending",
			})
		}
		return result, nil
	}
	result.ProjectionPending = false
	if record.Staged != nil {
		if err := c.staging.Remove(maintenanceCtx, *record.Staged); err != nil {
			_, _ = c.transition(maintenanceCtx, record, StateCleanupPending, JournalPatch{
				Result:    &result,
				ErrorCode: "cleanup pending",
			})
			return result, nil
		}
	}
	_, transitionErr := c.transition(maintenanceCtx, record, StateDone, JournalPatch{Result: &result})
	if transitionErr != nil {
		result.ProjectionPending = true
	}
	return result, nil
}

func (c *Coordinator) finalizeWithoutDurableCommit(
	ctx context.Context,
	record JournalRecord,
	result MutationResult,
) MutationResult {
	result.ProjectionPending = true
	_ = c.invalidator.Invalidate(ctx, c.exactInvalidation(record, result))
	if record.Staged != nil {
		_ = c.staging.Remove(ctx, *record.Staged)
	}
	return result
}

func normalizeResult(record JournalRecord, result MutationResult) MutationResult {
	result.OperationID = record.OperationID
	if result.CommitRef == "" && record.Result != nil {
		result.CommitRef = record.Result.CommitRef
	}
	if result.ObjectID == "" {
		result.ObjectID = record.Mutation.ObjectID
	}
	if record.Body != nil {
		result.Size = record.Body.PlaintextSize
		result.SHA256 = record.Body.SHA256
	}
	return result
}
