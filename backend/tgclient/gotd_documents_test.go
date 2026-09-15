package tgclient

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/gotd/td/tg"
)

func TestDocumentOfClassifiesMessages(t *testing.T) {
	t.Parallel()

	doc := &tg.Document{
		ID:         7,
		DCID:       4,
		Attributes: []tg.DocumentAttributeClass{&tg.DocumentAttributeFilename{FileName: "clip.mkv"}},
	}
	got, name, err := documentOf(&tg.Message{ID: 1, Media: &tg.MessageMediaDocument{Document: doc}})
	if err != nil || got != doc || name != "clip.mkv" {
		t.Fatalf("documentOf(document) = %v, %q, %v", got, name, err)
	}
	if _, _, err := documentOf(&tg.MessageEmpty{ID: 2}); !errors.Is(err, ErrMessageNotFound) {
		t.Fatalf("documentOf(deleted placeholder) err = %v, want %v", err, ErrMessageNotFound)
	}
	if _, _, err := documentOf(&tg.Message{ID: 3}); !errors.Is(err, ErrNotFile) {
		t.Fatalf("documentOf(text) err = %v, want %v", err, ErrNotFile)
	}
	empty := &tg.Message{ID: 4, Media: &tg.MessageMediaDocument{Document: &tg.DocumentEmpty{}}}
	if _, _, err := documentOf(empty); !errors.Is(err, ErrEmptyDocument) {
		t.Fatalf("documentOf(empty document) err = %v, want %v", err, ErrEmptyDocument)
	}
}

// Telegram answers a batch in whatever order it likes; refs must follow the
// requested ids, and the first bad message is named in the error.
func TestDocumentRefsInOrderFollowsRequestedIDs(t *testing.T) {
	t.Parallel()

	peer := InputPeer{ChannelID: 10, AccessHash: 20}
	messages := map[int64]tg.MessageClass{
		5: &tg.Message{ID: 5, Media: &tg.MessageMediaDocument{Document: &tg.Document{ID: 50, DCID: 2, Size: 500, FileReference: []byte{5}}}},
		3: &tg.Message{ID: 3, Media: &tg.MessageMediaDocument{Document: &tg.Document{ID: 30, DCID: 4, Size: 300}}},
	}

	refs, err := documentRefsInOrder(peer, []int64{3, 5}, messages)
	if err != nil {
		t.Fatalf("documentRefsInOrder: %v", err)
	}
	if len(refs) != 2 || refs[0].MsgID != 3 || refs[1].MsgID != 5 {
		t.Fatalf("refs = %+v, want message 3 then 5", refs)
	}
	if refs[0].DCID != 4 || refs[0].Size != 300 || refs[0].Peer != peer {
		t.Fatalf("refs[0] = %+v", refs[0])
	}
	if refs[1].DocumentID != 50 || refs[1].DCID != 2 || string(refs[1].FileReference) != "\x05" {
		t.Fatalf("refs[1] = %+v", refs[1])
	}

	_, err = documentRefsInOrder(peer, []int64{3, 4}, messages)
	if !errors.Is(err, ErrMessageNotFound) || !strings.Contains(err.Error(), "message 4") {
		t.Fatalf("missing message err = %v, want %v naming message 4", err, ErrMessageNotFound)
	}
}

func TestFakeResolvesDocumentBatchInOrder(t *testing.T) {
	t.Parallel()

	f := NewFake(1)
	peer := InputPeer{ChannelID: 10, AccessHash: 20}
	f.SeedHistory(HistoryMessage{MsgID: 5, HasMedia: true, MediaSize: 8, DocumentName: "a.bin"})
	f.SeedHistory(HistoryMessage{MsgID: 6, HasMedia: true, MediaSize: 9, DocumentName: "b.bin"})

	refs, err := f.ResolveDocuments(context.Background(), peer, []int64{6, 5})
	if err != nil {
		t.Fatalf("ResolveDocuments: %v", err)
	}
	if len(refs) != 2 || refs[0].MsgID != 6 || refs[0].Size != 9 || refs[1].MsgID != 5 || refs[1].Size != 8 {
		t.Fatalf("refs = %+v, want message 6 then 5", refs)
	}
	if _, err := f.ResolveDocuments(context.Background(), peer, []int64{5, 99}); !errors.Is(err, ErrMessageNotFound) {
		t.Fatalf("missing message err = %v, want %v", err, ErrMessageNotFound)
	}
}
