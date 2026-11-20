package file

import (
	"context"
	"slices"
	"testing"

	"TDrive/backend/channelsource"
	"TDrive/backend/projection"
	"TDrive/backend/tgclient"
)

func TestSourceCopyResolvesOnceAndJournalsBeforeSend(t *testing.T) {
	svc, telegram, deps := newSourceCopyFixture(t)
	batchID := projection.NewUploadUUID()
	resolve := deps.Resolve
	sourceReads := 0
	deps.Resolve = func(ctx context.Context, source channelsource.CopySource) (tgclient.DocumentRef, error) {
		sourceReads++
		return resolve(ctx, source)
	}
	telegram.beforeBody = func(ref tgclient.DocumentRef, caption string) {
		receipts, err := svc.ListSourceCopyBatch(t.Context(), personalChannelID, 7, batchID)
		if err != nil || len(receipts) != 1 {
			t.Fatalf("before send, durable receipts = %+v, %v", receipts, err)
		}
		job, err := svc.loadSourceCopyJob(t.Context(), personalChannelID, 7, receipts[0].ID)
		if err != nil || len(job.Parts) != 1 || job.Parts[0].DocumentID != ref.DocumentID ||
			job.Parts[0].Size != ref.Size || job.Parts[0].BodyID != 0 || job.bodyHeader(0) != caption {
			t.Fatalf("before send, durable intent = %+v, %v", job.Parts, err)
		}
	}
	results, err := svc.StartSourceCopies(t.Context(), personalChannelID, 7, "", []SourceCopyInput{sourceCopyTestInput()}, deps, batchID)
	if err != nil || len(results) != 1 || results[0].Status != sourceCopySaved || telegram.bodyCalls != 1 {
		t.Fatalf("copy = %+v, %v, %d sends", results, err, telegram.bodyCalls)
	}
	if sourceReads != 1 {
		t.Fatalf("source document reads = %d, want one for the send", sourceReads)
	}
}

func TestSourceCopyReadsRawSharedDocumentOnlyOnce(t *testing.T) {
	svc, telegram, deps, input := newSharedMultipartSourceCopyFixture(t)
	raw := sharedCopyMessage(31, 99, 12)
	telegram.SeedHistory(raw)
	input.Source.MsgID = raw.MsgID
	input.Source.Generation = "0"
	input.Source.Identity = tgclient.SharedRawCopyIdentity(raw)
	client := &sourceCopyDocumentReadCounter{sourceCopyTelegram: telegram}
	svc.TG = client

	results, err := svc.StartSourceCopies(t.Context(), personalChannelID, 7, "", []SourceCopyInput{input}, deps, projection.NewUploadUUID())
	if err != nil || len(results) != 1 || results[0].Status != sourceCopySaved || results[0].Name != "member.mov" {
		t.Fatalf("copy = %+v, %v", results, err)
	}
	if !slices.Equal(client.sharedReads, []int64{raw.MsgID}) {
		t.Fatalf("source document reads = %v, want one for message %d", client.sharedReads, raw.MsgID)
	}
}

type sourceCopyDocumentReadCounter struct {
	*sourceCopyTelegram
	sharedReads []int64
}

func (client *sourceCopyDocumentReadCounter) ResolveMessageDocument(ctx context.Context, peer tgclient.InputPeer, id int64) (tgclient.HistoryMessage, tgclient.DocumentRef, error) {
	if peer.ChannelID == sharedChannelID {
		client.sharedReads = append(client.sharedReads, id)
	}
	return client.sourceCopyTelegram.ResolveMessageDocument(ctx, peer, id)
}
