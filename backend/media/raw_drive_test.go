package media

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"TDrive/backend/projection"
	"TDrive/backend/tgclient"
	"github.com/gotd/td/tgerr"
)

func TestRawDrivePreviewsServeUnprojectedAttachments(t *testing.T) {
	for _, tc := range []struct {
		name, mime string
		body       []byte
		kind       StreamKind
	}{
		{"clip.mp4", "video/mp4", []byte("synthetic mp4 range bytes"), StreamKindVideo},
		{"document.pdf", "application/pdf", []byte("%PDF-1.4\nsynthetic PDF bytes"), StreamKindPDF},
		{"notes.md", "text/markdown", []byte("# Plain text preview\n"), StreamKindText},
		{"photo.jpg", "image/jpeg", jpegFixture(t, 8, 8), StreamKindImage},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc, _, _, _ := rawDriveFixture(t, tc.name, tc.mime, tc.body)
			var result OpenResult
			var err error
			if tc.kind == StreamKindImage {
				result, err = svc.OpenImage(t.Context(), testChannelID, 7, 0)
			} else {
				result, err = svc.OpenStream(t.Context(), testChannelID, 7)
			}
			if err != nil {
				t.Fatalf("preview visible raw attachment: %v", err)
			}
			if result.Kind != tc.kind || result.Info.SourceKind != "drive" {
				t.Fatalf("preview identity = %+v", result)
			}
			request, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, result.URL, nil)
			request.Header.Set("Range", "bytes=2-7")
			response, err := http.DefaultClient.Do(request)
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			body, err := io.ReadAll(response.Body)
			if err != nil {
				t.Fatal(err)
			}
			if response.StatusCode != http.StatusPartialContent || !bytes.Equal(body, tc.body[2:8]) {
				t.Fatalf("range status=%d bytes=%q", response.StatusCode, body)
			}
			if response.Header.Get("Cache-Control") != "no-store, max-age=0" {
				t.Fatal("raw preview must not be persistently cached")
			}
			var count int
			if err := svc.resolver.db.QueryRow("SELECT COUNT(*) FROM files").Scan(&count); err != nil || count != 0 {
				t.Fatalf("preview wrote projection: count=%d err=%v", count, err)
			}
		})
	}
}

func rawDriveFixture(t *testing.T, name, mime string, body []byte) (*Service, *tgclient.Fake, tgclient.InputPeer, *atomic.Int64) {
	t.Helper()
	db := newResolverTestDB(t)
	peer := tgclient.InputPeer{Kind: tgclient.PeerSupergroup, ChannelID: testChannelID, AccessHash: 55}
	if err := projection.InsertChannel(db, projection.Channel{ChannelID: testChannelID, AccessHash: 55, Kind: projection.KindShared}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("UPDATE channels SET kind = ? WHERE channel_id = ?", projection.KindShared, testChannelID); err != nil {
		t.Fatal(err)
	}
	fake := tgclient.NewFake(1234)
	fake.SeedMediaSourcePeers(tgclient.SourcePeer{ID: peer.ChannelID, Kind: peer.Kind, AccessHash: peer.AccessHash})
	fake.SeedHistory(tgclient.HistoryMessage{PeerKind: peer.Kind, ChannelID: peer.ChannelID, MsgID: 7, HasMedia: true, DocumentID: 701, DocumentAccessHash: 702, DocumentName: name, MimeType: mime, MediaSize: int64(len(body))})
	fake.SeedSourceDocumentBody(peer.Kind, peer.ChannelID, 7, body)
	account := &atomic.Int64{}
	account.Store(1234)
	svc := NewService(Config{DB: db, Peers: staticPeerResolver{peer: peer}, Ranges: fake, RawDrive: fake, AccountID: func(context.Context) (int64, error) { return account.Load(), nil }})
	t.Cleanup(func() {
		if err := svc.Close(); err != nil {
			t.Error(err)
		}
	})
	return svc, fake, peer, account
}

func TestRawDriveRefusesManagedAndInternalAttachments(t *testing.T) {
	for _, tc := range []struct {
		name    string
		prepare func(*testing.T, *Service)
		caption string
	}{
		{name: "tombstoned", prepare: func(t *testing.T, svc *Service) {
			mustApplyOp(t, svc.resolver.db, 7, projection.Op{Type: projection.OpFileUpload, Parent: projection.RootParent, Name: "notes.md", FileSize: 20})
			mustApplyOp(t, svc.resolver.db, 8, projection.Op{Type: projection.OpTomb, Obj: "f:7"})
		}},
		{name: "hidden multipart part", prepare: func(t *testing.T, svc *Service) {
			mustApplyOp(t, svc.resolver.db, 7, projection.Op{Type: projection.OpFilePart, UploadUUID: "raw-hidden-part", PartIndex: 0, FileSize: 20})
		}},
		{name: "malformed operation", caption: "TDX1|t=f|p=bad|n=broken"},
		{name: "encrypted legacy upload", caption: "TDX1|t=f|p=|n=notes.md|sz=20|ts=1|enc=1|psz=4|ev=1"},
		{name: "internal body caption", caption: "TDX1|t=part|u=body|i=0|sz=20"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc, fake, _, _ := rawDriveFixture(t, "notes.md", "text/markdown", []byte("# Plain text preview"))
			if tc.prepare != nil {
				tc.prepare(t, svc)
			}
			if tc.caption != "" {
				svc.rawDrive = &rawMessageOverride{RawDriveSource: fake, change: func(message tgclient.HistoryMessage) tgclient.HistoryMessage {
					message.Text = tc.caption
					return message
				}}
			}
			if _, err := svc.OpenStream(t.Context(), testChannelID, 7); !errors.Is(err, ErrFileNotFound) {
				t.Fatalf("managed/internal raw fallback: %v", err)
			}
		})
	}
}

func TestRawDrivePreflightRejectsProtectionAndWrongIdentity(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(tgclient.HistoryMessage) tgclient.HistoryMessage
	}{
		{"protected message", func(m tgclient.HistoryMessage) tgclient.HistoryMessage { m.NoForwards = true; return m }},
		{"paid", func(m tgclient.HistoryMessage) tgclient.HistoryMessage { m.Paid = true; return m }},
		{"expires", func(m tgclient.HistoryMessage) tgclient.HistoryMessage { m.TTLSeconds = 10; return m }},
		{"wrong channel", func(m tgclient.HistoryMessage) tgclient.HistoryMessage { m.ChannelID++; return m }},
		{"wrong message", func(m tgclient.HistoryMessage) tgclient.HistoryMessage { m.MsgID++; return m }},
		{"wrong peer kind", func(m tgclient.HistoryMessage) tgclient.HistoryMessage { m.PeerKind = tgclient.PeerChannel; return m }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc, fake, _, _ := rawDriveFixture(t, "file.pdf", "application/pdf", []byte("%PDF-1.4 test document"))
			svc.rawDrive = &rawMessageOverride{RawDriveSource: fake, change: tc.change}
			if _, err := svc.OpenStream(t.Context(), testChannelID, 7); !errors.Is(err, ErrExternalRestricted) {
				t.Fatalf("open protected/misdirected source: %v", err)
			}
		})
	}
	svc, fake, peer, _ := rawDriveFixture(t, "file.pdf", "application/pdf", []byte("%PDF-1.4 test document"))
	fake.SeedMediaSourcePeers(tgclient.SourcePeer{ID: peer.ChannelID, Kind: peer.Kind, AccessHash: peer.AccessHash, Protected: true})
	if _, err := svc.OpenStream(t.Context(), testChannelID, 7); !errors.Is(err, ErrExternalRestricted) {
		t.Fatalf("open protected peer: %v", err)
	}
}

func TestRawDriveLiveReadsKeepCapturedAccountAndDeletionBoundary(t *testing.T) {
	for _, tc := range []struct {
		name       string
		transition func(*testing.T, *Service, *atomic.Int64)
		want       error
	}{
		{"account changed", func(_ *testing.T, _ *Service, account *atomic.Int64) { account.Store(9999) }, ErrExternalRestricted},
		{"channel removed", func(t *testing.T, svc *Service, _ *atomic.Int64) {
			if _, err := svc.resolver.db.Exec("DELETE FROM channels WHERE channel_id = ?", testChannelID); err != nil {
				t.Fatal(err)
			}
		}, ErrExternalRestricted},
		{"adopted then trashed", func(t *testing.T, svc *Service, _ *atomic.Int64) {
			mustApplyOp(t, svc.resolver.db, 7, projection.Op{Type: projection.OpFileUpload, Parent: projection.RootParent, Name: "file.pdf", FileSize: 3 * tgclient.RangeReadMaxBytes})
			mustApplyOp(t, svc.resolver.db, 8, projection.Op{Type: projection.OpTomb, Obj: "f:7"})
		}, ErrFileNotFound},
		{"adopted same original", func(t *testing.T, svc *Service, _ *atomic.Int64) {
			mustApplyOp(t, svc.resolver.db, 7, projection.Op{Type: projection.OpFileUpload, Parent: projection.RootParent, Name: "file.pdf", FileSize: 3 * tgclient.RangeReadMaxBytes})
		}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			data := bytes.Repeat([]byte{0x4a}, 3*tgclient.RangeReadMaxBytes)
			svc, _, _, account := rawDriveFixture(t, "file.pdf", "application/pdf", data)
			opened, err := svc.OpenStream(t.Context(), testChannelID, 7)
			if err != nil {
				t.Fatal(err)
			}
			tc.transition(t, svc, account)
			session := svc.server.session(opened.Token)
			buf := make([]byte, 8)
			n, err := session.ReadAt(t.Context(), buf, 2*tgclient.RangeReadMaxBytes)
			if tc.want != nil {
				if !errors.Is(err, tc.want) {
					t.Fatalf("read after transition: n=%d error=%v want=%v", n, err, tc.want)
				}
				return
			}
			if err != nil || n != len(buf) || !bytes.Equal(buf, data[2*tgclient.RangeReadMaxBytes:2*tgclient.RangeReadMaxBytes+8]) {
				t.Fatalf("same-file adoption broke original read: n=%d bytes=%x err=%v", n, buf, err)
			}
		})
	}
}

func TestRawDriveInvalidImageAndRevisionAreRejected(t *testing.T) {
	svc, _, _, _ := rawDriveFixture(t, "photo.jpg", "image/jpeg", []byte("not JPEG image bytes"))
	if _, err := svc.OpenImage(t.Context(), testChannelID, 7, 0); !errors.Is(err, ErrInvalidImage) {
		t.Fatalf("invalid image = %v", err)
	}
	if _, err := svc.OpenImage(t.Context(), testChannelID, 7, 2); !errors.Is(err, ErrStaleRevision) {
		t.Fatalf("raw stale revision = %v", err)
	}
	if len(svc.server.sessions) != 0 {
		t.Fatal("rejected image leaked session")
	}
}

func TestRawDriveAccountAndCloseRacesDoNotPublish(t *testing.T) {
	for _, closeService := range []bool{false, true} {
		t.Run(fmt.Sprintf("close=%t", closeService), func(t *testing.T) {
			svc, fake, peer, account := rawDriveFixture(t, "file.pdf", "application/pdf", []byte("%PDF-1.4 test document"))
			entered, release := make(chan struct{}), make(chan struct{})
			svc.ranges = &blockedRawResolve{RangeClient: fake, entered: entered, release: release}
			result := make(chan error, 1)
			go func() { _, err := svc.OpenStream(t.Context(), peer.ChannelID, 7); result <- err }()
			select {
			case <-entered:
			case <-time.After(5 * time.Second):
				t.Fatal("open did not reach resolution")
			}
			if closeService {
				if err := svc.Close(); err != nil {
					t.Fatal(err)
				}
			} else {
				account.Store(9999)
			}
			close(release)
			select {
			case err := <-result:
				if err == nil {
					t.Fatal("racing open published a URL")
				}
			case <-time.After(5 * time.Second):
				t.Fatal("open did not finish")
			}
			svc.server.mu.Lock()
			count := len(svc.server.sessions)
			svc.server.mu.Unlock()
			if count != 0 {
				t.Fatalf("racing open leaked %d sessions", count)
			}
		})
	}
}

type rawMessageOverride struct {
	RawDriveSource
	change func(tgclient.HistoryMessage) tgclient.HistoryMessage
}

func (s *rawMessageOverride) GetChannelMessage(ctx context.Context, peer tgclient.InputPeer, id int64) (tgclient.HistoryMessage, error) {
	message, err := s.RawDriveSource.GetChannelMessage(ctx, peer, id)
	if err != nil {
		return message, err
	}
	return s.change(message), nil
}

type blockedRawResolve struct {
	tgclient.RangeClient
	entered, release chan struct{}
}

func (c *blockedRawResolve) ResolveDocument(ctx context.Context, peer tgclient.InputPeer, id int64) (tgclient.DocumentRef, error) {
	close(c.entered)
	select {
	case <-ctx.Done():
		return tgclient.DocumentRef{}, ctx.Err()
	case <-c.release:
	}
	return c.RangeClient.ResolveDocument(ctx, peer, id)
}

func TestRawAndProjectedDriveRefreshStalePeerOnce(t *testing.T) {
	for _, projected := range []bool{false, true} {
		t.Run(fmt.Sprintf("projected=%t", projected), func(t *testing.T) {
			body := []byte("%PDF-1.4 raw drive preview")
			svc, fake, peer, _ := rawDriveFixture(t, "file.pdf", "application/pdf", body)
			fresh := peer
			fresh.Kind = "" // Engine drive peers default to channel; raw reclassifies shared.
			resolver := &refreshingDrivePeer{stale: peer, fresh: fresh}
			resolver.stale.AccessHash = 99
			resolver.fresh.AccessHash = peer.AccessHash
			svc.peers = resolver
			if projected {
				mustApplyOp(t, svc.resolver.db, 7, projection.Op{Type: projection.OpFileUpload, Parent: projection.RootParent, Name: "file.pdf", FileSize: int64(len(body))})
				svc.ranges = &staleRangeClient{RangeClient: fake, expectedHash: peer.AccessHash}
			}
			opened, err := svc.OpenStream(t.Context(), testChannelID, 7)
			if err != nil {
				t.Fatalf("stale cached peer did not recover: %v", err)
			}
			if resolver.refreshes.Load() != 1 {
				t.Fatalf("refresh calls=%d want one", resolver.refreshes.Load())
			}
			if !projected && opened.Info.SourcePeerKind != string(tgclient.PeerSupergroup) {
				t.Fatalf("lost captured shared kind: %+v", opened.Info)
			}
		})
	}
}

func TestRawDriveLegacyUploadUsesCaptionNameBeforeSync(t *testing.T) {
	for _, caption := range []string{
		"TDX1|t=f|p=|n=renamed.pdf\nTDrive: renamed.pdf",
		"TDX1|t=f|p=|n=renamed.pdf|sz=26\nTDrive: renamed.pdf",
	} {
		t.Run(caption, func(t *testing.T) {
			svc, fake, _, _ := rawDriveFixture(t, "opaque.bin", "application/pdf", []byte("%PDF-1.4 raw drive preview"))
			svc.rawDrive = &rawMessageOverride{RawDriveSource: fake, change: func(message tgclient.HistoryMessage) tgclient.HistoryMessage { message.Text = caption; return message }}
			opened, err := svc.OpenStream(t.Context(), testChannelID, 7)
			if err != nil {
				t.Fatal(err)
			}
			if opened.Name != "renamed.pdf" {
				t.Fatalf("caption name=%q", opened.Name)
			}
		})
	}
}

type refreshingDrivePeer struct {
	stale, fresh tgclient.InputPeer
	refreshes    atomic.Int32
}

func (p *refreshingDrivePeer) ResolvePeer(context.Context, int64) (tgclient.InputPeer, error) {
	return p.stale, nil
}
func (p *refreshingDrivePeer) RefreshPeer(context.Context, int64) (tgclient.InputPeer, error) {
	p.refreshes.Add(1)
	return p.fresh, nil
}

type staleRangeClient struct {
	tgclient.RangeClient
	expectedHash int64
}

func (c *staleRangeClient) ResolveDocument(ctx context.Context, peer tgclient.InputPeer, id int64) (tgclient.DocumentRef, error) {
	if peer.AccessHash != c.expectedHash {
		return tgclient.DocumentRef{}, tgerr.New(400, "CHANNEL_INVALID")
	}
	return c.RangeClient.ResolveDocument(ctx, peer, id)
}

func TestRawDriveRefRefreshRejectsPhotoVariantAndDocumentReplacement(t *testing.T) {
	for _, change := range []string{"photo variant", "document", "peer kind"} {
		t.Run(change, func(t *testing.T) {
			svc, fake, peer, _ := rawDriveFixture(t, "photo.jpg", "image/jpeg", jpegFixture(t, 8, 8))
			ranges := &changingRawReference{RangeClient: fake}
			svc.ranges = ranges
			opened, err := svc.OpenImage(t.Context(), peer.ChannelID, 7, 0)
			if err != nil {
				t.Fatal(err)
			}
			ranges.change = change
			checked := svc.server.session(opened.Token).reader.client
			if _, err := checked.ResolveDocument(t.Context(), peer, 7); !errors.Is(err, ErrExternalReplaced) {
				t.Fatalf("changed reference accepted: %v", err)
			}
		})
	}
}

func TestRawDrivePeriodicProtectionCheckRejectsNewReads(t *testing.T) {
	svc, fake, peer, _ := rawDriveFixture(t, "file.pdf", "application/pdf", []byte("%PDF-1.4 raw drive preview"))
	opened, err := svc.OpenStream(t.Context(), peer.ChannelID, 7)
	if err != nil {
		t.Fatal(err)
	}
	checked := svc.server.session(opened.Token).reader.client.(*externalRangeClient)
	checked.lastRemoteCheck.Store(0)
	fake.SeedMediaSourcePeers(tgclient.SourcePeer{ID: peer.ChannelID, Kind: peer.Kind, AccessHash: peer.AccessHash, Protected: true})
	dst := make([]byte, 8)
	if n, err := checked.ReadDocumentRange(t.Context(), checked.original, 0, dst); n != 0 || !errors.Is(err, ErrExternalRestricted) {
		t.Fatalf("read after protection changed: n=%d err=%v", n, err)
	}
}

type changingRawReference struct {
	tgclient.RangeClient
	change string
}

func (c *changingRawReference) ResolveDocument(ctx context.Context, peer tgclient.InputPeer, id int64) (tgclient.DocumentRef, error) {
	ref, err := c.RangeClient.ResolveDocument(ctx, peer, id)
	if err != nil {
		return ref, err
	}
	ref.PhotoSizeType = "x"
	switch c.change {
	case "photo variant":
		ref.PhotoSizeType = "y"
	case "document":
		ref.DocumentID++
	case "peer kind":
		ref.Peer.Kind = tgclient.PeerChannel
	}
	return ref, nil
}

func TestRawDriveDocumentRejectionRefreshesAfterMetadataSucceeded(t *testing.T) {
	svc, fake, peer, _ := rawDriveFixture(t, "file.pdf", "application/pdf", []byte("%PDF-1.4 raw drive preview"))
	resolver := &refreshingDrivePeer{stale: peer, fresh: peer}
	resolver.stale.AccessHash = 99
	svc.peers = resolver
	svc.rawDrive = &metadataAcceptsPeer{RawDriveSource: fake, hash: peer.AccessHash}
	svc.ranges = &staleRangeClient{RangeClient: fake, expectedHash: peer.AccessHash}
	if _, err := svc.OpenStream(t.Context(), peer.ChannelID, 7); err != nil {
		t.Fatalf("metadata succeeded but document rejected stale peer: %v", err)
	}
	if resolver.refreshes.Load() != 1 {
		t.Fatalf("refresh calls=%d want one", resolver.refreshes.Load())
	}
}

type metadataAcceptsPeer struct {
	RawDriveSource
	hash int64
}

func (c *metadataAcceptsPeer) GetMediaSourcePeer(ctx context.Context, peer tgclient.InputPeer) (tgclient.SourcePeer, error) {
	peer.AccessHash = c.hash
	return c.RawDriveSource.GetMediaSourcePeer(ctx, peer)
}
