package mountwrite

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

func TestSQLiteJournalPersistsBoundedHardDeletePlanAcrossReopen(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "journal.db")
	db := openJournalDB(t, dbPath)
	if err := EnsureJournalSchema(ctx, db); err != nil {
		t.Fatalf("ensure schema: %v", err)
	}
	journal, err := NewSQLiteJournal(db)
	if err != nil {
		t.Fatalf("new journal: %v", err)
	}
	now := time.Unix(1_700_000_000, 0).UTC()
	if err := journal.Create(ctx, JournalRecord{
		OperationID: "hard-delete-plan",
		Mutation:    Mutation{Kind: MutationHardDelete, DriveID: 42, ObjectID: "folder-1", ExpectedRevision: 1, Recursive: true},
		State:       StateDeletePlanPending,
		CreatedAt:   now,
		UpdatedAt:   now,
	}); err != nil {
		t.Fatalf("create record: %v", err)
	}
	if err := journal.AppendHardDeletePlan(ctx, "hard-delete-plan", []int64{10, 20}, 3, false); err != nil {
		t.Fatalf("append first page: %v", err)
	}
	if err := journal.AppendHardDeletePlan(ctx, "hard-delete-plan", []int64{10, 20}, 3, false); err != nil {
		t.Fatalf("repeat first page idempotently: %v", err)
	}
	if err := journal.AppendHardDeletePlan(ctx, "hard-delete-plan", []int64{30}, 3, true); err != nil {
		t.Fatalf("seal plan: %v", err)
	}
	status, found, err := journal.HardDeletePlanStatus(ctx, "hard-delete-plan")
	if err != nil || !found {
		t.Fatalf("status: found=%v err=%v", found, err)
	}
	if status != (HardDeletePlanStatus{ExpectedCount: 3, PlannedCount: 3, CompletedCount: 0, Cursor: 30, Sealed: true}) {
		t.Fatalf("status = %#v", status)
	}
	batch, err := journal.NextHardDeleteBatch(ctx, "hard-delete-plan", 2)
	if err != nil || !slices.Equal(batch, []int64{10, 20}) {
		t.Fatalf("next batch = %v, err=%v", batch, err)
	}
	if err := journal.MarkHardDeleteBatchDone(ctx, "hard-delete-plan", batch); err != nil {
		t.Fatalf("mark batch done: %v", err)
	}

	if err := db.Close(); err != nil {
		t.Fatalf("close database: %v", err)
	}
	db = openJournalDB(t, dbPath)
	journal, err = NewSQLiteJournal(db)
	if err != nil {
		t.Fatalf("reopen journal: %v", err)
	}
	batch, err = journal.NextHardDeleteBatch(ctx, "hard-delete-plan", 100)
	if err != nil || !slices.Equal(batch, []int64{30}) {
		t.Fatalf("remaining batch = %v, err=%v", batch, err)
	}
	status, found, err = journal.HardDeletePlanStatus(ctx, "hard-delete-plan")
	if err != nil || !found || status.CompletedCount != 2 {
		t.Fatalf("reopened status = %#v, found=%v err=%v", status, found, err)
	}
	if err := journal.MarkHardDeleteBatchDone(ctx, "hard-delete-plan", batch); err != nil {
		t.Fatalf("mark final batch done: %v", err)
	}
	if _, err := journal.Transition(ctx, "hard-delete-plan", StateDeletePlanPending, StateDeletingBodies, JournalPatch{}); err != nil {
		t.Fatalf("transition to deleting: %v", err)
	}
	if _, err := journal.Transition(ctx, "hard-delete-plan", StateDeletingBodies, StateDeleteFinalizing, JournalPatch{}); err != nil {
		t.Fatalf("transition to finalizing: %v", err)
	}
	if err := journal.CompactHardDeletePlan(ctx, "hard-delete-plan"); err != nil {
		t.Fatalf("compact plan: %v", err)
	}
	if err := journal.CompactHardDeletePlan(ctx, "hard-delete-plan"); err != nil {
		t.Fatalf("repeat plan compaction after crash: %v", err)
	}
	if _, found, err := journal.HardDeletePlanStatus(ctx, "hard-delete-plan"); err != nil || found {
		t.Fatalf("compacted status: found=%v err=%v", found, err)
	}
}

func TestSQLiteJournalRejectsMalformedOrInconsistentHardDeletePlans(t *testing.T) {
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
	now := time.Unix(1_700_000_000, 0).UTC()
	if err := journal.Create(ctx, JournalRecord{
		OperationID: "hard-delete-invalid-plan",
		Mutation:    Mutation{Kind: MutationHardDelete, DriveID: 42, ObjectID: "file-1", ExpectedRevision: 1, Recursive: true},
		State:       StateDeletePlanPending,
		CreatedAt:   now,
		UpdatedAt:   now,
	}); err != nil {
		t.Fatalf("create record: %v", err)
	}
	invalidPages := [][]int64{{0}, {2, 1}, {1, 1}}
	for _, page := range invalidPages {
		if err := journal.AppendHardDeletePlan(ctx, "hard-delete-invalid-plan", page, 2, false); !errors.Is(err, ErrInvalidRequest) {
			t.Fatalf("page %v error = %v, want ErrInvalidRequest", page, err)
		}
	}
	if err := journal.AppendHardDeletePlan(ctx, "hard-delete-invalid-plan", []int64{1}, 2, false); err != nil {
		t.Fatalf("append valid page: %v", err)
	}
	if err := journal.AppendHardDeletePlan(ctx, "hard-delete-invalid-plan", []int64{2}, 3, true); !errors.Is(err, ErrConflict) {
		t.Fatalf("inconsistent total error = %v, want ErrConflict", err)
	}
	if err := journal.AppendHardDeletePlan(ctx, "hard-delete-invalid-plan", []int64{2}, 2, false); err != nil {
		t.Fatalf("append second page: %v", err)
	}
	if err := journal.AppendHardDeletePlan(ctx, "hard-delete-invalid-plan", nil, 2, true); err != nil {
		t.Fatalf("seal exact plan: %v", err)
	}
	if err := journal.AppendHardDeletePlan(ctx, "hard-delete-invalid-plan", []int64{3}, 2, true); !errors.Is(err, ErrConflict) {
		t.Fatalf("append after seal error = %v, want ErrConflict", err)
	}
	if err := journal.MarkHardDeleteBatchDone(ctx, "hard-delete-invalid-plan", []int64{99}); !errors.Is(err, ErrConflict) {
		t.Fatalf("unknown completion error = %v, want ErrConflict", err)
	}
}
