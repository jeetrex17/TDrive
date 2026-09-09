package mountwrite

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"sync"
	"testing"
)

func TestHardDeleteRequestUsesDistinctRetentionFreeMutation(t *testing.T) {
	t.Parallel()

	request := HardDeleteRequest{
		OperationID:      "hard-delete-1",
		DriveID:          42,
		ObjectID:         "folder-1",
		ParentID:         "parent-1",
		ExpectedRevision: 7,
	}
	if err := request.Validate(); err != nil {
		t.Fatalf("validate hard delete: %v", err)
	}
	mutation := request.mutation()
	if mutation.Kind != MutationHardDelete || mutation.TrashRetention != 0 || !mutation.Recursive {
		t.Fatalf("hard delete mutation = %#v", mutation)
	}

	invalid := []HardDeleteRequest{
		{DriveID: 0, ObjectID: "file-1", ExpectedRevision: 1},
		{DriveID: 42},
		{DriveID: 42, ObjectID: "file-1"},
		{OperationID: "bad\noperation", DriveID: 42, ObjectID: "file-1", ExpectedRevision: 1},
	}
	for _, candidate := range invalid {
		if err := candidate.Validate(); !errors.Is(err, ErrInvalidRequest) {
			t.Fatalf("invalid request %#v error = %v", candidate, err)
		}
	}
}

func TestCoordinatorHardDeleteBatchesOnlyAfterSealedDurablePlan(t *testing.T) {
	t.Parallel()

	remote := &fakeHardDeleteRemote{fakeRemote: &fakeRemote{}, plan: sequenceMessageIDs(201)}
	coordinator, journal, _ := newTestCoordinator(t, remote, &fakeInvalidator{})
	result, err := coordinator.HardDelete(context.Background(), HardDeleteRequest{
		OperationID:      "hard-delete-batches",
		DriveID:          42,
		ObjectID:         "folder-1",
		ExpectedRevision: 3,
	})
	if err != nil {
		t.Fatalf("hard delete: %v", err)
	}
	if result.ObjectID != "folder-1" {
		t.Fatalf("result = %#v", result)
	}
	if got := remote.deleted(); len(got) != 3 || len(got[0]) != 100 || len(got[1]) != 100 || len(got[2]) != 1 {
		t.Fatalf("delete batches = %#v", got)
	}
	if remote.finalized() != 1 {
		t.Fatalf("finalize calls = %d, want 1", remote.finalized())
	}
	if state := mustJournalRecord(t, journal, "hard-delete-batches").State; state != StateDone {
		t.Fatalf("state = %s, want done", state)
	}
	planJournal := journal.(HardDeleteJournal)
	_, found, err := planJournal.HardDeletePlanStatus(context.Background(), "hard-delete-batches")
	if err != nil || found {
		t.Fatalf("completed plan retained hot rows: found=%v err=%v", found, err)
	}
}

func TestCoordinatorHardDeleteRecoveryResumesUnresolvedBodies(t *testing.T) {
	t.Parallel()

	remote := &fakeHardDeleteRemote{
		fakeRemote:   &fakeRemote{},
		plan:         sequenceMessageIDs(201),
		failDeleteAt: 2,
	}
	coordinator, journal, _ := newTestCoordinator(t, remote, &fakeInvalidator{})
	_, err := coordinator.HardDelete(context.Background(), HardDeleteRequest{
		OperationID:      "hard-delete-recovery",
		DriveID:          42,
		ObjectID:         "folder-1",
		ExpectedRevision: 1,
	})
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("hard delete error = %v, want ErrUnavailable", err)
	}
	if state := mustJournalRecord(t, journal, "hard-delete-recovery").State; state != StateDeletingBodies {
		t.Fatalf("state after failure = %s, want deleting bodies", state)
	}

	remote.clearDeleteFailure()
	report, err := coordinator.Recover(context.Background())
	if err != nil {
		t.Fatalf("recover: report=%#v err=%v", report, err)
	}
	if state := mustJournalRecord(t, journal, "hard-delete-recovery").State; state != StateDone {
		t.Fatalf("state after recovery = %s, want done", state)
	}
	got := remote.deleted()
	if len(got) != 4 || !slices.Equal(got[0], sequenceMessageIDs(100)) ||
		!slices.Equal(got[1], sequenceMessageIDs(201)[100:200]) ||
		!slices.Equal(got[2], sequenceMessageIDs(201)[100:200]) ||
		!slices.Equal(got[3], []int64{201}) {
		t.Fatalf("delete calls = %#v", got)
	}
	if remote.commitCalls != 1 {
		t.Fatalf("commit calls after recovery = %d, want 1", remote.commitCalls)
	}
}

func TestCoordinatorHardDeleteFailsClosedWhenDurableCommitTransitionFails(t *testing.T) {
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
	journal := &failingHardDeleteJournal{SQLiteJournal: base, failNext: StateRemoteCommitted}
	staging, err := NewDiskStagingStore(DiskStagingConfig{
		Root: filepath.Join(t.TempDir(), "staging"), MaxObjectBytes: 1024,
		MaxAggregateBytes: 2048, MaxConcurrent: 1,
	})
	if err != nil {
		t.Fatalf("new staging: %v", err)
	}
	remote := &fakeHardDeleteRemote{fakeRemote: &fakeRemote{}, plan: []int64{11}}
	coordinator := buildCoordinator(t, journal, staging, remote, &fakeInvalidator{})

	_, err = coordinator.HardDelete(ctx, HardDeleteRequest{
		OperationID: "hard-delete-journal-failure", DriveID: 42,
		ObjectID: "file-1", ExpectedRevision: 1,
	})
	if err == nil {
		t.Fatal("hard delete reported success without a durable remote-committed transition")
	}
	if len(remote.deleted()) != 0 {
		t.Fatalf("body deletion started without durable commit state: %v", remote.deleted())
	}
	if state := mustJournalRecord(t, base, "hard-delete-journal-failure").State; state != StateCommitting {
		t.Fatalf("durable state = %s, want committing for recovery", state)
	}
}

func TestCoordinatorHardDeleteDoesNotReturnSuccessWhileCleanupPending(t *testing.T) {
	t.Parallel()

	remote := &fakeHardDeleteRemote{fakeRemote: &fakeRemote{}, plan: []int64{11}, failAllDeletes: true}
	coordinator, _, _ := newTestCoordinator(t, remote, &fakeInvalidator{})
	request := HardDeleteRequest{OperationID: "hard-delete-pending", DriveID: 42, ObjectID: "file-1", ExpectedRevision: 1}
	if _, err := coordinator.HardDelete(context.Background(), request); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("first hard delete error = %v, want ErrUnavailable", err)
	}
	if _, err := coordinator.HardDelete(context.Background(), request); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("repeated hard delete error = %v, want ErrUnavailable", err)
	}
	if remote.commitCalls != 1 {
		t.Fatalf("commit calls = %d, want one durable marker", remote.commitCalls)
	}
}

type fakeHardDeleteRemote struct {
	*fakeRemote

	deleteMu       sync.Mutex
	plan           []int64
	deleteCalls    [][]int64
	failDeleteAt   int
	failAllDeletes bool
	finalizeCalls  int
}

func (r *fakeHardDeleteRemote) HardDeletePlan(_ context.Context, _ string, afterMsgID int64, limit int) ([]int64, int64, bool, error) {
	start := 0
	for start < len(r.plan) && r.plan[start] <= afterMsgID {
		start++
	}
	end := min(start+limit, len(r.plan))
	page := append([]int64(nil), r.plan[start:end]...)
	return page, int64(len(r.plan)), end == len(r.plan), nil
}

func (r *fakeHardDeleteRemote) DeleteBodies(_ context.Context, _ string, messageIDs []int64) error {
	r.deleteMu.Lock()
	defer r.deleteMu.Unlock()
	r.deleteCalls = append(r.deleteCalls, append([]int64(nil), messageIDs...))
	if r.failAllDeletes || (r.failDeleteAt > 0 && len(r.deleteCalls) == r.failDeleteAt) {
		return errors.New("temporary Telegram delete failure")
	}
	return nil
}

type failingHardDeleteJournal struct {
	*SQLiteJournal
	failNext JournalState
}

func (j *failingHardDeleteJournal) Transition(
	ctx context.Context,
	operationID string,
	expected, next JournalState,
	patch JournalPatch,
) (JournalRecord, error) {
	if next == j.failNext {
		return JournalRecord{}, errors.New("temporary journal failure")
	}
	return j.SQLiteJournal.Transition(ctx, operationID, expected, next, patch)
}

func (r *fakeHardDeleteRemote) FinalizeHardDelete(context.Context, string) error {
	r.deleteMu.Lock()
	defer r.deleteMu.Unlock()
	r.finalizeCalls++
	return nil
}

func (r *fakeHardDeleteRemote) deleted() [][]int64 {
	r.deleteMu.Lock()
	defer r.deleteMu.Unlock()
	result := make([][]int64, len(r.deleteCalls))
	for i := range r.deleteCalls {
		result[i] = append([]int64(nil), r.deleteCalls[i]...)
	}
	return result
}

func (r *fakeHardDeleteRemote) finalized() int {
	r.deleteMu.Lock()
	defer r.deleteMu.Unlock()
	return r.finalizeCalls
}

func (r *fakeHardDeleteRemote) clearDeleteFailure() {
	r.deleteMu.Lock()
	defer r.deleteMu.Unlock()
	r.failDeleteAt = 0
}

func sequenceMessageIDs(count int) []int64 {
	result := make([]int64, count)
	for i := range result {
		result[i] = int64(i + 1)
	}
	return result
}
