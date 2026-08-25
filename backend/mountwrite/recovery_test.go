package mountwrite

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func TestCoordinatorRecoveryAbortsStagedUploadWithFileBackedSQLite(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "journal.db")
	stageRoot := filepath.Join(t.TempDir(), "staging")
	db := openJournalDB(t, dbPath)
	if err := EnsureJournalSchema(ctx, db); err != nil {
		t.Fatalf("ensure schema: %v", err)
	}
	journal, err := NewSQLiteJournal(db)
	if err != nil {
		t.Fatalf("new journal: %v", err)
	}
	staging, err := NewDiskStagingStore(DiskStagingConfig{
		Root:              stageRoot,
		MaxObjectBytes:    1024,
		MaxAggregateBytes: 2048,
		MaxConcurrent:     1,
	})
	if err != nil {
		t.Fatalf("new staging: %v", err)
	}
	payload := []byte("resume after restart")
	staged, err := staging.Stage(ctx, "recover-staged", int64(len(payload)), 0, bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("seed stage: %v", err)
	}
	createdAt := time.Unix(1_700_000_000, 0).UTC()
	record := JournalRecord{
		OperationID: "recover-staged",
		Mutation: Mutation{
			Kind:                MutationPut,
			DriveID:             42,
			DestinationParentID: "",
			DestinationName:     "recover.txt",
			ContentLength:       int64(len(payload)),
		},
		State:     StateReceiving,
		CreatedAt: createdAt,
		UpdatedAt: createdAt,
	}
	if err := journal.Create(ctx, record); err != nil {
		t.Fatalf("seed journal: %v", err)
	}
	if _, err := journal.Transition(ctx, record.OperationID, StateReceiving, StateStaged, JournalPatch{Staged: &staged, UpdatedAt: createdAt}); err != nil {
		t.Fatalf("seed transition: %v", err)
	}

	remote := &fakeRemote{}
	invalidator := &fakeInvalidator{}
	coordinator := buildCoordinator(t, journal, staging, remote, invalidator)
	report, err := coordinator.Recover(ctx)
	if err != nil {
		t.Fatalf("recover: %v", err)
	}
	if report.Examined != 1 || report.Aborted != 1 || report.Pending != 0 {
		t.Fatalf("report = %#v", report)
	}
	if remote.uploadCalls != 0 || remote.commitCalls != 0 || invalidator.calls != 0 {
		t.Fatalf("recovery calls: upload=%d commit=%d invalidate=%d", remote.uploadCalls, remote.commitCalls, invalidator.calls)
	}
	if state := mustJournalRecord(t, journal, record.OperationID).State; state != StateAborted {
		t.Fatalf("state = %s, want aborted", state)
	}
	if staging.UsedBytes() != 0 {
		t.Fatalf("staging bytes = %d after recovery", staging.UsedBytes())
	}
}

func TestCoordinatorRecoveryRetriesCommittingOperationIdempotently(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	remote := &fakeRemote{
		commitAcceptedRef: "808",
		commitResult: MutationResult{
			OperationID: "recover-commit",
			ObjectID:    "object-remote",
			Revision:    8,
		},
	}
	coordinator, journal, _ := newTestCoordinator(t, remote, &fakeInvalidator{})
	at := time.Unix(1_700_000_000, 0).UTC()
	record := JournalRecord{
		OperationID: "recover-commit",
		Mutation: Mutation{
			Kind:                MutationMove,
			DriveID:             42,
			ObjectID:            "object-remote",
			SourceParentID:      "a",
			DestinationParentID: "b",
			DestinationName:     "renamed.txt",
		},
		State:     StateCommitting,
		CreatedAt: at,
		UpdatedAt: at,
	}
	if err := journal.Create(ctx, record); err != nil {
		t.Fatalf("create: %v", err)
	}
	report, err := coordinator.Recover(ctx)
	if err != nil {
		t.Fatalf("recover: %v", err)
	}
	if report.Completed != 1 || remote.reconcileCalls != 0 || remote.commitCalls != 1 {
		t.Fatalf("report=%#v reconcile=%d commit=%d", report, remote.reconcileCalls, remote.commitCalls)
	}
	if !remote.lastCommit.CommitTime.Equal(at) {
		t.Fatalf("commit time = %v, want persisted %v", remote.lastCommit.CommitTime, at)
	}
}

func TestCoordinatorRecoveryUsesPersistedCommitReceipt(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	remote := &fakeRemote{
		receiptFound: true,
		receiptResult: MutationResult{
			ObjectID: "object-from-receipt",
			Revision: 9,
		},
	}
	coordinator, journal, _ := newTestCoordinator(t, remote, &fakeInvalidator{})
	at := time.Unix(1_700_000_000, 0).UTC()
	result := MutationResult{OperationID: "recover-receipt", CommitRef: "900"}
	record := JournalRecord{
		OperationID: "recover-receipt",
		Mutation: Mutation{
			Kind:                MutationMove,
			DriveID:             42,
			ObjectID:            "object-from-receipt",
			SourceParentID:      "a",
			DestinationParentID: "b",
			DestinationName:     "renamed.txt",
		},
		State:     StateReconciling,
		Result:    &result,
		CreatedAt: at,
		UpdatedAt: at,
	}
	if err := journal.Create(ctx, record); err != nil {
		t.Fatalf("create: %v", err)
	}
	report, err := coordinator.Recover(ctx)
	if err != nil {
		t.Fatalf("recover: %v", err)
	}
	if report.Completed != 1 || remote.receiptCalls != 1 || remote.reconcileCalls != 0 {
		t.Fatalf("report=%#v receipt=%d reconcile=%d", report, remote.receiptCalls, remote.reconcileCalls)
	}
	if remote.lastReceipt != "900" {
		t.Fatalf("receipt = %q, want 900", remote.lastReceipt)
	}
}

func TestCoordinatorRecoveryRejectsCorruptCommitReceiptBeforeRemote(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	remote := &fakeRemote{receiptFound: true}
	coordinator, journal, _ := newTestCoordinator(t, remote, &fakeInvalidator{})
	at := time.Unix(1_700_000_000, 0).UTC()
	result := MutationResult{OperationID: "corrupt-receipt", CommitRef: string(bytes.Repeat([]byte("x"), 257))}
	record := JournalRecord{
		OperationID: "corrupt-receipt",
		Mutation: Mutation{
			Kind: MutationMkdir, DriveID: 42, DestinationName: "Safe",
		},
		State: StateReconciling, Result: &result, CreatedAt: at, UpdatedAt: at,
	}
	if err := journal.Create(ctx, record); err != nil {
		t.Fatalf("create: %v", err)
	}
	report, err := coordinator.Recover(ctx)
	if err != nil || report.Aborted != 1 {
		t.Fatalf("Recover report=%#v error=%v, want terminal abort", report, err)
	}
	if remote.receiptCalls != 0 || remote.reconcileCalls != 0 {
		t.Fatalf("remote calls receipt=%d reconcile=%d", remote.receiptCalls, remote.reconcileCalls)
	}
	if state := mustJournalRecord(t, journal, record.OperationID).State; state != StateAborted {
		t.Fatalf("state = %s, want aborted", state)
	}
}

func TestCoordinatorRecoveryRetriesCleanupAfterReceiptRejection(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	remote := &fakeRemote{
		commitAcceptedRef: "779",
		commitErr:         ErrPreconditionFailed,
		reconcileErr:      ErrPreconditionFailed,
		discardErr:        ErrUnavailable,
	}
	coordinator, journal, _ := newTestCoordinator(t, remote, &fakeInvalidator{})
	payload := []byte("cleanup after rejection")
	_, err := coordinator.Put(ctx, PutRequest{
		OperationID: "receipt-rejected-cleanup", DriveID: 42, Name: "existing.txt",
		ExistingObjectID: "f:12", ExpectedRevision: 4,
		ContentLength: int64(len(payload)),
	}, bytes.NewReader(payload))
	if !errors.Is(err, ErrPreconditionFailed) {
		t.Fatalf("Put error = %v, want ErrPreconditionFailed", err)
	}
	if state := mustJournalRecord(t, journal, "receipt-rejected-cleanup").State; state != StateCleanupPending {
		t.Fatalf("state = %s, want cleanup_pending", state)
	}
	remote.mu.Lock()
	remote.discardErr = nil
	remote.mu.Unlock()
	report, err := coordinator.Recover(ctx)
	if err != nil {
		t.Fatalf("Recover: %v", err)
	}
	if report.Aborted != 1 || remote.discardCalls != 2 {
		t.Fatalf("report=%#v discard=%d", report, remote.discardCalls)
	}
	if state := mustJournalRecord(t, journal, "receipt-rejected-cleanup").State; state != StateAborted {
		t.Fatalf("state = %s, want aborted", state)
	}
}

func TestCoordinatorRecoveryAbortsPersistedReceiptRejection(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	remote := &fakeRemote{reconcileErr: ErrPreconditionFailed}
	coordinator, journal, _ := newTestCoordinator(t, remote, &fakeInvalidator{})
	at := time.Unix(1_700_000_000, 0).UTC()
	body := RemoteBody{UploadUUID: "rejected-u", PartCount: 1, PlaintextSize: 1, MessageIDs: []int64{70}}
	result := MutationResult{OperationID: "persisted-rejection", CommitRef: "810"}
	record := JournalRecord{
		OperationID: "persisted-rejection",
		Mutation: Mutation{
			Kind: MutationPut, DriveID: 42, ObjectID: "f:12",
			DestinationName: "existing.txt", ExpectedRevision: 4,
		},
		State: StateReconciling, Body: &body, Result: &result,
		CreatedAt: at, UpdatedAt: at,
	}
	if err := journal.Create(ctx, record); err != nil {
		t.Fatalf("create: %v", err)
	}
	report, err := coordinator.Recover(ctx)
	if err != nil {
		t.Fatalf("Recover: %v", err)
	}
	if report.Aborted != 1 || remote.receiptCalls != 1 || remote.discardCalls != 1 {
		t.Fatalf("report=%#v receipt=%d discard=%d", report, remote.receiptCalls, remote.discardCalls)
	}
	if state := mustJournalRecord(t, journal, record.OperationID).State; state != StateAborted {
		t.Fatalf("state = %s, want aborted", state)
	}
}

func TestCoordinatorRecoveryRetriesProjectionWithoutRemoteCalls(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	remote := &fakeRemote{}
	invalidator := &fakeInvalidator{}
	coordinator, journal, _ := newTestCoordinator(t, remote, invalidator)
	at := time.Unix(1_700_000_000, 0).UTC()
	result := MutationResult{OperationID: "recover-projection", ObjectID: "file-1", Revision: 3}
	record := JournalRecord{
		OperationID: result.OperationID,
		Mutation: Mutation{
			Kind:                MutationPut,
			DriveID:             42,
			ObjectID:            "file-1",
			DestinationParentID: "",
			DestinationName:     "file.txt",
		},
		State:     StateProjectionPending,
		Result:    &result,
		CreatedAt: at,
		UpdatedAt: at,
	}
	if err := journal.Create(ctx, record); err != nil {
		t.Fatalf("create: %v", err)
	}
	report, err := coordinator.Recover(ctx)
	if err != nil {
		t.Fatalf("recover: %v", err)
	}
	if report.Completed != 1 || invalidator.calls != 1 {
		t.Fatalf("report=%#v invalidations=%d", report, invalidator.calls)
	}
	if remote.uploadCalls != 0 || remote.commitCalls != 0 || remote.reconcileCalls != 0 {
		t.Fatalf("projection recovery touched remote: %#v", remote)
	}
}

func TestCoordinatorRecoveryAbortsNeverStagedReceivingOperation(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	coordinator, journal, _ := newTestCoordinator(t, &fakeRemote{}, &fakeInvalidator{})
	at := time.Unix(1_700_000_000, 0).UTC()
	record := JournalRecord{
		OperationID: "receiving-crash",
		Mutation: Mutation{
			Kind:                MutationPut,
			DriveID:             42,
			DestinationParentID: "",
			DestinationName:     "partial.txt",
		},
		State:     StateReceiving,
		CreatedAt: at,
		UpdatedAt: at,
	}
	if err := journal.Create(ctx, record); err != nil {
		t.Fatalf("create: %v", err)
	}

	report, err := coordinator.Recover(ctx)
	if err != nil {
		t.Fatalf("recover: %v", err)
	}
	if report.Aborted != 1 {
		t.Fatalf("report = %#v", report)
	}
	if state := mustJournalRecord(t, journal, record.OperationID).State; state != StateAborted {
		t.Fatalf("state = %s, want aborted", state)
	}
}

func TestCoordinatorRecoveryRemovesOrphanStageFromReceivingBoundary(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	coordinator, journal, staging := newTestCoordinator(t, &fakeRemote{}, &fakeInvalidator{})
	payload := []byte("staged before journal transition")
	if _, err := staging.Stage(ctx, "receiving-orphan", int64(len(payload)), 0, bytes.NewReader(payload)); err != nil {
		t.Fatalf("seed orphan stage: %v", err)
	}
	at := time.Unix(1_700_000_000, 0).UTC()
	record := JournalRecord{
		OperationID: "receiving-orphan",
		Mutation:    Mutation{Kind: MutationPut, DriveID: 42, DestinationName: "orphan.txt"},
		State:       StateReceiving,
		CreatedAt:   at,
		UpdatedAt:   at,
	}
	if err := journal.Create(ctx, record); err != nil {
		t.Fatalf("create receiving record: %v", err)
	}
	report, err := coordinator.Recover(ctx)
	if err != nil || report.Aborted != 1 {
		t.Fatalf("report=%#v err=%v", report, err)
	}
	if staging.UsedBytes() != 0 {
		t.Fatalf("orphan retained %d staging bytes", staging.UsedBytes())
	}
}

func TestCoordinatorRecoveryResumesEveryDurableBoundary(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name              string
		state             JournalState
		kind              MutationKind
		withStage         bool
		withBody          bool
		withResult        bool
		wantState         JournalState
		wantUploads       int
		wantCommits       int
		wantInvalidations int
		wantDiscards      int
	}{
		{name: "uploading discards deterministic hidden upload", state: StateUploading, kind: MutationPut, withStage: true, wantState: StateAborted, wantDiscards: 1},
		{name: "uploaded discards persisted hidden body", state: StateUploaded, kind: MutationPut, withStage: true, withBody: true, wantState: StateAborted, wantDiscards: 1},
		{name: "prepared metadata aborts without commit", state: StateStaged, kind: MutationMove, wantState: StateAborted},
		{name: "remote committed projects", state: StateRemoteCommitted, kind: MutationMove, withResult: true, wantState: StateDone, wantInvalidations: 1},
		{name: "committed cleanup removes stage", state: StateCleanupPending, kind: MutationPut, withStage: true, withResult: true, wantState: StateDone},
		{name: "failed cleanup discards hidden body", state: StateCleanupPending, kind: MutationPut, withBody: true, wantState: StateAborted, wantDiscards: 1},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			remote := &fakeRemote{}
			invalidator := &fakeInvalidator{}
			coordinator, journal, staging := newTestCoordinator(t, remote, invalidator)
			at := time.Unix(1_700_000_000, 0).UTC()
			mutation := Mutation{
				Kind:                test.kind,
				DriveID:             42,
				ObjectID:            "object-boundary",
				SourceParentID:      "source",
				DestinationParentID: "destination",
				DestinationName:     "boundary.txt",
			}
			record := JournalRecord{
				OperationID: "boundary-" + string(test.state),
				Mutation:    mutation,
				State:       test.state,
				CreatedAt:   at,
				UpdatedAt:   at,
			}
			if test.withStage {
				payload := []byte("recovery boundary")
				staged, err := staging.Stage(ctx, record.OperationID, int64(len(payload)), 0, bytes.NewReader(payload))
				if err != nil {
					t.Fatalf("stage: %v", err)
				}
				record.Staged = &staged
			}
			if test.withBody {
				record.Body = &RemoteBody{UploadUUID: "upload-boundary", PlaintextSize: 17}
			}
			if test.withResult {
				record.Result = &MutationResult{OperationID: record.OperationID, ObjectID: mutation.ObjectID, Revision: 2}
			}
			if err := journal.Create(ctx, record); err != nil {
				t.Fatalf("create: %v", err)
			}
			report, err := coordinator.Recover(ctx)
			if err != nil {
				t.Fatalf("recover: %v", err)
			}
			if report.Examined != 1 {
				t.Fatalf("report = %#v", report)
			}
			if state := mustJournalRecord(t, journal, record.OperationID).State; state != test.wantState {
				t.Fatalf("state = %s, want %s", state, test.wantState)
			}
			if remote.uploadCalls != test.wantUploads || remote.commitCalls != test.wantCommits ||
				invalidator.calls != test.wantInvalidations || remote.discardCalls != test.wantDiscards {
				t.Fatalf("calls upload=%d commit=%d invalidate=%d discard=%d", remote.uploadCalls, remote.commitCalls, invalidator.calls, remote.discardCalls)
			}
		})
	}
}

func TestCoordinatorRecoveryLeavesUnconfirmedCommitPending(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	remote := &fakeRemote{reconcileFound: false}
	coordinator, journal, _ := newTestCoordinator(t, remote, &fakeInvalidator{})
	at := time.Unix(1_700_000_000, 0).UTC()
	record := JournalRecord{
		OperationID: "still-uncertain",
		Mutation: Mutation{
			Kind:                MutationMove,
			DriveID:             42,
			ObjectID:            "file-1",
			SourceParentID:      "a",
			DestinationParentID: "b",
			DestinationName:     "file.txt",
		},
		State:     StateReconciling,
		CreatedAt: at,
		UpdatedAt: at,
	}
	if err := journal.Create(ctx, record); err != nil {
		t.Fatalf("create: %v", err)
	}
	report, err := coordinator.Recover(ctx)
	if err != nil {
		t.Fatalf("recover: %v", err)
	}
	if report.Pending != 1 || remote.reconcileCalls != 1 || remote.commitCalls != 0 {
		t.Fatalf("report=%#v reconcile=%d commit=%d", report, remote.reconcileCalls, remote.commitCalls)
	}
}

func buildCoordinator(t *testing.T, journal Journal, staging StagingStore, remote Remote, invalidator SnapshotInvalidator) *Coordinator {
	t.Helper()
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
	return coordinator
}
