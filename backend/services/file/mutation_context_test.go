package file

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"TDrive/backend/projection"
	"TDrive/backend/tgclient"
)

func TestMutationMethodsRejectNilContext(t *testing.T) {
	svc, _, _, _ := newTestService(t)
	tests := map[string]func() error{
		"metadata": func() error {
			return svc.MetaContext(nil, personalChannelID, 1, "file.txt", 1, "")
		},
		"rename": func() error {
			return svc.Rename(nil, personalChannelID, 1, "renamed.txt")
		},
		"move": func() error {
			return svc.Move(nil, personalChannelID, 1, "")
		},
		"delete": func() error {
			return svc.Delete(nil, personalChannelID, 1)
		},
	}
	for operation, call := range tests {
		t.Run(operation, func(t *testing.T) {
			err := call()
			if !errors.Is(err, projection.ErrInvalidContext) {
				t.Fatalf("error = %v, want projection.ErrInvalidContext", err)
			}
			if !strings.Contains(err.Error(), operation) {
				t.Fatalf("error = %q, want %q operation context", err, operation)
			}
		})
	}
}

func TestSharedRawMetadataUsesAuthoritativeSenderAndPreservesContentOnRename(t *testing.T) {
	svc, db, fakeTG, actor := newTestService(t)
	peer := tgclient.InputPeer{ChannelID: sharedChannelID, AccessHash: 88, Kind: tgclient.PeerSupergroup}
	svc.Peers = testPeerResolver{peer: peer}
	fakeTG.SeedMediaSourcePeers(tgclient.SourcePeer{ID: sharedChannelID, AccessHash: 88, Kind: tgclient.PeerSupergroup})
	body := []byte("forwarded bytes")
	fakeTG.SeedHistory(tgclient.HistoryMessage{
		PeerKind: tgclient.PeerSupergroup, ChannelID: sharedChannelID, MsgID: 55, DocumentID: 555, FromID: *actor, Date: 876,
		HasMedia: true, DocumentName: "original.pdf", MediaSize: int64(len(body)),
	})
	fakeTG.SeedDocumentBody(55, body)
	if err := svc.MetaContext(t.Context(), sharedChannelID, 55, "forged.bin", 999, ""); err != nil {
		t.Fatalf("adopt forwarded file: %v", err)
	}
	file, found, err := projection.FileByID(db, sharedChannelID, 55)
	if err != nil || !found {
		t.Fatalf("adopted file: %+v, found=%v, err=%v", file, found, err)
	}
	uploader, err := projection.FileUploader(db, sharedChannelID, 55)
	if err != nil {
		t.Fatalf("read uploader: %v", err)
	}
	if file.Name != "original.pdf" || file.Size != int64(len(body)) || file.UploadTime != 876 || uploader != *actor {
		t.Fatalf("adopted metadata = %+v, want authoritative document metadata and sender", file)
	}
	if err := svc.Rename(t.Context(), sharedChannelID, 55, "renamed.pdf"); err != nil {
		t.Fatalf("rename adopted file: %v", err)
	}
	file, found, err = projection.FileByID(db, sharedChannelID, 55)
	if err != nil || !found || file.Name != "renamed.pdf" || file.ContentMsgID != 55 {
		t.Fatalf("renamed file: %+v, found=%v, err=%v", file, found, err)
	}
	var downloaded bytes.Buffer
	if err := fakeTG.DownloadFile(t.Context(), peer, file.ContentMsgID, &downloaded, nil); err != nil {
		t.Fatalf("read original document: %v", err)
	}
	if !bytes.Equal(downloaded.Bytes(), body) {
		t.Fatalf("renamed file bytes = %q, want %q", downloaded.Bytes(), body)
	}
}

func TestSharedRawMetadataCannotClaimAnotherOrUnknownSender(t *testing.T) {
	for _, sender := range []int64{9, 0} {
		t.Run(fmt.Sprintf("sender_%d", sender), func(t *testing.T) {
			svc, db, fakeTG, _ := newTestService(t)
			svc.Peers = testPeerResolver{peer: tgclient.InputPeer{ChannelID: sharedChannelID, Kind: tgclient.PeerSupergroup}}
			fakeTG.SeedMediaSourcePeers(tgclient.SourcePeer{ID: sharedChannelID, Kind: tgclient.PeerSupergroup})
			fakeTG.SeedHistory(tgclient.HistoryMessage{PeerKind: tgclient.PeerSupergroup, ChannelID: sharedChannelID, MsgID: 55, DocumentID: 555, FromID: sender, HasMedia: true, MediaSize: 2, DocumentName: "forwarded.pdf"})
			if err := svc.MetaContext(t.Context(), sharedChannelID, 55, "claim.pdf", 2, ""); err == nil || !strings.Contains(err.Error(), "Only the uploader") {
				t.Fatal("metadata adoption granted ownership to a different or unknown sender")
			}
			var operations int
			if err := db.QueryRow(`SELECT COUNT(*) FROM replay_log WHERE channel_id=?`, sharedChannelID).Scan(&operations); err != nil {
				t.Fatal(err)
			}
			if operations != 0 || projection.FileExists(db, sharedChannelID, 55) {
				t.Fatalf("rejected adoption changed projection: operations=%d", operations)
			}
		})
	}
}

func TestSharedRawMetadataRejectsHiddenBodiesBeforeEmit(t *testing.T) {
	for _, test := range []struct {
		name    string
		caption string
		managed bool
	}{
		{name: "encrypted upload", caption: projection.Format(projection.Op{Type: projection.OpFileUpload, Name: "secret.pdf", FileSize: 2, Encrypted: true})},
		{name: "multipart body", caption: projection.Format(projection.Op{Type: projection.OpFilePart, UploadUUID: "upload", PartIndex: 0, FileSize: 2})},
		{name: "malformed control", caption: "TDX1|t=unsupported"},
		{name: "pending cleanup", managed: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			svc, db, fakeTG, actor := newTestService(t)
			svc.Peers = testPeerResolver{peer: tgclient.InputPeer{ChannelID: sharedChannelID, Kind: tgclient.PeerSupergroup}}
			fakeTG.SeedMediaSourcePeers(tgclient.SourcePeer{ID: sharedChannelID, Kind: tgclient.PeerSupergroup})
			fakeTG.SeedHistory(tgclient.HistoryMessage{
				PeerKind: tgclient.PeerSupergroup, ChannelID: sharedChannelID, MsgID: 55, DocumentID: 555, FromID: *actor, Text: test.caption,
				HasMedia: true, MediaSize: 2, DocumentName: "hidden.pdf",
			})
			if test.managed {
				if err := projection.QueuePartCleanup(db, sharedChannelID, []int64{55}); err != nil {
					t.Fatal(err)
				}
			}
			emitted := false
			svc.EmitOpContext = func(context.Context, int64, projection.Op) (int64, error) {
				emitted = true
				return 0, nil
			}
			if err := svc.MetaContext(t.Context(), sharedChannelID, 55, "reveal.pdf", 2, ""); err == nil {
				t.Fatal("hidden body was adopted")
			}
			if emitted || projection.FileExists(db, sharedChannelID, 55) {
				t.Fatal("hidden body rejection had side effects")
			}
		})
	}
}

func TestSharedRawMetadataRejectsRestrictedMediaBeforeEmit(t *testing.T) {
	for _, test := range []struct {
		name         string
		message      tgclient.HistoryMessage
		protected    bool
		restricted   bool
		wrongChannel bool
	}{
		{name: "protected peer", protected: true},
		{name: "restricted peer", restricted: true},
		{name: "protected message", message: tgclient.HistoryMessage{NoForwards: true, DocumentID: 555}},
		{name: "restricted message", message: tgclient.HistoryMessage{Restricted: true, DocumentID: 555}},
		{name: "expiring message", message: tgclient.HistoryMessage{TTLSeconds: 30, DocumentID: 555}},
		{name: "paid message", message: tgclient.HistoryMessage{Paid: true, DocumentID: 555}},
		{name: "missing document"},
		{name: "wrong peer channel", wrongChannel: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			svc, db, fakeTG, actor := newTestService(t)
			reads := &sharedAdoptionReadRecorder{Client: fakeTG}
			svc.TG = reads
			peer := tgclient.InputPeer{ChannelID: sharedChannelID, Kind: tgclient.PeerSupergroup}
			if test.wrongChannel {
				peer.ChannelID = personalChannelID
			}
			svc.Peers = testPeerResolver{peer: peer}
			fakeTG.SeedMediaSourcePeers(tgclient.SourcePeer{ID: peer.ChannelID, Kind: tgclient.PeerSupergroup, Protected: test.protected, Restricted: test.restricted})
			message := test.message
			message.PeerKind = tgclient.PeerSupergroup
			message.ChannelID = peer.ChannelID
			message.MsgID = 55
			message.FromID = *actor
			message.HasMedia = true
			message.MediaSize = 2
			message.DocumentName = "protected.pdf"
			if test.protected || test.restricted || test.wrongChannel {
				message.DocumentID = 555
			}
			fakeTG.SeedHistory(message)
			emitted := false
			svc.EmitOpContext = func(context.Context, int64, projection.Op) (int64, error) {
				emitted = true
				return 0, nil
			}
			if err := svc.MetaContext(t.Context(), sharedChannelID, 55, "claim.pdf", 2, ""); err == nil {
				t.Fatal("restricted body was adopted")
			}
			if emitted || projection.FileExists(db, sharedChannelID, 55) {
				t.Fatal("rejected restricted body changed projection")
			}
			if test.wrongChannel && reads.calls != 0 {
				t.Fatalf("wrong channel was used for %d Telegram reads", reads.calls)
			}
		})
	}
}

type sharedAdoptionReadRecorder struct {
	tgclient.Client
	calls int
}

func (r *sharedAdoptionReadRecorder) GetChannelMessage(ctx context.Context, peer tgclient.InputPeer, msgID int64) (tgclient.HistoryMessage, error) {
	r.calls++
	return r.Client.GetChannelMessage(ctx, peer, msgID)
}

func (r *sharedAdoptionReadRecorder) GetMediaSourcePeer(ctx context.Context, peer tgclient.InputPeer) (tgclient.SourcePeer, error) {
	r.calls++
	return r.Client.GetMediaSourcePeer(ctx, peer)
}

func TestSharedRawMetadataDoesNotResurrectTrashedFile(t *testing.T) {
	svc, db, _, actor := newTestService(t)
	project(t, db, sharedChannelID, 55, *actor, projection.Op{
		Type: projection.OpFileUpload, Name: "trashed.pdf", FileSize: 2,
	})
	if err := svc.Delete(t.Context(), sharedChannelID, 55); err != nil {
		t.Fatalf("trash file: %v", err)
	}
	if err := svc.MetaContext(t.Context(), sharedChannelID, 55, "resurrect.pdf", 2, ""); err == nil {
		t.Fatal("metadata resurrected a trashed file")
	}
	if projection.FileExists(db, sharedChannelID, 55) {
		t.Fatal("trashed file became visible")
	}
}

func TestRenameStopsBeforeEmitWhenContextCanceled(t *testing.T) {
	svc, db, _, _ := newTestService(t)
	project(t, db, personalChannelID, 44, 7, projection.Op{
		Type:           projection.OpFileUpload,
		Name:           "before.txt",
		FileSize:       1,
		FileUploadTime: 1,
	})
	called := false
	svc.EmitOpContext = func(context.Context, int64, projection.Op) (int64, error) {
		called = true
		return 0, nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := svc.Rename(ctx, personalChannelID, 44, "after.txt")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Rename error = %v, want context canceled", err)
	}
	if called {
		t.Fatal("emitter called after cancellation")
	}
}

func TestMetaContextPropagatesContextToEmitter(t *testing.T) {
	svc, _, _, _ := newTestService(t)
	type contextKey string
	const key contextKey = "mutation"
	ctx := context.WithValue(context.Background(), key, "mounted")
	called := false
	svc.EmitOpContext = func(got context.Context, channelID int64, op projection.Op) (int64, error) {
		called = true
		if got.Value(key) != "mounted" {
			t.Fatalf("context value was not propagated")
		}
		return svc.EmitOp(channelID, op)
	}

	if err := svc.MetaContext(ctx, personalChannelID, 55, "mounted.txt", 8, ""); err != nil {
		t.Fatalf("MetaContext: %v", err)
	}
	if !called {
		t.Fatal("context emitter was not called")
	}
}
