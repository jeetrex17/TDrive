package file

import (
	"errors"
	"testing"

	"TDrive/backend/projection"
)

func TestSourceCopyBatchRecoversOnlyItsReceipts(t *testing.T) {
	svc, telegram, deps := newSourceCopyFixture(t)
	input := []SourceCopyInput{sourceCopyTestInput()}
	before, current := projection.NewUploadUUID(), projection.NewUploadUUID()

	earlier, err := svc.StartSourceCopies(t.Context(), personalChannelID, 7, "", input, deps, before)
	if err != nil || len(earlier) != 1 || earlier[0].Status != sourceCopySaved {
		t.Fatalf("earlier copy = %+v, %v", earlier, err)
	}
	saved, err := svc.StartSourceCopies(t.Context(), personalChannelID, 7, "", input, deps, current)
	if err != nil || len(saved) != 1 || saved[0].Status != sourceCopySaved {
		t.Fatalf("current copy = %+v, %v", saved, err)
	}

	if _, err := svc.StartSourceCopies(t.Context(), personalChannelID, 7, "", input, deps, current); err == nil {
		t.Fatal("resubmitting the current batch should be rejected")
	}
	if telegram.bodyCalls != 2 {
		t.Fatalf("body sends = %d, want two across both batches", telegram.bodyCalls)
	}
	receipts, err := svc.ListSourceCopyBatch(t.Context(), personalChannelID, 7, current)
	if err != nil || len(receipts) != 1 || receipts[0] != saved[0] {
		t.Fatalf("current batch receipts = %+v, %v", receipts, err)
	}
	if _, err := svc.ListSourceCopyBatch(t.Context(), personalChannelID, 8, current); err == nil {
		t.Fatal("another account could read the batch")
	}
}

func TestSourceCopyPreflightFailureDoesNotRecordReceipts(t *testing.T) {
	svc, telegram, deps := newSourceCopyFixture(t)
	batchID := projection.NewUploadUUID()
	_, err := svc.StartSourceCopies(t.Context(), personalChannelID, 7, "d:missing", []SourceCopyInput{sourceCopyTestInput()}, deps, batchID)
	if _, ok := errors.AsType[SourceCopyPreflightError](err); !ok {
		t.Fatalf("destination validation returned %v, want preflight error", err)
	}
	receipts, err := svc.ListSourceCopyBatch(t.Context(), personalChannelID, 7, batchID)
	if err != nil || len(receipts) != 0 || telegram.bodyCalls != 0 {
		t.Fatalf("rejected batch receipts = %+v, %v, body sends = %d", receipts, err, telegram.bodyCalls)
	}
}
