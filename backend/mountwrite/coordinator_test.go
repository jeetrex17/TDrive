package mountwrite

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestCoordinatorPutCommitsBeforeInvalidatingAndReturnsCommittedResult(t *testing.T) {
	t.Parallel()

	events := &eventLog{}
	remote := &fakeRemote{events: events}
	invalidator := &fakeInvalidator{events: events}
	coordinator, journal, staging := newTestCoordinator(t, remote, invalidator)
	payload := []byte("committed Telegram content")

	result, err := coordinator.Put(context.Background(), PutRequest{
		DriveID:       42,
		ParentID:      "",
		Name:          "notes.txt",
		ContentLength: int64(len(payload)),
		MaxBytes:      1024,
	}, bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("put: %v", err)
	}
	if result.OperationID != "generated-1" || result.ObjectID != "object-1" || result.Revision != 1 || !result.Created {
		t.Fatalf("result = %#v", result)
	}
	assertStringsEqual(t, events.snapshot(), []string{"upload", "commit", "invalidate"})

	record, found, err := journal.Get(context.Background(), result.OperationID)
	if err != nil || !found {
		t.Fatalf("journal get: found=%v err=%v", found, err)
	}
	if record.State != StateDone || record.Result == nil || record.Result.ObjectID != result.ObjectID {
		t.Fatalf("journal record = %#v", record)
	}
	if invalidator.calls != 1 {
		t.Fatalf("invalidation calls = %d, want 1", invalidator.calls)
	}
	wantInvalidation := SnapshotInvalidation{
		OperationID: result.OperationID,
		DriveID:     42,
		ParentIDs:   []string{""},
		ObjectIDs:   []string{"object-1"},
	}
	if !equalInvalidation(invalidator.last, wantInvalidation) {
		t.Fatalf("invalidation = %#v, want %#v", invalidator.last, wantInvalidation)
	}
	if staging.UsedBytes() != 0 {
		t.Fatalf("staging bytes after success = %d", staging.UsedBytes())
	}
}

func TestCoordinatorPutUploadFailureNeverCommitsAndCleansStage(t *testing.T) {
	t.Parallel()

	remote := &fakeRemote{uploadErr: errors.New("Telegram upload failed")}
	coordinator, journal, staging := newTestCoordinator(t, remote, &fakeInvalidator{})
	payload := []byte("not visible")
	_, err := coordinator.Put(context.Background(), PutRequest{
		OperationID:   "upload-fails",
		DriveID:       42,
		ParentID:      "",
		Name:          "failure.txt",
		ContentLength: int64(len(payload)),
		MaxBytes:      1024,
	}, bytes.NewReader(payload))
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("put error = %v, want ErrUnavailable", err)
	}
	if remote.commitCalls != 0 {
		t.Fatalf("commit calls = %d, want 0", remote.commitCalls)
	}
	record := mustJournalRecord(t, journal, "upload-fails")
	if record.State != StateAborted {
		t.Fatalf("state = %s, want aborted", record.State)
	}
	if staging.UsedBytes() != 0 {
		t.Fatalf("staging bytes after failure = %d", staging.UsedBytes())
	}
}

func TestCoordinatorPutAndMetadataValidateBoundaryFailures(t *testing.T) {
	t.Parallel()

	coordinator, journal, _ := newTestCoordinator(t, &fakeRemote{}, &fakeInvalidator{})
	if _, err := coordinator.Put(context.Background(), PutRequest{DriveID: 0, Name: "file"}, bytes.NewReader(nil)); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("invalid PUT error = %v", err)
	}
	if _, err := coordinator.Put(context.Background(), PutRequest{DriveID: 42, Name: "file", ContentLength: 0}, nil); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("nil PUT source error = %v", err)
	}
	if _, err := coordinator.Mkdir(context.Background(), MkdirRequest{DriveID: 42, Name: "bad/name"}); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("invalid MKDIR error = %v", err)
	}
	if _, err := coordinator.Move(context.Background(), MoveRequest{DriveID: 42, DestinationName: "file"}); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("invalid MOVE error = %v", err)
	}
	if _, err := coordinator.Delete(context.Background(), DeleteRequest{DriveID: 42}); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("invalid DELETE error = %v", err)
	}

	_, err := coordinator.Put(context.Background(), PutRequest{
		OperationID:   "length-mismatch",
		DriveID:       42,
		Name:          "file.txt",
		ContentLength: 10,
	}, bytes.NewReader([]byte("short")))
	if !errors.Is(err, ErrLengthMismatch) {
		t.Fatalf("length mismatch error = %v", err)
	}
	if state := mustJournalRecord(t, journal, "length-mismatch").State; state != StateAborted {
		t.Fatalf("state = %s, want aborted", state)
	}
}

func TestCoordinatorPutDefiniteCommitFailureDiscardsHiddenBody(t *testing.T) {
	t.Parallel()

	remote := &fakeRemote{commitErr: ErrConflict}
	coordinator, journal, _ := newTestCoordinator(t, remote, &fakeInvalidator{})
	payload := []byte("conflicting content")
	_, err := coordinator.Put(context.Background(), PutRequest{
		OperationID:   "commit-conflict",
		DriveID:       42,
		ParentID:      "",
		Name:          "conflict.txt",
		ContentLength: int64(len(payload)),
	}, bytes.NewReader(payload))
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("put error = %v, want ErrConflict", err)
	}
	if remote.discardCalls != 1 {
		t.Fatalf("hidden discard calls = %d, want 1", remote.discardCalls)
	}
	if state := mustJournalRecord(t, journal, "commit-conflict").State; state != StateAborted {
		t.Fatalf("state = %s, want aborted", state)
	}
}

func TestCoordinatorCleansHiddenBodyWhenUploadedJournalTransitionFails(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db := openJournalDB(t, filepath.Join(t.TempDir(), "journal.db"))
	if err := EnsureJournalSchema(ctx, db); err != nil {
		t.Fatalf("ensure schema: %v", err)
	}
	base, err := NewSQLiteJournal(db)
	if err != nil {
		t.Fatalf("new journal: %v", err)
	}
	journal := &failingTransitionJournal{Journal: base, failNext: StateUploaded}
	staging, err := NewDiskStagingStore(DiskStagingConfig{
		Root:              filepath.Join(t.TempDir(), "staging"),
		MaxObjectBytes:    1024,
		MaxAggregateBytes: 2048,
		MaxConcurrent:     1,
	})
	if err != nil {
		t.Fatalf("new staging: %v", err)
	}
	remote := &fakeRemote{}
	coordinator := buildCoordinator(t, journal, staging, remote, &fakeInvalidator{})
	payload := []byte("hidden but never committed")
	_, err = coordinator.Put(ctx, PutRequest{
		OperationID:   "uploaded-journal-failure",
		DriveID:       42,
		Name:          "hidden.txt",
		ContentLength: int64(len(payload)),
	}, bytes.NewReader(payload))
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("put error = %v, want ErrUnavailable", err)
	}
	if remote.discardCalls != 1 || remote.commitCalls != 0 || staging.UsedBytes() != 0 {
		t.Fatalf("discard=%d commit=%d staged=%d", remote.discardCalls, remote.commitCalls, staging.UsedBytes())
	}
}

func TestCoordinatorPutUnknownCommitOutcomeReconcilesWithoutDuplicateCommit(t *testing.T) {
	t.Parallel()

	remote := &fakeRemote{
		commitErr:      ErrCommitOutcomeUnknown,
		reconcileFound: true,
		reconcileResult: MutationResult{
			OperationID: "unknown-outcome",
			ObjectID:    "object-reconciled",
			Revision:    5,
			Created:     false,
		},
	}
	coordinator, journal, _ := newTestCoordinator(t, remote, &fakeInvalidator{})
	payload := []byte("maybe committed")
	result, err := coordinator.Put(context.Background(), PutRequest{
		OperationID:      "unknown-outcome",
		DriveID:          42,
		ParentID:         "",
		Name:             "existing.txt",
		ExistingObjectID: "existing-object",
		ExpectedRevision: 4,
		ContentLength:    int64(len(payload)),
	}, bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("put: %v", err)
	}
	if result.ObjectID != "object-reconciled" || remote.commitCalls != 1 || remote.reconcileCalls != 1 {
		t.Fatalf("result=%#v commitCalls=%d reconcileCalls=%d", result, remote.commitCalls, remote.reconcileCalls)
	}
	if state := mustJournalRecord(t, journal, "unknown-outcome").State; state != StateDone {
		t.Fatalf("state = %s, want done", state)
	}
}

func TestCoordinatorPersistsAndUsesUnknownCommitReceipt(t *testing.T) {
	t.Parallel()

	remote := &fakeRemote{
		commitErr:    ErrCommitOutcomeUnknown,
		commitResult: MutationResult{CommitRef: "451"},
		receiptFound: true,
		receiptResult: MutationResult{
			ObjectID: "object-from-receipt", Revision: 2,
		},
	}
	coordinator, journal, _ := newTestCoordinator(t, remote, &fakeInvalidator{})
	result, err := coordinator.Mkdir(context.Background(), MkdirRequest{
		OperationID: "receipt-op", DriveID: 42, Name: "Receipt",
	})
	if err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if result.ObjectID != "object-from-receipt" || remote.receiptCalls != 1 || remote.reconcileCalls != 0 {
		t.Fatalf("result=%+v receiptCalls=%d reconcileCalls=%d", result, remote.receiptCalls, remote.reconcileCalls)
	}
	record := mustJournalRecord(t, journal, "receipt-op")
	if record.Result == nil || record.Result.CommitRef != "451" {
		t.Fatalf("journal result=%+v, want persisted commit receipt", record.Result)
	}
}

func TestCoordinatorPersistsReceiptBeforeRemoteCommitReturns(t *testing.T) {
	t.Parallel()

	started := make(chan struct{})
	release := make(chan struct{})
	remote := &fakeRemote{
		commitAcceptedRef: "777",
		commitStarted:     started,
		commitRelease:     release,
		commitResult: MutationResult{
			ObjectID: "created-folder",
			Revision: 1,
		},
	}
	coordinator, journal, _ := newTestCoordinator(t, remote, &fakeInvalidator{})
	done := make(chan error, 1)
	go func() {
		_, err := coordinator.Mkdir(context.Background(), MkdirRequest{
			OperationID: "receipt-before-return", DriveID: 42, Name: "Durable",
		})
		done <- err
	}()
	<-started
	record := mustJournalRecord(t, journal, "receipt-before-return")
	if record.State != StateReconciling || record.Result == nil || record.Result.CommitRef != "777" {
		t.Fatalf("journal while commit blocked = %+v", record)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatalf("mkdir: %v", err)
	}
}

func TestCoordinatorUnknownCommitRemainsReconcilingWhenNotYetVisible(t *testing.T) {
	t.Parallel()

	remote := &fakeRemote{commitErr: ErrCommitOutcomeUnknown}
	coordinator, journal, _ := newTestCoordinator(t, remote, &fakeInvalidator{})
	payload := []byte("uncertain")
	_, err := coordinator.Put(context.Background(), PutRequest{
		OperationID:   "unknown-not-found",
		DriveID:       42,
		Name:          "uncertain.txt",
		ContentLength: int64(len(payload)),
	}, bytes.NewReader(payload))
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("put error = %v, want ErrUnavailable", err)
	}
	if state := mustJournalRecord(t, journal, "unknown-not-found").State; state != StateReconciling {
		t.Fatalf("state = %s, want reconciling", state)
	}
	if remote.commitCalls != 1 || remote.reconcileCalls != 1 || remote.discardCalls != 0 {
		t.Fatalf("calls commit=%d reconcile=%d discard=%d", remote.commitCalls, remote.reconcileCalls, remote.discardCalls)
	}
}

func TestCoordinatorReceiptRejectionAbortsAndCleansHiddenUpload(t *testing.T) {
	t.Parallel()

	remote := &fakeRemote{
		commitAcceptedRef: "778",
		commitErr:         ErrPreconditionFailed,
		reconcileErr:      ErrPreconditionFailed,
	}
	coordinator, journal, staging := newTestCoordinator(t, remote, &fakeInvalidator{})
	payload := []byte("rejected replacement")
	_, err := coordinator.Put(context.Background(), PutRequest{
		OperationID: "receipt-rejected", DriveID: 42, Name: "existing.txt",
		ExistingObjectID: "f:12", ExpectedRevision: 4,
		ContentLength: int64(len(payload)),
	}, bytes.NewReader(payload))
	if !errors.Is(err, ErrPreconditionFailed) {
		t.Fatalf("Put error = %v, want ErrPreconditionFailed", err)
	}
	record := mustJournalRecord(t, journal, "receipt-rejected")
	if record.State != StateAborted {
		t.Fatalf("state = %s, want aborted", record.State)
	}
	if remote.receiptCalls != 1 || remote.discardCalls != 1 || staging.UsedBytes() != 0 {
		t.Fatalf("receipt=%d discard=%d staged=%d", remote.receiptCalls, remote.discardCalls, staging.UsedBytes())
	}
}

func TestCoordinatorProjectionFailureReturnsSuccessAndPersistsRecovery(t *testing.T) {
	t.Parallel()

	invalidator := &fakeInvalidator{err: errors.New("projection database unavailable")}
	coordinator, journal, _ := newTestCoordinator(t, &fakeRemote{}, invalidator)
	payload := []byte("already remotely committed")
	result, err := coordinator.Put(context.Background(), PutRequest{
		OperationID:   "projection-pending",
		DriveID:       42,
		ParentID:      "",
		Name:          "committed.txt",
		ContentLength: int64(len(payload)),
	}, bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("confirmed remote commit should return success: %v", err)
	}
	if !result.ProjectionPending {
		t.Fatalf("result should report projection pending: %#v", result)
	}
	record := mustJournalRecord(t, journal, "projection-pending")
	if record.State != StateProjectionPending || record.Result == nil {
		t.Fatalf("journal record = %#v", record)
	}
	invalidator.mu.Lock()
	invalidator.err = nil
	invalidator.mu.Unlock()
	retried, err := coordinator.Put(context.Background(), PutRequest{
		OperationID:   "projection-pending",
		DriveID:       42,
		ParentID:      "",
		Name:          "committed.txt",
		ContentLength: int64(len(payload)),
	}, &failOnRead{})
	if err != nil || retried.ProjectionPending {
		t.Fatalf("projection retry result=%#v err=%v", retried, err)
	}
	if state := mustJournalRecord(t, journal, "projection-pending").State; state != StateDone {
		t.Fatalf("state after projection retry = %s, want done", state)
	}
}

func TestCoordinatorSuccessfulCommitPersistsCleanupWhenStageRemovalFails(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db := openJournalDB(t, filepath.Join(t.TempDir(), "journal.db"))
	if err := EnsureJournalSchema(ctx, db); err != nil {
		t.Fatalf("ensure schema: %v", err)
	}
	journal, err := NewSQLiteJournal(db)
	if err != nil {
		t.Fatalf("new journal: %v", err)
	}
	disk, err := NewDiskStagingStore(DiskStagingConfig{
		Root:              filepath.Join(t.TempDir(), "staging"),
		MaxObjectBytes:    1024,
		MaxAggregateBytes: 2048,
		MaxConcurrent:     1,
	})
	if err != nil {
		t.Fatalf("new staging: %v", err)
	}
	staging := &failingStaging{StagingStore: disk, removeErr: errors.New("disk temporarily busy")}
	coordinator := buildCoordinator(t, journal, staging, &fakeRemote{}, &fakeInvalidator{})
	payload := []byte("committed before cleanup")
	result, err := coordinator.Put(ctx, PutRequest{
		OperationID:   "stage-cleanup-failure",
		DriveID:       42,
		Name:          "cleanup.txt",
		ContentLength: int64(len(payload)),
	}, bytes.NewReader(payload))
	if err != nil || result.ProjectionPending {
		t.Fatalf("result=%#v err=%v", result, err)
	}
	if state := mustJournalRecord(t, journal, "stage-cleanup-failure").State; state != StateCleanupPending {
		t.Fatalf("state = %s, want cleanup_pending", state)
	}
}

func TestCoordinatorRemoteCommitStillSucceedsWhenJournalConfirmationTemporarilyFails(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db := openJournalDB(t, filepath.Join(t.TempDir(), "journal.db"))
	if err := EnsureJournalSchema(ctx, db); err != nil {
		t.Fatalf("ensure schema: %v", err)
	}
	base, err := NewSQLiteJournal(db)
	if err != nil {
		t.Fatalf("new journal: %v", err)
	}
	journal := &failingTransitionJournal{Journal: base, failNext: StateRemoteCommitted}
	staging, err := NewDiskStagingStore(DiskStagingConfig{
		Root:              filepath.Join(t.TempDir(), "staging"),
		MaxObjectBytes:    1024,
		MaxAggregateBytes: 2048,
		MaxConcurrent:     1,
	})
	if err != nil {
		t.Fatalf("new staging: %v", err)
	}
	invalidator := &fakeInvalidator{}
	coordinator := buildCoordinator(t, journal, staging, &fakeRemote{}, invalidator)
	payload := []byte("remote is already visible")
	result, err := coordinator.Put(ctx, PutRequest{
		OperationID:   "journal-confirm-fails",
		DriveID:       42,
		Name:          "visible.txt",
		ContentLength: int64(len(payload)),
	}, bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("confirmed commit returned error: %v", err)
	}
	if !result.ProjectionPending || invalidator.calls != 1 || staging.UsedBytes() != 0 {
		t.Fatalf("result=%#v invalidations=%d staged=%d", result, invalidator.calls, staging.UsedBytes())
	}
	if state := mustJournalRecord(t, base, "journal-confirm-fails").State; state != StateCommitting {
		t.Fatalf("durable state = %s, want committing for reconcile", state)
	}
}

func TestCoordinatorRetryOfCompletedOperationIsIdempotent(t *testing.T) {
	t.Parallel()

	remote := &fakeRemote{}
	coordinator, _, _ := newTestCoordinator(t, remote, &fakeInvalidator{})
	request := PutRequest{
		OperationID:   "same-operation",
		DriveID:       42,
		ParentID:      "",
		Name:          "same.txt",
		ContentLength: 4,
	}
	first, err := coordinator.Put(context.Background(), request, bytes.NewReader([]byte("same")))
	if err != nil {
		t.Fatalf("first put: %v", err)
	}
	second, err := coordinator.Put(context.Background(), request, &failOnRead{})
	if err != nil {
		t.Fatalf("idempotent put: %v", err)
	}
	if first != second {
		t.Fatalf("retry result = %#v, want %#v", second, first)
	}
	if remote.uploadCalls != 1 || remote.commitCalls != 1 {
		t.Fatalf("retry performed remote I/O: uploads=%d commits=%d", remote.uploadCalls, remote.commitCalls)
	}
}

func TestCoordinatorMoveCommitsWithAtomicOverwriteMetadataAndExactInvalidation(t *testing.T) {
	t.Parallel()

	remote := &fakeRemote{commitResult: MutationResult{OperationID: "move-1", ObjectID: "source", Revision: 10}}
	invalidator := &fakeInvalidator{}
	coordinator, journal, _ := newTestCoordinator(t, remote, invalidator)
	result, err := coordinator.Move(context.Background(), MoveRequest{
		OperationID:            "move-1",
		DriveID:                42,
		ObjectID:               "source",
		SourceParentID:         "parent-z",
		DestinationParentID:    "parent-a",
		DestinationName:        "renamed.txt",
		ExpectedSourceRevision: 9,
		OverwriteTargetID:      "destination",
		ExpectedTargetRevision: 4,
	})
	if err != nil {
		t.Fatalf("move: %v", err)
	}
	if result.ObjectID != "source" || remote.commitCalls != 1 {
		t.Fatalf("result=%#v commits=%d", result, remote.commitCalls)
	}
	commit := remote.lastCommit
	if commit.Mutation.OverwriteTargetID != "destination" || commit.Mutation.ExpectedTargetRevision != 4 {
		t.Fatalf("commit mutation = %#v", commit.Mutation)
	}
	assertStringsEqual(t, invalidator.last.ParentIDs, []string{"parent-a", "parent-z"})
	assertStringsEqual(t, invalidator.last.ObjectIDs, []string{"destination", "source"})
	if state := mustJournalRecord(t, journal, "move-1").State; state != StateDone {
		t.Fatalf("state = %s, want done", state)
	}
}

func TestCoordinatorDeleteCarriesTrashRetentionToCommit(t *testing.T) {
	t.Parallel()

	remote := &fakeRemote{}
	invalidator := &fakeInvalidator{}
	coordinator, _, _ := newTestCoordinator(t, remote, invalidator)
	retention := 14 * 24 * time.Hour
	_, err := coordinator.Delete(context.Background(), DeleteRequest{
		OperationID:      "delete-1",
		DriveID:          42,
		ObjectID:         "file-1",
		ParentID:         "d:parent",
		ExpectedRevision: 2,
		TrashRetention:   retention,
	})
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	if got := remote.lastCommit.Mutation.TrashRetention; got != retention {
		t.Fatalf("retention = %s, want %s", got, retention)
	}
	assertStringsEqual(t, invalidator.last.ParentIDs, []string{"d:parent"})
	assertStringsEqual(t, invalidator.last.ObjectIDs, []string{"file-1"})
}

func TestCoordinatorRejectsOperationIDReuseForDifferentMutation(t *testing.T) {
	t.Parallel()

	coordinator, _, _ := newTestCoordinator(t, &fakeRemote{}, &fakeInvalidator{})
	first := MoveRequest{
		OperationID:         "reused-operation",
		DriveID:             42,
		ObjectID:            "file-1",
		SourceParentID:      "a",
		DestinationParentID: "b",
		DestinationName:     "first.txt",
	}
	if _, err := coordinator.Move(context.Background(), first); err != nil {
		t.Fatalf("first move: %v", err)
	}
	second := first
	second.DestinationName = "different.txt"
	if _, err := coordinator.Move(context.Background(), second); !errors.Is(err, ErrConflict) {
		t.Fatalf("reused operation error = %v, want ErrConflict", err)
	}
}

func TestCoordinatorLoadsWinnerAfterCrossProcessCreateRace(t *testing.T) {
	t.Parallel()

	request := MoveRequest{
		OperationID:         "create-race",
		DriveID:             42,
		ObjectID:            "file-1",
		SourceParentID:      "a",
		DestinationParentID: "b",
		DestinationName:     "file.txt",
	}
	winner := JournalRecord{
		OperationID: request.OperationID,
		Mutation:    request.mutation(),
		State:       StateDone,
		Result:      &MutationResult{OperationID: request.OperationID, ObjectID: request.ObjectID, Revision: 2},
		CreatedAt:   time.Now().UTC(),
		UpdatedAt:   time.Now().UTC(),
	}
	journal := &createRaceJournal{winner: winner}
	remote := &fakeRemote{}
	coordinator, err := NewCoordinator(CoordinatorConfig{
		Journal:             journal,
		Staging:             &stubStaging{},
		Remote:              remote,
		Invalidator:         &fakeInvalidator{},
		MaxActiveOperations: 1,
	})
	if err != nil {
		t.Fatalf("new coordinator: %v", err)
	}
	result, err := coordinator.Move(context.Background(), request)
	if err != nil {
		t.Fatalf("move: %v", err)
	}
	if result.Revision != 2 || remote.commitCalls != 0 {
		t.Fatalf("result=%#v commits=%d", result, remote.commitCalls)
	}
}

func TestCoordinatorCreateRaceRejectsMissingMismatchedAndFailedWinner(t *testing.T) {
	t.Parallel()

	mutation := Mutation{Kind: MutationMove, DriveID: 42, ObjectID: "file", DestinationName: "file.txt"}
	tests := []struct {
		name    string
		journal Journal
		want    error
	}{
		{name: "winner missing", journal: &fixedGetJournal{}, want: ErrJournalConflict},
		{name: "winner differs", journal: &fixedGetJournal{found: true, record: JournalRecord{Mutation: Mutation{Kind: MutationDelete, DriveID: 42, ObjectID: "file"}}}, want: ErrConflict},
		{name: "read fails", journal: &fixedGetJournal{err: errors.New("database unavailable")}, want: ErrUnavailable},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			coordinator := &Coordinator{journal: test.journal}
			_, _, err := coordinator.loadAfterCreateRace(context.Background(), "operation", mutation)
			if test.want == ErrUnavailable {
				if classifyError(err) != ErrUnavailable {
					t.Fatalf("error = %v, want unavailable classification", err)
				}
				return
			}
			if !errors.Is(err, test.want) {
				t.Fatalf("error = %v, want %v", err, test.want)
			}
		})
	}
}

func TestCoordinatorReturnsInProgressForDurableNonterminalOperation(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	coordinator, journal, _ := newTestCoordinator(t, &fakeRemote{}, &fakeInvalidator{})
	request := MoveRequest{
		OperationID:         "already-running",
		DriveID:             42,
		ObjectID:            "file-1",
		SourceParentID:      "a",
		DestinationParentID: "b",
		DestinationName:     "file.txt",
	}
	at := time.Unix(1_700_000_000, 0).UTC()
	if err := journal.Create(ctx, JournalRecord{
		OperationID: request.OperationID,
		Mutation:    request.mutation(),
		State:       StateStaged,
		CreatedAt:   at,
		UpdatedAt:   at,
	}); err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := coordinator.Move(ctx, request); !errors.Is(err, ErrOperationInProgress) {
		t.Fatalf("move error = %v, want ErrOperationInProgress", err)
	}
}

func TestCoordinatorDefensivelyRejectsCorruptDurableRecords(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	coordinator, journal, _ := newTestCoordinator(t, &fakeRemote{}, &fakeInvalidator{})
	at := time.Unix(1_700_000_000, 0).UTC()
	missingStage := JournalRecord{
		OperationID: "missing-stage",
		Mutation:    Mutation{Kind: MutationPut, DriveID: 42, DestinationName: "file.txt"},
		State:       StateStaged,
		CreatedAt:   at,
		UpdatedAt:   at,
	}
	if err := journal.Create(ctx, missingStage); err != nil {
		t.Fatalf("create missing stage: %v", err)
	}
	if _, err := coordinator.uploadStaged(ctx, missingStage); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing stage error = %v, want ErrNotFound", err)
	}

	missingResult := JournalRecord{
		OperationID: "missing-result",
		Mutation:    Mutation{Kind: MutationMove, DriveID: 42, ObjectID: "file", DestinationName: "file.txt"},
		State:       StateRemoteCommitted,
		CreatedAt:   at,
		UpdatedAt:   at,
	}
	if err := journal.Create(ctx, missingResult); err != nil {
		t.Fatalf("create missing result: %v", err)
	}
	report, err := coordinator.Recover(ctx)
	if err == nil || report.Failed == 0 {
		t.Fatalf("corrupt recovery report=%#v err=%v", report, err)
	}
}

func TestCoordinatorAbortsWhenPersistedStageCannotBeOpened(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	coordinator, journal, staging := newTestCoordinator(t, &fakeRemote{}, &fakeInvalidator{})
	at := time.Unix(1_700_000_000, 0).UTC()
	record := JournalRecord{
		OperationID: "missing-stage-file",
		Mutation:    Mutation{Kind: MutationPut, DriveID: 42, DestinationName: "file.txt", ContentLength: 1},
		State:       StateStaged,
		Staged: &StagedObject{
			Key:           "missing.stage",
			Path:          filepath.Join(staging.Root(), "missing.stage"),
			PlaintextSize: 1,
			StoredSize:    1,
		},
		CreatedAt: at,
		UpdatedAt: at,
	}
	if err := journal.Create(ctx, record); err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := coordinator.uploadStaged(ctx, record); !errors.Is(err, ErrNotFound) {
		t.Fatalf("upload error = %v, want ErrNotFound", err)
	}
	if state := mustJournalRecord(t, journal, record.OperationID).State; state != StateAborted {
		t.Fatalf("state = %s, want aborted", state)
	}
}

func TestCoordinatorAbortsWhenMetadataPreparationCannotBeJournaled(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db := openJournalDB(t, filepath.Join(t.TempDir(), "journal.db"))
	if err := EnsureJournalSchema(ctx, db); err != nil {
		t.Fatalf("ensure schema: %v", err)
	}
	base, err := NewSQLiteJournal(db)
	if err != nil {
		t.Fatalf("new journal: %v", err)
	}
	journal := &failingTransitionJournal{Journal: base, failNext: StateStaged}
	coordinator := buildCoordinator(t, journal, &stubStaging{}, &fakeRemote{}, &fakeInvalidator{})
	_, err = coordinator.Move(ctx, MoveRequest{
		OperationID:         "metadata-prepare-failure",
		DriveID:             42,
		ObjectID:            "file",
		SourceParentID:      "a",
		DestinationParentID: "b",
		DestinationName:     "file.txt",
	})
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("move error = %v, want ErrUnavailable", err)
	}
	if state := mustJournalRecord(t, base, "metadata-prepare-failure").State; state != StateAborted {
		t.Fatalf("state = %s, want aborted", state)
	}
}

func TestCoordinatorAbortsUploadedBodyWhenCommitPreparationCannotBeJournaled(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db := openJournalDB(t, filepath.Join(t.TempDir(), "journal.db"))
	if err := EnsureJournalSchema(ctx, db); err != nil {
		t.Fatalf("ensure schema: %v", err)
	}
	base, err := NewSQLiteJournal(db)
	if err != nil {
		t.Fatalf("new journal: %v", err)
	}
	journal := &failingTransitionJournal{Journal: base, failNext: StateCommitting}
	disk, err := NewDiskStagingStore(DiskStagingConfig{
		Root:              filepath.Join(t.TempDir(), "staging"),
		MaxObjectBytes:    1024,
		MaxAggregateBytes: 2048,
		MaxConcurrent:     1,
	})
	if err != nil {
		t.Fatalf("new staging: %v", err)
	}
	remote := &fakeRemote{}
	coordinator := buildCoordinator(t, journal, disk, remote, &fakeInvalidator{})
	payload := []byte("hidden")
	staged, err := disk.Stage(ctx, plaintextStageRequest("commit-prepare-failure", int64(len(payload)), 0), bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("stage: %v", err)
	}
	at := time.Unix(1_700_000_000, 0).UTC()
	body := &RemoteBody{UploadUUID: "hidden-body", PlaintextSize: int64(len(payload))}
	record := JournalRecord{
		OperationID: "commit-prepare-failure",
		Mutation:    Mutation{Kind: MutationPut, DriveID: 42, DestinationName: "file.txt", ContentLength: int64(len(payload))},
		State:       StateUploaded,
		Staged:      &staged,
		Body:        body,
		CreatedAt:   at,
		UpdatedAt:   at,
	}
	if err := base.Create(ctx, record); err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := coordinator.commitPrepared(ctx, record); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("commit preparation error = %v, want ErrUnavailable", err)
	}
	if remote.discardCalls != 1 || disk.UsedBytes() != 0 {
		t.Fatalf("discard=%d staging=%d", remote.discardCalls, disk.UsedBytes())
	}
}

func TestCoordinatorPersistsCleanupWhenHiddenDiscardFails(t *testing.T) {
	t.Parallel()

	remote := &fakeRemote{commitErr: ErrConflict, discardErr: errors.New("cleanup unavailable")}
	coordinator, journal, _ := newTestCoordinator(t, remote, &fakeInvalidator{})
	payload := []byte("conflict with cleanup failure")
	_, err := coordinator.Put(context.Background(), PutRequest{
		OperationID:   "cleanup-pending",
		DriveID:       42,
		Name:          "cleanup.txt",
		ContentLength: int64(len(payload)),
	}, bytes.NewReader(payload))
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("put error = %v, want ErrConflict", err)
	}
	if state := mustJournalRecord(t, journal, "cleanup-pending").State; state != StateCleanupPending {
		t.Fatalf("state = %s, want cleanup_pending", state)
	}
	remote.mu.Lock()
	remote.discardErr = nil
	remote.commitErr = nil
	remote.mu.Unlock()
	report, err := coordinator.Recover(context.Background())
	if err != nil || report.Aborted != 1 {
		t.Fatalf("cleanup recovery report=%#v err=%v", report, err)
	}
}

func TestCoordinatorRejectsNewWorkAfterDrainAndWaitsForActiveOperation(t *testing.T) {
	t.Parallel()

	remote := &fakeRemote{commitStarted: make(chan struct{}), commitRelease: make(chan struct{})}
	coordinator, _, _ := newTestCoordinator(t, remote, &fakeInvalidator{})
	moveDone := make(chan error, 1)
	go func() {
		_, err := coordinator.Move(context.Background(), MoveRequest{
			OperationID:         "active-move",
			DriveID:             42,
			ObjectID:            "file-1",
			SourceParentID:      "",
			DestinationParentID: "folder",
			DestinationName:     "file.txt",
		})
		moveDone <- err
	}()
	select {
	case <-remote.commitStarted:
	case <-time.After(time.Second):
		t.Fatal("operation did not reach commit")
	}

	drainDone := make(chan error, 1)
	go func() { drainDone <- coordinator.Drain(context.Background()) }()
	for i := 0; i < 100 && coordinator.Status().Accepting; i++ {
		time.Sleep(time.Millisecond)
	}
	if coordinator.Status().Accepting {
		t.Fatal("coordinator did not enter draining state")
	}
	if _, err := coordinator.Mkdir(context.Background(), MkdirRequest{DriveID: 42, ParentID: "", Name: "new"}); !errors.Is(err, ErrDraining) {
		t.Fatalf("new operation error = %v, want ErrDraining", err)
	}
	select {
	case err := <-drainDone:
		t.Fatalf("drain returned before active operation: %v", err)
	case <-time.After(20 * time.Millisecond):
	}
	close(remote.commitRelease)
	if err := <-moveDone; err != nil {
		t.Fatalf("move: %v", err)
	}
	if err := <-drainDone; err != nil {
		t.Fatalf("drain: %v", err)
	}
	status := coordinator.Status()
	if status.Accepting || status.Active != 0 {
		t.Fatalf("status after drain = %#v", status)
	}
}

func TestCoordinatorCapabilitiesDeclareNarrowWritableBeta(t *testing.T) {
	t.Parallel()

	coordinator, _, _ := newTestCoordinator(t, &fakeRemote{}, &fakeInvalidator{})
	got := coordinator.Capabilities()
	want := Capabilities{Writable: true, PersonalOnly: true, PlaintextOnly: false, OnlineOnly: true}
	if got != want {
		t.Fatalf("capabilities = %#v, want %#v", got, want)
	}
}

func TestCoordinatorConstructorAndCloseValidation(t *testing.T) {
	t.Parallel()

	valid := CoordinatorConfig{
		Journal:             &stubJournal{},
		Staging:             &stubStaging{},
		Remote:              &fakeRemote{},
		Invalidator:         SnapshotInvalidatorFunc(func(context.Context, SnapshotInvalidation) error { return nil }),
		MaxActiveOperations: 1,
	}
	invalidConfigs := []CoordinatorConfig{
		{},
		{Journal: valid.Journal, Staging: valid.Staging, Remote: valid.Remote, Invalidator: valid.Invalidator},
		{Journal: valid.Journal, Staging: valid.Staging, Remote: valid.Remote, Invalidator: valid.Invalidator, MaxActiveOperations: 1, MaxQueuedOperations: -1},
	}
	for _, config := range invalidConfigs {
		if _, err := NewCoordinator(config); !errors.Is(err, ErrInvalidRequest) {
			t.Fatalf("invalid config error = %v, want ErrInvalidRequest", err)
		}
	}
	coordinator, err := NewCoordinator(valid)
	if err != nil {
		t.Fatalf("new coordinator: %v", err)
	}
	if coordinator.operationID("") == "" {
		t.Fatal("default UUID generator returned an empty ID")
	}
	if err := coordinator.Close(context.Background()); err != nil {
		t.Fatalf("close: %v", err)
	}
	if coordinator.Status().Accepting {
		t.Fatal("closed coordinator still accepts work")
	}
}

func TestCoordinatorPublicMethodsRejectNilContextWithoutPanic(t *testing.T) {
	t.Parallel()

	coordinator, _, _ := newTestCoordinator(t, &fakeRemote{}, &fakeInvalidator{})
	payload := []byte("data")
	tests := []struct {
		name string
		call func() error
	}{
		{name: "put", call: func() error {
			_, err := coordinator.Put(nil, PutRequest{DriveID: 42, Name: "file.txt", ContentLength: int64(len(payload))}, bytes.NewReader(payload))
			return err
		}},
		{name: "mkdir", call: func() error {
			_, err := coordinator.Mkdir(nil, MkdirRequest{DriveID: 42, Name: "folder"})
			return err
		}},
		{name: "move", call: func() error {
			_, err := coordinator.Move(nil, MoveRequest{DriveID: 42, ObjectID: "file", DestinationName: "file.txt"})
			return err
		}},
		{name: "delete", call: func() error {
			_, err := coordinator.Delete(nil, DeleteRequest{DriveID: 42, ObjectID: "file"})
			return err
		}},
		{name: "recover", call: func() error {
			_, err := coordinator.Recover(nil)
			return err
		}},
		{name: "drain", call: func() error { return coordinator.Drain(nil) }},
		{name: "close", call: func() error { return coordinator.Close(nil) }},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			if err := test.call(); !errors.Is(err, ErrInvalidRequest) {
				t.Fatalf("error = %v, want ErrInvalidRequest", err)
			}
		})
	}
}

func TestNilCoordinatorIsSafelyUnavailable(t *testing.T) {
	t.Parallel()

	var coordinator *Coordinator
	if got := coordinator.Status(); got != (Status{}) {
		t.Fatalf("nil status = %#v", got)
	}
	if got := coordinator.Capabilities(); got != (Capabilities{}) {
		t.Fatalf("nil capabilities = %#v", got)
	}
	payload := []byte("data")
	tests := []func() error{
		func() error {
			_, err := coordinator.Put(context.Background(), PutRequest{DriveID: 42, Name: "file.txt", ContentLength: int64(len(payload))}, bytes.NewReader(payload))
			return err
		},
		func() error {
			_, err := coordinator.Mkdir(context.Background(), MkdirRequest{DriveID: 42, Name: "folder"})
			return err
		},
		func() error {
			_, err := coordinator.Move(context.Background(), MoveRequest{DriveID: 42, ObjectID: "file", DestinationName: "file.txt"})
			return err
		},
		func() error {
			_, err := coordinator.Delete(context.Background(), DeleteRequest{DriveID: 42, ObjectID: "file"})
			return err
		},
		func() error {
			_, err := coordinator.Recover(context.Background())
			return err
		},
		func() error { return coordinator.Drain(context.Background()) },
		func() error { return coordinator.Close(context.Background()) },
	}
	for index, call := range tests {
		if err := call(); !errors.Is(err, ErrInvalidRequest) {
			t.Fatalf("call %d error = %v, want ErrInvalidRequest", index, err)
		}
	}
}

func TestCoordinatorBoundsExecutingAndQueuedOperations(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db := openJournalDB(t, filepath.Join(t.TempDir(), "journal.db"))
	if err := EnsureJournalSchema(ctx, db); err != nil {
		t.Fatalf("ensure schema: %v", err)
	}
	journal, err := NewSQLiteJournal(db)
	if err != nil {
		t.Fatalf("new journal: %v", err)
	}
	staging, err := NewDiskStagingStore(DiskStagingConfig{
		Root:              filepath.Join(t.TempDir(), "staging"),
		MaxObjectBytes:    1024,
		MaxAggregateBytes: 4096,
		MaxConcurrent:     1,
	})
	if err != nil {
		t.Fatalf("new staging: %v", err)
	}
	remote := newConcurrencyRemote()
	coordinator, err := NewCoordinator(CoordinatorConfig{
		Journal:             journal,
		Staging:             staging,
		Remote:              remote,
		Invalidator:         &fakeInvalidator{},
		IDGenerator:         &sequenceIDGenerator{},
		MaxActiveOperations: 2,
		MaxQueuedOperations: 3,
		Now:                 time.Now,
	})
	if err != nil {
		t.Fatalf("new coordinator: %v", err)
	}

	results := make(chan error, 5)
	startMove := func(index int) {
		go func() {
			_, moveErr := coordinator.Move(ctx, MoveRequest{
				OperationID:         fmt.Sprintf("bounded-%d", index),
				DriveID:             42,
				ObjectID:            fmt.Sprintf("object-%d", index),
				SourceParentID:      "source",
				DestinationParentID: "destination",
				DestinationName:     fmt.Sprintf("file-%d.txt", index),
			})
			results <- moveErr
		}()
	}
	startMove(0)
	startMove(1)
	remote.waitForEntered(t, 2)
	startMove(2)
	startMove(3)
	startMove(4)
	waitForStatus(t, coordinator, Status{Accepting: true, Active: 5, Executing: 2, Queued: 3})

	_, err = coordinator.Move(ctx, MoveRequest{
		OperationID:         "rejected-sixth",
		DriveID:             42,
		ObjectID:            "object-sixth",
		SourceParentID:      "source",
		DestinationParentID: "destination",
		DestinationName:     "sixth.txt",
	})
	if !errors.Is(err, ErrBusy) {
		t.Fatalf("sixth operation error = %v, want ErrBusy", err)
	}
	remote.releaseAll()
	for range 5 {
		if err := <-results; err != nil {
			t.Fatalf("admitted operation: %v", err)
		}
	}
	if remote.maxConcurrent() != 2 || remote.commitCount() != 5 {
		t.Fatalf("remote max=%d commits=%d, want max=2 commits=5", remote.maxConcurrent(), remote.commitCount())
	}
	waitForStatus(t, coordinator, Status{Accepting: true})
}

func newTestCoordinator(t *testing.T, remote Remote, invalidator SnapshotInvalidator) (*Coordinator, Journal, *DiskStagingStore) {
	t.Helper()
	ctx := context.Background()
	db := openJournalDB(t, filepath.Join(t.TempDir(), "journal.db"))
	if err := EnsureJournalSchema(ctx, db); err != nil {
		t.Fatalf("ensure schema: %v", err)
	}
	journal, err := NewSQLiteJournal(db)
	if err != nil {
		t.Fatalf("new journal: %v", err)
	}
	staging, err := NewDiskStagingStore(DiskStagingConfig{
		Root:              filepath.Join(t.TempDir(), "staging"),
		MaxObjectBytes:    1 << 20,
		MaxAggregateBytes: 2 << 20,
		MaxConcurrent:     2,
	})
	if err != nil {
		t.Fatalf("new staging: %v", err)
	}
	coordinator, err := NewCoordinator(CoordinatorConfig{
		Journal:             journal,
		Staging:             staging,
		Remote:              remote,
		Invalidator:         invalidator,
		IDGenerator:         &sequenceIDGenerator{},
		MaxActiveOperations: 4,
		Now:                 func() time.Time { return time.Unix(1_700_000_000, 0).UTC() },
	})
	if err != nil {
		t.Fatalf("new coordinator: %v", err)
	}
	return coordinator, journal, staging
}

func mustJournalRecord(t *testing.T, journal Journal, operationID string) JournalRecord {
	t.Helper()
	record, found, err := journal.Get(context.Background(), operationID)
	if err != nil || !found {
		t.Fatalf("journal get %q: found=%v err=%v", operationID, found, err)
	}
	return record
}

type fakeRemote struct {
	mu                sync.Mutex
	events            *eventLog
	uploadErr         error
	commitErr         error
	reconcileErr      error
	reconcileFound    bool
	commitResult      MutationResult
	reconcileResult   MutationResult
	receiptResult     MutationResult
	uploadCalls       int
	commitCalls       int
	reconcileCalls    int
	receiptCalls      int
	receiptFound      bool
	lastReceipt       string
	discardCalls      int
	discardErr        error
	lastUpload        HiddenUpload
	lastCommit        CommitRequest
	commitStarted     chan struct{}
	commitRelease     chan struct{}
	commitAcceptedRef string
	commitAcceptedErr error
}

type concurrencyRemote struct {
	mu         sync.Mutex
	release    chan struct{}
	entered    chan struct{}
	current    int
	maximum    int
	commits    int
	releaseOne sync.Once
}

func newConcurrencyRemote() *concurrencyRemote {
	return &concurrencyRemote{release: make(chan struct{}), entered: make(chan struct{}, 16)}
}

func (r *concurrencyRemote) UploadHidden(context.Context, HiddenUpload, io.ReadSeeker) (RemoteBody, error) {
	return RemoteBody{}, errors.New("unexpected upload")
}

func (r *concurrencyRemote) RecoverHidden(context.Context, HiddenUpload, io.ReadSeeker) (RemoteBody, error) {
	return RemoteBody{}, errors.New("unexpected hidden recovery")
}

func (r *concurrencyRemote) Commit(_ context.Context, request CommitRequest) (MutationResult, error) {
	r.mu.Lock()
	r.current++
	r.commits++
	if r.current > r.maximum {
		r.maximum = r.current
	}
	r.mu.Unlock()
	r.entered <- struct{}{}
	<-r.release
	r.mu.Lock()
	r.current--
	r.mu.Unlock()
	return MutationResult{OperationID: request.OperationID, ObjectID: request.Mutation.ObjectID, Revision: 1}, nil
}

func (r *concurrencyRemote) Reconcile(context.Context, string) (MutationResult, bool, error) {
	return MutationResult{}, false, nil
}

func (r *concurrencyRemote) DiscardHidden(context.Context, string, *RemoteBody) error {
	return nil
}

func (r *concurrencyRemote) waitForEntered(t *testing.T, count int) {
	t.Helper()
	for range count {
		select {
		case <-r.entered:
		case <-time.After(time.Second):
			t.Fatal("remote operation did not enter")
		}
	}
}

func (r *concurrencyRemote) releaseAll() {
	r.releaseOne.Do(func() { close(r.release) })
}

func (r *concurrencyRemote) maxConcurrent() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.maximum
}

func (r *concurrencyRemote) commitCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.commits
}

func waitForStatus(t *testing.T, coordinator *Coordinator, want Status) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if got := coordinator.Status(); got == want {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("status = %#v, want %#v", coordinator.Status(), want)
}

func (r *fakeRemote) UploadHidden(_ context.Context, request HiddenUpload, source io.ReadSeeker) (RemoteBody, error) {
	r.mu.Lock()
	r.uploadCalls++
	r.lastUpload = request
	err := r.uploadErr
	r.mu.Unlock()
	if r.events != nil {
		r.events.add("upload")
	}
	if err != nil {
		return RemoteBody{}, err
	}
	payload, err := io.ReadAll(source)
	if err != nil {
		return RemoteBody{}, err
	}
	if int64(len(payload)) != request.StoredSize {
		return RemoteBody{}, errors.New("source size differs")
	}
	return RemoteBody{
		ContentRef:        "hidden-body-1",
		PlaintextSize:     request.PlaintextSize,
		StoredSize:        request.StoredSize,
		Encrypted:         request.Encrypted,
		EncryptionVersion: request.EncryptionVersion,
		SHA256:            request.SHA256,
		StoredSHA256:      request.StoredSHA256,
	}, nil
}

func (r *fakeRemote) RecoverHidden(_ context.Context, request HiddenUpload, source io.ReadSeeker) (RemoteBody, error) {
	payload, err := io.ReadAll(source)
	if err != nil {
		return RemoteBody{}, err
	}
	if int64(len(payload)) != request.StoredSize {
		return RemoteBody{}, errors.New("recovery source size differs")
	}
	return RemoteBody{
		UploadUUID:        "recovered-" + request.OperationID,
		PartCount:         1,
		PlaintextSize:     request.PlaintextSize,
		StoredSize:        request.StoredSize,
		Encrypted:         request.Encrypted,
		EncryptionVersion: request.EncryptionVersion,
		SHA256:            request.SHA256,
		StoredSHA256:      request.StoredSHA256,
		MessageIDs:        []int64{700},
	}, nil
}

func (r *fakeRemote) Commit(_ context.Context, request CommitRequest) (MutationResult, error) {
	r.mu.Lock()
	r.commitCalls++
	r.lastCommit = request
	err := r.commitErr
	result := r.commitResult
	started := r.commitStarted
	release := r.commitRelease
	acceptedRef := r.commitAcceptedRef
	r.mu.Unlock()
	if r.events != nil {
		r.events.add("commit")
	}
	if acceptedRef != "" {
		if request.PersistCommitRef == nil {
			return MutationResult{}, errors.New("missing commit receipt callback")
		}
		if persistErr := request.PersistCommitRef(acceptedRef); persistErr != nil {
			r.mu.Lock()
			r.commitAcceptedErr = persistErr
			r.mu.Unlock()
			return MutationResult{CommitRef: acceptedRef}, ErrCommitOutcomeUnknown
		}
	}
	if started != nil {
		close(started)
	}
	if release != nil {
		<-release
	}
	if err != nil {
		return result, err
	}
	if result.OperationID == "" {
		result = MutationResult{
			OperationID: request.OperationID,
			ObjectID:    firstNonEmpty(request.Mutation.ObjectID, "object-1"),
			Revision:    request.Mutation.ExpectedRevision + 1,
			Created:     request.Mutation.ObjectID == "",
		}
	}
	if request.Body != nil {
		result.Size = request.Body.PlaintextSize
		result.SHA256 = request.Body.SHA256
	}
	return result, nil
}

func (r *fakeRemote) ReconcileReceipt(_ context.Context, _ CommitRequest, commitRef string) (MutationResult, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.receiptCalls++
	r.lastReceipt = commitRef
	return r.receiptResult, r.receiptFound, r.reconcileErr
}

func (r *fakeRemote) Reconcile(_ context.Context, _ string) (MutationResult, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.reconcileCalls++
	return r.reconcileResult, r.reconcileFound, r.reconcileErr
}

func (r *fakeRemote) DiscardHidden(_ context.Context, _ string, _ *RemoteBody) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.discardCalls++
	return r.discardErr
}

type fakeInvalidator struct {
	mu     sync.Mutex
	events *eventLog
	err    error
	calls  int
	last   SnapshotInvalidation
}

func (i *fakeInvalidator) Invalidate(_ context.Context, invalidation SnapshotInvalidation) error {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.calls++
	i.last = invalidation
	if i.events != nil {
		i.events.add("invalidate")
	}
	return i.err
}

type sequenceIDGenerator struct {
	mu   sync.Mutex
	next int
}

func (g *sequenceIDGenerator) NewID() string {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.next++
	return "generated-" + string(rune('0'+g.next))
}

type eventLog struct {
	mu     sync.Mutex
	events []string
}

func (l *eventLog) add(event string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.events = append(l.events, event)
}

func (l *eventLog) snapshot() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.events...)
}

type failOnRead struct{}

func (*failOnRead) Read([]byte) (int, error) {
	return 0, errors.New("reader must not be consumed")
}

func equalInvalidation(a, b SnapshotInvalidation) bool {
	if a.OperationID != b.OperationID || a.DriveID != b.DriveID {
		return false
	}
	if len(a.ParentIDs) != len(b.ParentIDs) || len(a.ObjectIDs) != len(b.ObjectIDs) {
		return false
	}
	for i := range a.ParentIDs {
		if a.ParentIDs[i] != b.ParentIDs[i] {
			return false
		}
	}
	for i := range a.ObjectIDs {
		if a.ObjectIDs[i] != b.ObjectIDs[i] {
			return false
		}
	}
	return true
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

type stubJournal struct{}

func (*stubJournal) Create(context.Context, JournalRecord) error { return nil }
func (*stubJournal) Get(context.Context, string) (JournalRecord, bool, error) {
	return JournalRecord{}, false, nil
}
func (*stubJournal) Transition(context.Context, string, JournalState, JournalState, JournalPatch) (JournalRecord, error) {
	return JournalRecord{}, nil
}
func (*stubJournal) ListRecoverable(context.Context) ([]JournalRecord, error) { return nil, nil }

type stubStaging struct{}

func (*stubStaging) Stage(context.Context, StageRequest, io.Reader) (StagedObject, error) {
	return StagedObject{}, nil
}
func (*stubStaging) Open(StagedObject) (ReadSeekCloser, error)     { return nil, ErrNotFound }
func (*stubStaging) Remove(context.Context, StagedObject) error    { return nil }
func (*stubStaging) RemoveOperation(context.Context, string) error { return nil }

type failingTransitionJournal struct {
	Journal
	failNext JournalState
}

type failingStaging struct {
	StagingStore
	removeErr error
}

func (s *failingStaging) Remove(ctx context.Context, staged StagedObject) error {
	if s.removeErr != nil {
		return s.removeErr
	}
	return s.StagingStore.Remove(ctx, staged)
}

type createRaceJournal struct {
	mu     sync.Mutex
	gets   int
	winner JournalRecord
}

type fixedGetJournal struct {
	record JournalRecord
	found  bool
	err    error
}

func (*fixedGetJournal) Create(context.Context, JournalRecord) error { return nil }
func (j *fixedGetJournal) Get(context.Context, string) (JournalRecord, bool, error) {
	return j.record, j.found, j.err
}
func (*fixedGetJournal) Transition(context.Context, string, JournalState, JournalState, JournalPatch) (JournalRecord, error) {
	return JournalRecord{}, nil
}
func (*fixedGetJournal) ListRecoverable(context.Context) ([]JournalRecord, error) { return nil, nil }

func (*createRaceJournal) Create(context.Context, JournalRecord) error {
	return ErrOperationExists
}

func (j *createRaceJournal) Get(context.Context, string) (JournalRecord, bool, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.gets++
	if j.gets == 1 {
		return JournalRecord{}, false, nil
	}
	return cloneRecord(j.winner), true, nil
}

func (*createRaceJournal) Transition(context.Context, string, JournalState, JournalState, JournalPatch) (JournalRecord, error) {
	return JournalRecord{}, errors.New("unexpected transition")
}

func (*createRaceJournal) ListRecoverable(context.Context) ([]JournalRecord, error) {
	return nil, nil
}

func (j *failingTransitionJournal) Transition(
	ctx context.Context,
	operationID string,
	expected, next JournalState,
	patch JournalPatch,
) (JournalRecord, error) {
	if next == j.failNext {
		return JournalRecord{}, errors.New("temporary journal failure")
	}
	return j.Journal.Transition(ctx, operationID, expected, next, patch)
}

var _ Remote = (*fakeRemote)(nil)
var _ Remote = (*concurrencyRemote)(nil)
var _ SnapshotInvalidator = (*fakeInvalidator)(nil)
var _ = (*sql.DB)(nil)
