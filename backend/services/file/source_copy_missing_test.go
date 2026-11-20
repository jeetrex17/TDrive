package file

import (
	"context"
	"testing"

	"TDrive/backend/projection"
	"TDrive/backend/tgclient"
)

func TestSourceCopyFailsWhenKnownUncommittedBodyWasDeleted(t *testing.T) {
	for _, sinceProjected := range []bool{false, true} {
		name := "deleted before sync saw it"
		if sinceProjected {
			name = "deleted after sync saw it"
		}
		t.Run(name, func(t *testing.T) {
			svc, telegram, deps := newSourceCopyFixture(t)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			telegram.afterBody = cancel
			results, err := svc.StartSourceCopies(ctx, personalChannelID, 7, "",
				[]SourceCopyInput{sourceCopyTestInput()}, deps, projection.NewUploadUUID())
			if err != nil || len(results) != 1 || results[0].Status != sourceCopyPending || len(telegram.SentControls()) != 0 {
				t.Fatalf("interrupted copy = %+v, %v, commits %v", results, err, telegram.SentControls())
			}
			if sinceProjected {
				if err := deps.Sync(t.Context(), personalChannelID); err != nil {
					t.Fatal(err)
				}
			}
			if err := telegram.DeleteMessages(t.Context(), tgclient.InputPeer{ChannelID: personalChannelID}, []int64{telegram.nextBodyID}); err != nil {
				t.Fatal(err)
			}

			result, err := svc.RetrySourceCopy(t.Context(), personalChannelID, 7, results[0].ID, deps)
			if err != nil || result.Status != sourceCopyFailed || telegram.bodyCalls != 1 || len(telegram.SentControls()) != 0 {
				t.Fatalf("retry with deleted body = %+v, %v, body calls %d, commits %v",
					result, err, telegram.bodyCalls, telegram.SentControls())
			}
		})
	}
}

func TestSourceCopyDoesNotAbandonMissingBodyWithUnknownCommit(t *testing.T) {
	svc, telegram, deps := newSourceCopyFixture(t)
	unknown := &sourceCopyUnknownCommit{sourceCopyTelegram: telegram}
	svc.TG = unknown
	results, err := svc.StartSourceCopies(t.Context(), personalChannelID, 7, "",
		[]SourceCopyInput{sourceCopyTestInput()}, deps, projection.NewUploadUUID())
	if err != nil || len(results) != 1 || results[0].Status != sourceCopyPending || unknown.commitCalls != 1 {
		t.Fatalf("copy with unknown commit = %+v, %v, commit calls %d", results, err, unknown.commitCalls)
	}
	if err := telegram.DeleteMessages(t.Context(), tgclient.InputPeer{ChannelID: personalChannelID}, []int64{telegram.nextBodyID}); err != nil {
		t.Fatal(err)
	}
	retried, err := svc.RetrySourceCopy(t.Context(), personalChannelID, 7, results[0].ID, deps)
	if err != nil || retried.Status != sourceCopyPending || unknown.commitCalls != 1 || len(telegram.DeletedBatches()) != 1 {
		t.Fatalf("copy with deleted body and unknown commit = %+v, %v, commit calls %d, deleted %v",
			retried, err, unknown.commitCalls, telegram.DeletedBatches())
	}
}
