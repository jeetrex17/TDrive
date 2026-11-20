package channelsource

import (
	"context"
	"errors"
	"testing"

	"TDrive/backend/tgclient"
)

func TestSourceCopyEligibilityIsSeparateFromPreviewSupport(t *testing.T) {
	source := tgclient.SourcePeer{Kind: tgclient.PeerGroup, ID: 310}
	document := tgclient.HistoryMessage{MsgID: 27, DocumentID: 40, HasMedia: true, MediaSize: 120,
		DocumentName: "archive.zip", MimeType: "application/zip", Text: "Archive from our group"}
	item, ok := mediaItem(source, document)
	if !ok || item.Streamable || item.CopyIdentity == "" {
		t.Fatalf("unsupported preview should still be eligible for a copy: %+v", item)
	}
	legacy := document
	legacy.Text = "TDX1|t=f|p=|n=archive.zip|sz=120"
	if item, ok := mediaItem(source, legacy); !ok || item.CopyIdentity == "" {
		t.Fatalf("clear legacy document should still be eligible for a copy: %+v", item)
	}
	for _, option := range []struct {
		name   string
		adjust func(*tgclient.SourcePeer, *tgclient.HistoryMessage)
	}{
		{"protected peer", func(peer *tgclient.SourcePeer, _ *tgclient.HistoryMessage) { peer.Protected = true }},
		{"restricted peer", func(peer *tgclient.SourcePeer, _ *tgclient.HistoryMessage) { peer.Restricted = true }},
		{"protected post", func(_ *tgclient.SourcePeer, msg *tgclient.HistoryMessage) { msg.NoForwards = true }},
		{"restricted post", func(_ *tgclient.SourcePeer, msg *tgclient.HistoryMessage) { msg.Restricted = true }},
		{"paid post", func(_ *tgclient.SourcePeer, msg *tgclient.HistoryMessage) { msg.Paid = true }},
		{"attachment expiry", func(_ *tgclient.SourcePeer, msg *tgclient.HistoryMessage) { msg.TTLSeconds = 30 }},
		{"message auto-delete", func(_ *tgclient.SourcePeer, msg *tgclient.HistoryMessage) { msg.AutoDeleteSeconds = 60 }},
		{"native photo", func(_ *tgclient.SourcePeer, msg *tgclient.HistoryMessage) { msg.NativePhoto = true }},
		{"internal part", func(_ *tgclient.SourcePeer, msg *tgclient.HistoryMessage) {
			msg.Text = "TDX1|t=part|u=upload|pix=0|sz=120"
		}},
		{"encrypted legacy upload", func(_ *tgclient.SourcePeer, msg *tgclient.HistoryMessage) {
			msg.Text = "TDX1|t=f|n=archive.zip|enc=1|psz=120|ev=1"
		}},
		{"legacy upload with multipart hint", func(_ *tgclient.SourcePeer, msg *tgclient.HistoryMessage) {
			msg.Text = "TDX1|t=f|n=archive.zip|u=upload"
		}},
	} {
		t.Run(option.name, func(t *testing.T) {
			peer, message := source, document
			option.adjust(&peer, &message)
			row, _ := mediaItem(peer, message)
			if row.CopyIdentity != "" {
				t.Fatalf("ineligible message exposed a copy identity: %+v", row)
			}
		})
	}
}

func TestResolveCopyMediaRejectsStaleScopeAndPolicy(t *testing.T) {
	const peerID, msgID = int64(310), int64(27)
	fake := tgclient.NewFake(testAccountID)
	group := tgclient.SourcePeer{Kind: tgclient.PeerGroup, ID: peerID, Title: "Group"}
	fake.SeedMediaSourcePeers(group, tgclient.SourcePeer{Kind: tgclient.PeerUser, ID: peerID, AccessHash: 811, Title: "User"})
	message := tgclient.HistoryMessage{PeerKind: tgclient.PeerGroup, ChannelID: peerID, MsgID: msgID,
		HasMedia: true, DocumentID: 40, DocumentName: " archive.zip ", MimeType: "application/zip", MediaSize: 120}
	otherPeer := message
	otherPeer.PeerKind = tgclient.PeerUser
	fake.SeedHistory(message, otherPeer)
	client := &copyDocumentTestClient{Fake: fake}
	service, _, _ := sourceFixture(t, client, fake)
	connected := connectTyped(t, service, group.Kind, peerID, "")
	other := connectTyped(t, service, otherPeer.PeerKind, peerID, "")
	page, err := service.PageSource(t.Context(), connected.PeerKind, peerID, 0, 10, "", "all")
	if err != nil || len(page.Items) != 1 || page.Items[0].CopyIdentity == "" {
		t.Fatalf("source page = %+v, error %v, want a copyable item", page, err)
	}
	request := CopySource{AccountID: testAccountID, PeerKind: connected.PeerKind, PeerID: peerID,
		Generation: connected.Generation, MsgID: msgID, Identity: page.Items[0].CopyIdentity}
	ref, err := service.ResolveCopyMedia(t.Context(), request)
	if err != nil || ref.DocumentID != 40 || ref.Size != 120 || ref.Name != "archive.zip" || ref.Peer.PeerKind() != group.Kind {
		t.Fatalf("resolved document = %+v, error %v", ref, err)
	}
	otherRequest := request
	otherRequest.PeerKind, otherRequest.Generation = other.PeerKind, other.Generation
	if _, err := service.ResolveCopyMedia(t.Context(), otherRequest); !errors.Is(err, ErrCopyChanged) {
		t.Fatalf("equal message ID in another peer: %v, want changed", err)
	}
	for _, option := range []struct {
		name   string
		adjust func(*tgclient.HistoryMessage, *tgclient.DocumentRef)
		want   error
	}{
		{"post protection", func(msg *tgclient.HistoryMessage, _ *tgclient.DocumentRef) { msg.NoForwards = true }, ErrCopyNotAllowed},
		{"attachment expiry", func(msg *tgclient.HistoryMessage, _ *tgclient.DocumentRef) { msg.TTLSeconds = 10 }, ErrCopyNotAllowed},
		{"message expiry", func(msg *tgclient.HistoryMessage, _ *tgclient.DocumentRef) { msg.AutoDeleteSeconds = 60 }, ErrCopyNotAllowed},
		{"internal body caption", func(msg *tgclient.HistoryMessage, _ *tgclient.DocumentRef) {
			msg.Text = "TDX1|t=part|u=upload|pix=0|sz=120"
		}, ErrCopyNotAllowed},
		{"edited attachment", func(msg *tgclient.HistoryMessage, ref *tgclient.DocumentRef) { msg.DocumentID++; ref.DocumentID++ }, ErrCopyChanged},
	} {
		t.Run(option.name, func(t *testing.T) {
			client.adjust = option.adjust
			defer func() { client.adjust = nil }()
			if _, err := service.ResolveCopyMedia(t.Context(), request); !errors.Is(err, option.want) {
				t.Fatalf("resolve = %v, want %v", err, option.want)
			}
		})
	}
	blockedGroup := group
	blockedGroup.Protected = true
	fake.SeedMediaSourcePeers(blockedGroup)
	if _, err := service.ResolveCopyMedia(t.Context(), request); !errors.Is(err, ErrCopyNotAllowed) {
		t.Fatalf("stale peer protection: %v, want blocked", err)
	}
	fake.SeedMediaSourcePeers()
	if _, err := service.ResolveCopyMedia(t.Context(), request); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("lost membership: %v, want unavailable", err)
	}
	fake.SetSelfID(testAccountID + 1)
	if _, err := service.ResolveCopyMedia(t.Context(), request); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("switched account: %v, want unavailable", err)
	}
	fake.SetSelfID(testAccountID)
	if _, err := service.DisconnectSourceWithGate(t.Context(), request.PeerKind, peerID, testAccountID, request.Generation,
		func(remove func() error) error { return remove() }); err != nil {
		t.Fatal(err)
	}
	if _, err := service.ResolveCopyMedia(t.Context(), request); !errors.Is(err, ErrNotConnected) {
		t.Fatalf("disconnected source: %v, want not connected", err)
	}
}

type copyDocumentTestClient struct {
	*tgclient.Fake
	adjust func(*tgclient.HistoryMessage, *tgclient.DocumentRef)
}

func (c *copyDocumentTestClient) ResolveMessageDocument(ctx context.Context, peer tgclient.InputPeer, msgID int64) (tgclient.HistoryMessage, tgclient.DocumentRef, error) {
	message, ref, err := c.Fake.ResolveMessageDocument(ctx, peer, msgID)
	if err == nil && c.adjust != nil {
		c.adjust(&message, &ref)
	}
	return message, ref, err
}
