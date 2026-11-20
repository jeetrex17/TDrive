package file

import (
	"context"
	"slices"
	"testing"

	"TDrive/backend/projection"
	"TDrive/backend/tgclient"
)

func TestSourceCopyVerifiesMultipartDestinationInOneRead(t *testing.T) {
	svc, telegram, deps, input := newSharedMultipartSourceCopyFixture(t)
	client := &sourceCopyReadCounter{sourceCopyTelegram: telegram}
	svc.TG = client

	results, err := svc.StartSourceCopies(t.Context(), personalChannelID, 7, "", []SourceCopyInput{input}, deps, projection.NewUploadUUID())
	if err != nil || len(results) != 1 || results[0].Status != sourceCopySaved {
		t.Fatalf("copy = %+v, %v", results, err)
	}
	if len(client.batches) != 1 || !slices.Equal(client.batches[0], []int64{telegram.nextBodyID - 10, telegram.nextBodyID}) {
		t.Fatalf("batch reads = %v, want one batch covering both parts", client.batches)
	}
	if !slices.Equal(client.scalarReads, []int64{results[0].FileID}) {
		t.Fatalf("single-message reads = %v, want only commit %d", client.scalarReads, results[0].FileID)
	}
}

func TestSourceCopyPreservesBatchWithMismatchedDestinationBody(t *testing.T) {
	svc, telegram, deps, input := newSharedMultipartSourceCopyFixture(t)
	client := &sourceCopyReadCounter{sourceCopyTelegram: telegram, changeLastDocument: true}
	svc.TG = client

	results, err := svc.StartSourceCopies(t.Context(), personalChannelID, 7, "", []SourceCopyInput{input}, deps, projection.NewUploadUUID())
	if err != nil || len(results) != 1 || results[0].Status != sourceCopyPending || len(telegram.SentControls()) != 0 || len(telegram.DeletedBatches()) != 0 {
		t.Fatalf("copy with mismatched body = %+v, %v, commits %v, deletions %v",
			results, err, telegram.SentControls(), telegram.DeletedBatches())
	}
}

type sourceCopyReadCounter struct {
	*sourceCopyTelegram
	batches            [][]int64
	scalarReads        []int64
	changeLastDocument bool
}

func (client *sourceCopyReadCounter) GetChannelMessages(ctx context.Context, peer tgclient.InputPeer, ids []int64) (map[int64]tgclient.HistoryMessage, error) {
	if peer.ChannelID == personalChannelID {
		client.batches = append(client.batches, slices.Clone(ids))
	}
	messages, err := client.sourceCopyTelegram.GetChannelMessages(ctx, peer, ids)
	if err != nil {
		return nil, err
	}
	if client.changeLastDocument && peer.ChannelID == personalChannelID && len(ids) > 0 {
		id := ids[len(ids)-1]
		message := messages[id]
		message.DocumentID++
		messages[id] = message
	}
	return messages, nil
}

func (client *sourceCopyReadCounter) GetChannelMessage(ctx context.Context, peer tgclient.InputPeer, id int64) (tgclient.HistoryMessage, error) {
	if peer.ChannelID == personalChannelID {
		client.scalarReads = append(client.scalarReads, id)
	}
	return client.sourceCopyTelegram.GetChannelMessage(ctx, peer, id)
}
