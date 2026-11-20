package file

import (
	"context"
	"errors"
	"slices"
	"testing"

	"TDrive/backend/channelsource"
	"TDrive/backend/projection"
	"TDrive/backend/tgclient"
)

func TestResolveSharedCopyUsesManagedPartOrder(t *testing.T) {
	svc, db, fake, _ := newTestService(t)
	setSharedCopyPeer(svc, fake)
	first := sharedCopyMessage(17, 150, 3)
	second := sharedCopyMessage(16, 151, 4)
	fake.SeedHistory(first, second)
	for index, message := range []tgclient.HistoryMessage{first, second} {
		project(t, db, sharedChannelID, message.MsgID, 8, projection.Op{
			Type: projection.OpFilePart, UploadUUID: "shared-parts", PartIndex: index, FileSize: message.MediaSize,
		})
	}
	project(t, db, sharedChannelID, 23, 8, projection.Op{
		Type: projection.OpFileManifest, UploadUUID: "shared-parts", PartCount: 2, Name: "full.mov", FileSize: 7,
	})
	source := channelsource.CopySource{
		PeerKind: SharedCopyPeerKind, PeerID: sharedChannelID, MsgID: 23, AccountID: 7, Generation: "1", Identity: "managed",
	}
	got, err := svc.ResolveSharedCopy(t.Context(), source)
	if err != nil || got.Name != "full.mov" || !slices.Equal(got.Parts, []int64{17, 16}) {
		t.Fatalf("resolve file = %+v, %v", got, err)
	}
	ref, err := svc.ResolveSharedCopyPart(t.Context(), source, 16)
	if err != nil || ref.DocumentID != 151 || ref.Size != 4 || ref.Name != "full.mov" || ref.Peer.ChannelID != sharedChannelID {
		t.Fatalf("resolve part = %+v, %v", ref, err)
	}
	if _, err := svc.ResolveSharedCopyPart(t.Context(), source, 23); !errors.Is(err, channelsource.ErrCopyChanged) {
		t.Fatalf("resolve manifest as part: %v", err)
	}
	source.Generation = "2"
	if _, err := svc.ResolveSharedCopyPart(t.Context(), source, 16); !errors.Is(err, channelsource.ErrCopyChanged) {
		t.Fatalf("resolve a stale revision: %v", err)
	}
}

func TestResolveSharedCopyAllowsOtherMembersRawDocuments(t *testing.T) {
	svc, _, fake, actor := newTestService(t)
	peer := setSharedCopyPeer(svc, fake)
	message := sharedCopyMessage(31, 99, 5)
	fake.SeedHistory(message)
	source := channelsource.CopySource{
		PeerKind: SharedCopyPeerKind, PeerID: sharedChannelID, MsgID: 31, AccountID: 7, Generation: "0",
		Identity: tgclient.SharedRawCopyIdentity(message),
	}
	got, err := svc.ResolveSharedCopy(t.Context(), source)
	if err != nil || got.Name != "member.mov" || !slices.Equal(got.Parts, []int64{31}) {
		t.Fatalf("resolve raw file from another member = %+v, %v", got, err)
	}
	peer.Protected = true
	fake.SeedMediaSourcePeers(peer)
	if _, err := svc.ResolveSharedCopyPart(t.Context(), source, 31); !errors.Is(err, channelsource.ErrCopyNotAllowed) {
		t.Fatalf("resolve from newly protected group: %v", err)
	}
	fake.SeedMediaSourcePeers()
	if _, err := svc.ResolveSharedCopyPart(t.Context(), source, 31); !errors.Is(err, channelsource.ErrCopyNotAllowed) {
		t.Fatalf("resolve with membership lost: %v", err)
	}
	peer.Protected = false
	fake.SeedMediaSourcePeers(peer)
	*actor = 8
	if _, err := svc.ResolveSharedCopyPart(t.Context(), source, 31); !errors.Is(err, channelsource.ErrCopyNotAllowed) {
		t.Fatalf("resolve from wrong account: %v", err)
	}
}

func TestResolveSharedCopyRejectsHiddenAndRestrictedBodies(t *testing.T) {
	for _, test := range []struct {
		name string
		edit func(*tgclient.HistoryMessage)
	}{
		{"no forwards", func(message *tgclient.HistoryMessage) { message.NoForwards = true }},
		{"media TTL", func(message *tgclient.HistoryMessage) { message.TTLSeconds = 60 }},
		{"message auto delete", func(message *tgclient.HistoryMessage) { message.AutoDeleteSeconds = 3600 }},
		{"paid attachment", func(message *tgclient.HistoryMessage) { message.Paid = true }},
		{"restricted attachment", func(message *tgclient.HistoryMessage) { message.Restricted = true }},
		{"native photo", func(message *tgclient.HistoryMessage) { message.NativePhoto = true }},
		{"hidden part caption", func(message *tgclient.HistoryMessage) {
			message.Text = projection.Format(projection.Op{Type: projection.OpFilePart, UploadUUID: "private-body", FileSize: 5})
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			svc, _, fake, _ := newTestService(t)
			setSharedCopyPeer(svc, fake)
			message := sharedCopyMessage(31, 99, 5)
			identity := tgclient.SharedRawCopyIdentity(message)
			test.edit(&message)
			fake.SeedHistory(message)
			source := channelsource.CopySource{
				PeerKind: SharedCopyPeerKind, PeerID: sharedChannelID, MsgID: 31, AccountID: 7, Generation: "0", Identity: identity,
			}
			if _, err := svc.ResolveSharedCopyPart(t.Context(), source, 31); !errors.Is(err, channelsource.ErrCopyNotAllowed) {
				t.Fatalf("resolve disallowed attachment: %v", err)
			}
		})
	}
}

func TestResolveSharedCopyRechecksProjectionAfterDocumentRead(t *testing.T) {
	svc, db, fake, _ := newTestService(t)
	setSharedCopyPeer(svc, fake)
	message := sharedCopyMessage(31, 99, 5)
	fake.SeedHistory(message)
	svc.TG = mutatingSharedCopyClient{Fake: fake, mutate: func() {
		project(t, db, sharedChannelID, 31, 8, projection.Op{Type: projection.OpFileUpload, Name: "adopted.mov", FileSize: 5})
	}}
	source := channelsource.CopySource{
		PeerKind: SharedCopyPeerKind, PeerID: sharedChannelID, MsgID: 31, AccountID: 7, Generation: "0",
		Identity: tgclient.SharedRawCopyIdentity(message),
	}
	if _, err := svc.ResolveSharedCopyPart(t.Context(), source, 31); !errors.Is(err, channelsource.ErrCopyChanged) {
		t.Fatalf("resolve became managed in flight: %v", err)
	}
}

func TestResolveSharedCopyRejectsReplacedRawAttachment(t *testing.T) {
	svc, _, fake, _ := newTestService(t)
	setSharedCopyPeer(svc, fake)
	original := sharedCopyMessage(31, 99, 5)
	replacement := sharedCopyMessage(31, 100, 5)
	fake.SeedHistory(replacement)
	source := channelsource.CopySource{
		PeerKind: SharedCopyPeerKind, PeerID: sharedChannelID, MsgID: original.MsgID, AccountID: 7, Generation: "0",
		Identity: tgclient.SharedRawCopyIdentity(original),
	}
	if _, err := svc.ResolveSharedCopy(t.Context(), source); !errors.Is(err, channelsource.ErrCopyChanged) {
		t.Fatalf("resolve replaced attachment with the same message ID and size: %v", err)
	}
	source.Identity = "raw"
	if err := ValidateSharedCopySource(source); !errors.Is(err, channelsource.ErrCopyNotAllowed) {
		t.Fatalf("validate loose selection without an attachment identity: %v", err)
	}
}

func TestResolveSharedCopyRejectsEncryptedFilesAndPersonalDriveSources(t *testing.T) {
	svc, db, fake, _ := newTestService(t)
	setSharedCopyPeer(svc, fake)
	project(t, db, sharedChannelID, 31, 8, projection.Op{
		Type: projection.OpFileUpload, Name: "locked.mov", FileSize: 10, Encrypted: true,
		EncryptionVersion: 1, PlaintextSize: 5,
	})
	source := channelsource.CopySource{
		PeerKind: SharedCopyPeerKind, PeerID: sharedChannelID, MsgID: 31, AccountID: 7, Generation: "1", Identity: "managed",
	}
	if _, err := svc.ResolveSharedCopy(t.Context(), source); !errors.Is(err, channelsource.ErrCopyNotAllowed) {
		t.Fatalf("resolve encrypted shared file: %v", err)
	}
	source.PeerID = personalChannelID
	if _, err := svc.ResolveSharedCopy(t.Context(), source); !errors.Is(err, channelsource.ErrCopyNotAllowed) {
		t.Fatalf("resolve personal drive as a shared source: %v", err)
	}
}

func setSharedCopyPeer(svc *Service, fake *tgclient.Fake) tgclient.SourcePeer {
	peer := tgclient.SourcePeer{Kind: tgclient.PeerSupergroup, ID: sharedChannelID, AccessHash: 88}
	svc.Peers = testPeerResolver{peer: tgclient.InputPeer{ChannelID: sharedChannelID, AccessHash: peer.AccessHash}}
	fake.SeedMediaSourcePeers(peer)
	return peer
}

func sharedCopyMessage(messageID, documentID, size int64) tgclient.HistoryMessage {
	return tgclient.HistoryMessage{
		PeerKind: tgclient.PeerSupergroup, ChannelID: sharedChannelID, MsgID: messageID, FromID: 8,
		HasMedia: true, DocumentID: documentID, DocumentName: "member.mov", MediaSize: size,
	}
}

type mutatingSharedCopyClient struct {
	*tgclient.Fake
	mutate func()
}

func (client mutatingSharedCopyClient) ResolveMessageDocument(ctx context.Context, peer tgclient.InputPeer, msgID int64) (tgclient.HistoryMessage, tgclient.DocumentRef, error) {
	message, ref, err := client.Fake.ResolveMessageDocument(ctx, peer, msgID)
	if err == nil {
		client.mutate()
	}
	return message, ref, err
}
