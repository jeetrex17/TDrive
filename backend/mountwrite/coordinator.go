package mountwrite

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"
)

const maintenanceTimeout = 30 * time.Second

// maxHiddenCleanupParts mirrors the bounded hidden-upload protocol. It lives at
// the Remote trust boundary so a malformed adapter cannot journal an unbounded
// delete set.
const maxHiddenCleanupParts = 32

type CoordinatorConfig struct {
	Journal             Journal
	Staging             StagingStore
	Remote              Remote
	Invalidator         SnapshotInvalidator
	IDGenerator         IDGenerator
	MaxActiveOperations int
	MaxQueuedOperations int
	Now                 func() time.Time
}

// Coordinator serializes conflicting mutations, bounds admitted work, and
// persists every visibility-relevant transition through Journal.
type Coordinator struct {
	journal     Journal
	staging     StagingStore
	remote      Remote
	invalidator SnapshotInvalidator
	ids         IDGenerator
	now         func() time.Time
	locks       *KeyedLocker
	slots       chan struct{}
	admission   chan struct{}

	lifecycleMu sync.Mutex
	accepting   bool
	active      int
	executing   int
	idle        chan struct{}
}

// NewCoordinator constructs an online-only writable coordinator. Schema and
// staging initialization must be completed before this call.
func NewCoordinator(config CoordinatorConfig) (*Coordinator, error) {
	if config.Journal == nil || config.Staging == nil || config.Remote == nil || config.Invalidator == nil {
		return nil, ErrInvalidRequest
	}
	if config.MaxActiveOperations <= 0 || config.MaxQueuedOperations < 0 {
		return nil, ErrInvalidRequest
	}
	if config.IDGenerator == nil {
		config.IDGenerator = UUIDGenerator{}
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	idle := make(chan struct{})
	close(idle)
	return &Coordinator{
		journal:     config.Journal,
		staging:     config.Staging,
		remote:      config.Remote,
		invalidator: config.Invalidator,
		ids:         config.IDGenerator,
		now:         config.Now,
		locks:       NewKeyedLocker(),
		slots:       make(chan struct{}, config.MaxActiveOperations),
		admission:   make(chan struct{}, config.MaxActiveOperations+config.MaxQueuedOperations),
		accepting:   true,
		idle:        idle,
	}, nil
}

type UUIDGenerator struct{}

func (UUIDGenerator) NewID() string {
	return uuid.NewString()
}

func (c *Coordinator) Capabilities() Capabilities {
	if c == nil {
		return Capabilities{}
	}
	return Capabilities{
		Writable:      true,
		PersonalOnly:  true,
		PlaintextOnly: false,
		OnlineOnly:    true,
	}
}

// Status returns a concurrency-safe lifecycle snapshot. Active includes both
// executing and queued work.
func (c *Coordinator) Status() Status {
	if c == nil {
		return Status{}
	}
	c.lifecycleMu.Lock()
	defer c.lifecycleMu.Unlock()
	return Status{
		Accepting: c.accepting,
		Active:    c.active,
		Executing: c.executing,
		Queued:    c.active - c.executing,
	}
}

// Drain rejects new work and waits for all admitted operations to finish.
func (c *Coordinator) Drain(ctx context.Context) error {
	if c == nil || ctx == nil {
		return ErrInvalidRequest
	}
	c.lifecycleMu.Lock()
	c.accepting = false
	idle := c.idle
	c.lifecycleMu.Unlock()
	select {
	case <-idle:
		return nil
	case <-ctx.Done():
		return ErrCanceled
	}
}

// Close drains the coordinator. It is safe to call more than once.
func (c *Coordinator) Close(ctx context.Context) error {
	return c.Drain(ctx)
}

func (c *Coordinator) begin(ctx context.Context) (func(), error) {
	if c == nil || ctx == nil {
		return nil, ErrInvalidRequest
	}
	c.lifecycleMu.Lock()
	if !c.accepting {
		c.lifecycleMu.Unlock()
		return nil, ErrDraining
	}
	select {
	case c.admission <- struct{}{}:
	default:
		c.lifecycleMu.Unlock()
		return nil, ErrBusy
	}
	if c.active == 0 {
		c.idle = make(chan struct{})
	}
	c.active++
	c.lifecycleMu.Unlock()

	select {
	case c.slots <- struct{}{}:
		c.lifecycleMu.Lock()
		c.executing++
		c.lifecycleMu.Unlock()
		var once sync.Once
		return func() {
			once.Do(func() {
				<-c.slots
				<-c.admission
				c.finishActive(true)
			})
		}, nil
	case <-ctx.Done():
		<-c.admission
		c.finishActive(false)
		return nil, ErrCanceled
	}
}

func (c *Coordinator) finishActive(wasExecuting bool) {
	c.lifecycleMu.Lock()
	defer c.lifecycleMu.Unlock()
	c.active--
	if wasExecuting {
		c.executing--
	}
	if c.active == 0 {
		close(c.idle)
	}
}

func (c *Coordinator) operationID(requested string) string {
	if requested != "" {
		return requested
	}
	return c.ids.NewID()
}

func (c *Coordinator) createOrLoad(
	ctx context.Context,
	operationID string,
	mutation Mutation,
) (JournalRecord, bool, error) {
	existing, found, err := c.journal.Get(ctx, operationID)
	if err != nil {
		return JournalRecord{}, false, err
	}
	if found {
		if existing.Mutation != mutation {
			return JournalRecord{}, false, ErrConflict
		}
		return existing, false, nil
	}
	now := c.now().UTC()
	record := JournalRecord{
		OperationID: operationID,
		Mutation:    mutation,
		State:       StateReceiving,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	if err := c.journal.Create(ctx, record); err != nil {
		if errors.Is(err, ErrOperationExists) {
			return c.loadAfterCreateRace(ctx, operationID, mutation)
		}
		return JournalRecord{}, false, err
	}
	return record, true, nil
}

func (c *Coordinator) loadAfterCreateRace(
	ctx context.Context,
	operationID string,
	mutation Mutation,
) (JournalRecord, bool, error) {
	record, found, err := c.journal.Get(ctx, operationID)
	if err != nil {
		return JournalRecord{}, false, err
	}
	if !found {
		return JournalRecord{}, false, ErrJournalConflict
	}
	if record.Mutation != mutation {
		return JournalRecord{}, false, ErrConflict
	}
	return record, false, nil
}

func (c *Coordinator) existingResult(ctx context.Context, record JournalRecord) (MutationResult, bool, error) {
	if record.Result == nil {
		return MutationResult{}, false, nil
	}
	switch record.State {
	case StateDone, StateCleanupPending:
		return *record.Result, true, nil
	case StateProjectionPending, StateRemoteCommitted:
		result, err := c.finalizeCommitted(ctx, record)
		return result, true, err
	default:
		return MutationResult{}, false, nil
	}
}

func (c *Coordinator) withOperation(
	ctx context.Context,
	operationID string,
	mutation Mutation,
	fn func(context.Context, JournalRecord) (MutationResult, error),
) (MutationResult, error) {
	finish, err := c.begin(ctx)
	if err != nil {
		return MutationResult{}, newOperationError(operationID, mutation.Kind, err)
	}
	defer finish()

	keys := append(mutation.lockKeys(), "operation:"+operationID)
	release, err := c.locks.Lock(ctx, keys...)
	if err != nil {
		return MutationResult{}, newOperationError(operationID, mutation.Kind, err)
	}
	defer release()

	record, created, err := c.createOrLoad(ctx, operationID, mutation)
	if err != nil {
		return MutationResult{}, newOperationError(operationID, mutation.Kind, err)
	}
	if result, handled, err := c.existingResult(ctx, record); handled {
		return result, err
	}
	if !created {
		return MutationResult{}, newOperationError(operationID, mutation.Kind, ErrOperationInProgress)
	}
	return fn(ctx, record)
}

func (c *Coordinator) transition(
	ctx context.Context,
	record JournalRecord,
	next JournalState,
	patch JournalPatch,
) (JournalRecord, error) {
	if patch.UpdatedAt.IsZero() {
		patch.UpdatedAt = c.now().UTC()
	}
	slog.Debug("mountwrite: journal transition", "operation_id", record.OperationID, "from", record.State, "to", next)
	updated, err := c.journal.Transition(ctx, record.OperationID, record.State, next, patch)
	if err != nil {
		slog.Warn("mountwrite: journal transition failed", "operation_id", record.OperationID, "from", record.State, "to", next, "error", err)
	}
	return updated, err
}

func (c *Coordinator) maintenanceContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(ctx), maintenanceTimeout)
}

func (c *Coordinator) markAborted(ctx context.Context, record JournalRecord, cause error) {
	maintenanceCtx, cancel := c.maintenanceContext(ctx)
	defer cancel()
	if isTerminal(record.State) {
		return
	}
	slog.Warn("mountwrite: aborting operation", "operation_id", record.OperationID, "state", record.State, "cause", cause)

	current := record
	// StateUploading is the sole boundary where Telegram may have accepted one
	// part whose MsgID is not yet in the journal. Recover it from the verified
	// staged bytes and durably patch the exact body before staging can be
	// removed. This also handles a locally-known body whose StateUploaded
	// transition failed: it still must become durable first.
	if current.State == StateUploading {
		prepared, err := c.prepareHiddenCleanupReceipt(maintenanceCtx, current)
		if err != nil {
			// Never persist an adapter-supplied body that failed receipt-boundary
			// validation. The verified stage remains the only recovery authority.
			current.Body = nil
			c.deferCleanup(maintenanceCtx, current, err)
			return
		}
		current = prepared
	}

	needsRemoteCleanup := current.Body != nil || current.State == StateUploaded
	var cleanupErr error
	if needsRemoteCleanup {
		cleanupErr = c.remote.DiscardHidden(maintenanceCtx, current.OperationID, current.Body)
	}
	if cleanupErr == nil {
		cleanupErr = c.removeStagedForCleanup(maintenanceCtx, current)
	}
	if cleanupErr != nil {
		// Before Uploading there cannot be a remote receipt. Leave the original
		// state intact when only local stage removal failed, so recovery never
		// invents and uploads a cleanup candidate for an operation that did not
		// reach Telegram.
		if current.Body == nil && (current.State == StateReceiving || current.State == StateStaged) {
			return
		}
		c.deferCleanup(maintenanceCtx, current, cleanupErr)
		return
	}
	_, _ = c.transition(maintenanceCtx, current, StateAborted, JournalPatch{ErrorCode: safeErrorLabel(classifyError(cause))})
}

func (c *Coordinator) prepareHiddenCleanupReceipt(ctx context.Context, record JournalRecord) (JournalRecord, error) {
	if record.Mutation.Kind != MutationPut || record.Staged == nil {
		return record, ErrNotFound
	}
	if err := validateStagedMutation(record.Mutation, *record.Staged); err != nil {
		return record, err
	}

	body := record.Body
	recoveredReceipt := body == nil
	if body == nil {
		source, err := c.staging.Open(*record.Staged)
		if err != nil {
			return record, err
		}
		recovered, recoverErr := c.remote.RecoverHidden(ctx, hiddenUploadFromRecord(record), source)
		closeErr := source.Close()
		if recoverErr != nil {
			return record, recoverErr
		}
		if closeErr != nil {
			return record, closeErr
		}
		recovered = withStagedMetadata(recovered, *record.Staged)
		body = &recovered
	} else {
		copyOfBody := cloneBody(*body)
		copyOfBody = withStagedMetadata(copyOfBody, *record.Staged)
		body = &copyOfBody
	}
	if recoveredReceipt || body.UploadUUID != "" {
		if err := validateHiddenCleanupBody(record.Mutation, *body, recoveredReceipt); err != nil {
			return record, err
		}
	} else if err := validateRemoteBody(record.Mutation, *body); err != nil {
		return record, err
	}
	return c.transition(ctx, record, StateCleanupPending, JournalPatch{Body: body})
}

func validateHiddenCleanupBody(mutation Mutation, body RemoteBody, requireReceipt bool) error {
	if err := validateRemoteBody(mutation, body); err != nil {
		return err
	}
	if !validCommitRef(body.UploadUUID) || body.ContentRef != "" || body.PartCount <= 0 ||
		body.PartCount > maxHiddenCleanupParts || len(body.MessageIDs) > body.PartCount ||
		(requireReceipt && len(body.MessageIDs) == 0) {
		return ErrInvalidRequest
	}
	seen := make(map[int64]struct{}, len(body.MessageIDs))
	for _, msgID := range body.MessageIDs {
		if msgID <= 0 {
			return ErrInvalidRequest
		}
		if _, duplicate := seen[msgID]; duplicate {
			return ErrInvalidRequest
		}
		seen[msgID] = struct{}{}
	}
	return nil
}

func (c *Coordinator) removeStagedForCleanup(ctx context.Context, record JournalRecord) error {
	if record.Staged != nil {
		return c.staging.Remove(ctx, *record.Staged)
	}
	if record.Mutation.Kind == MutationPut {
		return c.staging.RemoveOperation(ctx, record.OperationID)
	}
	return nil
}

func (c *Coordinator) deferCleanup(ctx context.Context, record JournalRecord, cause error) {
	_, _ = c.transition(ctx, record, StateCleanupPending, JournalPatch{
		Body:      record.Body,
		ErrorCode: safeErrorLabel(classifyError(cause)),
	})
}

func (c *Coordinator) exactInvalidation(record JournalRecord, result MutationResult) SnapshotInvalidation {
	return SnapshotInvalidation{
		OperationID: record.OperationID,
		DriveID:     record.Mutation.DriveID,
		ParentIDs:   record.Mutation.AffectedParents(),
		ObjectIDs:   record.Mutation.AffectedObjects(result),
	}
}

func operationError(record JournalRecord, err error) error {
	return newOperationError(record.OperationID, record.Mutation.Kind, err)
}

func requireResult(record JournalRecord) (MutationResult, error) {
	if record.Result == nil {
		return MutationResult{}, fmt.Errorf("committed journal record has no result")
	}
	return *record.Result, nil
}

var _ IDGenerator = UUIDGenerator{}
