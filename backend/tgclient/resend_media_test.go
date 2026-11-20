package tgclient

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"testing/synctest"
	"time"

	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"
)

func TestResendMediaRechecksAfterWriteCooldown(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		g := &Gotd{writes: newWriteCoordinator(nil, nil)}
		g.writes.observe(NewFloodWaitError(time.Second))
		now := time.Now()
		denied := errors.New("source no longer accessible")
		resolves, sends := 0, 0
		_, err := g.resendMedia(t.Context(), InputPeer{ChannelID: 3}, 25,
			func(context.Context) (ExistingMedia, error) {
				resolves++
				if time.Since(now) < time.Second {
					t.Fatal("source access checked before the write cooldown")
				}
				return ExistingMedia{}, denied
			},
			func(context.Context, *tg.MessagesSendMediaRequest) (tg.UpdatesClass, error) {
				sends++
				return nil, nil
			},
		)
		if !errors.Is(err, denied) || errors.Is(err, ErrSendOutcomeUnknown) || resolves != 1 || sends != 0 {
			t.Fatalf("resend = %v, %d checks, %d sends; want definite refusal", err, resolves, sends)
		}
	})
}

func TestResendMediaRefreshesReferenceAndKeepsRandomID(t *testing.T) {
	g := &Gotd{writes: newWriteCoordinator(nil, nil)}
	peer := InputPeer{ChannelID: 3, AccessHash: 70}
	resolves, sends := 0, 0
	result, err := g.resendMedia(t.Context(), peer, 25,
		func(context.Context) (ExistingMedia, error) {
			resolves++
			return resendMediaTestDocument(byte(resolves)), nil
		},
		func(_ context.Context, request *tg.MessagesSendMediaRequest) (tg.UpdatesClass, error) {
			sends++
			doc := request.Media.(*tg.InputMediaDocument).ID.(*tg.InputDocument)
			if request.RandomID != 25 || request.Peer.(*tg.InputPeerChannel).ChannelID != 3 ||
				request.Message != "part caption" || doc.ID != 80 || doc.AccessHash != 71 ||
				len(doc.FileReference) != 1 || doc.FileReference[0] != byte(sends) {
				t.Fatalf("incorrect media identity or request: %#v, %#v", request, doc)
			}
			if sends == 1 {
				return nil, tgerr.New(400, "FILE_REFERENCE_EXPIRED")
			}
			return &tg.Updates{Updates: []tg.UpdateClass{
				&tg.UpdateNewChannelMessage{Message: &tg.Message{ID: 777}},
				&tg.UpdateMessageID{ID: 91, RandomID: 25},
			}}, nil
		},
	)
	if err != nil || result.MsgID != 91 || resolves != 2 || sends != 2 {
		t.Fatalf("resend = %+v, %v, %d checks, %d sends; want exactly one refresh and the matching receipt", result, err, resolves, sends)
	}
}

func TestResendMediaRetainsUnknownSendWhenNextCheckDenies(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		g := &Gotd{writes: newWriteCoordinator(nil, nil)}
		denied := errors.New("source protected")
		transport := errors.New("connection reset by peer")
		resolves, sends := 0, 0
		_, err := g.resendMedia(t.Context(), InputPeer{ChannelID: 3}, 25,
			func(context.Context) (ExistingMedia, error) {
				resolves++
				if resolves > 1 {
					return ExistingMedia{}, denied
				}
				return resendMediaTestDocument(1), nil
			},
			func(context.Context, *tg.MessagesSendMediaRequest) (tg.UpdatesClass, error) {
				sends++
				return nil, transport
			},
		)
		if !errors.Is(err, denied) || !errors.Is(err, transport) || !errors.Is(err, ErrSendOutcomeUnknown) ||
			resolves != 2 || sends != 1 {
			t.Fatalf("resend = %v, %d checks, %d sends; want prior uncertainty and no second send", err, resolves, sends)
		}
	})
}

func TestResendMediaDistinguishesRejectionFromUnknownAcceptance(t *testing.T) {
	for _, option := range []struct {
		name     string
		rpcError error
		unknown  bool
	}{
		{name: "permission rejected", rpcError: tgerr.New(400, "CHAT_FORWARDS_RESTRICTED")},
		{name: "duplicate random id", rpcError: tgerr.New(500, "RANDOM_ID_DUPLICATE"), unknown: true},
		{name: "missing receipt", unknown: true},
	} {
		t.Run(option.name, func(t *testing.T) {
			g := &Gotd{writes: newWriteCoordinator(nil, nil)}
			result, err := g.resendMedia(t.Context(), InputPeer{ChannelID: 3}, 25,
				func(context.Context) (ExistingMedia, error) {
					return resendMediaTestDocument(1), nil
				},
				func(context.Context, *tg.MessagesSendMediaRequest) (tg.UpdatesClass, error) {
					return nil, option.rpcError
				},
			)
			if err == nil || result.MsgID != 0 || errors.Is(err, ErrSendOutcomeUnknown) != option.unknown {
				t.Fatalf("resend = %+v, %v, want failed with unknown=%v", result, err, option.unknown)
			}
		})
	}
}

func resendMediaTestDocument(ref byte) ExistingMedia {
	return ExistingMedia{
		Document: DocumentRef{DocumentID: 80, AccessHash: 71, Size: 142, FileReference: []byte{ref}},
		Caption:  "part caption",
	}
}

func TestHistoryMessageReadsBothDocumentAndMessageExpiry(t *testing.T) {
	document := &tg.MessageMediaDocument{Document: &tg.Document{ID: 4, Size: 5}}
	document.SetTTLSeconds(15)
	raw := &tg.Message{ID: 6, Media: document}
	raw.SetTTLPeriod(60)
	msg, ok := historyMessageFromTG(raw)
	if !ok || msg.TTLSeconds != 15 || msg.AutoDeleteSeconds != 60 {
		t.Fatalf("message = %+v, %v, want both expiry timers", msg, ok)
	}
}

func TestFakeResendMediaKeepsDestinationBytesAfterSourceMessageDeletion(t *testing.T) {
	ctx := t.Context()
	fake := NewFake(4)
	source := InputPeer{Kind: PeerGroup, ChannelID: 5}
	destination := InputPeer{ChannelID: 12}
	const msgID = 42
	body := []byte("complete document content")
	fake.SeedHistory(HistoryMessage{
		PeerKind: source.Kind, ChannelID: source.ChannelID, MsgID: msgID, DocumentID: 90,
		HasMedia: true, MediaSize: int64(len(body)), DocumentName: "payload.bin",
	})
	fake.SeedSourceDocumentBody(source.Kind, source.ChannelID, msgID, body)
	prepare := func(ctx context.Context) (ExistingMedia, error) {
		ref, err := fake.ResolveDocument(ctx, source, msgID)
		return ExistingMedia{Document: ref, Caption: "hidden body"}, err
	}
	receipt, err := fake.ResendMedia(ctx, destination, 25, prepare)
	if err != nil || receipt.MsgID == msgID || receipt.MsgID <= 0 {
		t.Fatalf("resend = %+v, %v, want a separate destination message", receipt, err)
	}
	if err := fake.DeleteMessages(ctx, source, []int64{msgID}); err != nil {
		t.Fatal(err)
	}
	var copied bytes.Buffer
	if err := fake.DownloadFile(ctx, destination, receipt.MsgID, &copied, nil); err != nil || !bytes.Equal(copied.Bytes(), body) {
		t.Fatalf("destination download = %q, %v, want %q", copied.Bytes(), err, body)
	}
}
