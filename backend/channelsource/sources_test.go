package channelsource

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"TDrive/backend/media"
	"TDrive/backend/projection"
	"TDrive/backend/tgclient"

	_ "modernc.org/sqlite"
)

func connectTyped(t *testing.T, sources *Service, kind tgclient.PeerKind, id int64, username string) SourceInfo {
	t.Helper()
	connected, err := sources.ConnectSourceWithGate(t.Context(), string(kind), id, username, testAccountID,
		func(save func() error) error { return save() })
	if err != nil {
		t.Fatal(err)
	}
	if !connected.Connected || connected.Generation == "" || connected.PeerKind != string(kind) || connected.PeerID != id {
		t.Fatalf("invalid connection: %#v", connected)
	}
	return connected
}

func seedTypedVideo(fake *tgclient.Fake, kind tgclient.PeerKind, peerID, msgID, documentID int64, body []byte) {
	fake.SeedHistory(tgclient.HistoryMessage{PeerKind: kind, ChannelID: peerID, MsgID: msgID,
		HasMedia: true, DocumentID: documentID, DocumentAccessHash: documentID + 1,
		DocumentName: "clip.mp4", MimeType: "video/mp4", MediaSize: int64(len(body))})
	fake.SeedSourceDocumentBody(kind, peerID, msgID, body)
}

func readOpened(t *testing.T, url string, want []byte) {
	t.Helper()
	request, err := http.NewRequestWithContext(t.Context(), http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Range", "bytes=0-7")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	actual, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusPartialContent || !bytes.Equal(actual, want[:8]) {
		t.Fatalf("range status %d body %q, want %q", response.StatusCode, actual, want[:8])
	}
}

func TestTypedSourcesIsolateEqualIDsAndAccount(t *testing.T) {
	ctx := t.Context()
	fake := tgclient.NewFake(testAccountID)
	const peerID, msgID = int64(7711), int64(42)
	fake.SeedMediaSourcePeers(
		tgclient.SourcePeer{Kind: tgclient.PeerUser, ID: peerID, AccessHash: 100, Title: "Alice"},
		tgclient.SourcePeer{Kind: tgclient.PeerGroup, ID: peerID, Title: "Friends"},
		tgclient.SourcePeer{Kind: tgclient.PeerSupergroup, ID: peerID, AccessHash: 101, Title: "Forum"},
		tgclient.SourcePeer{Kind: tgclient.PeerSelf, ID: testAccountID, Title: "Saved Messages"},
	)
	bodies := map[tgclient.PeerKind][]byte{
		tgclient.PeerUser:       bytes.Repeat([]byte("user"), 128),
		tgclient.PeerGroup:      bytes.Repeat([]byte("group"), 128),
		tgclient.PeerSupergroup: bytes.Repeat([]byte("forum"), 128),
	}
	for kind, body := range bodies {
		seedTypedVideo(fake, kind, peerID, msgID, int64(len(body)), body)
	}
	seedTypedVideo(fake, tgclient.PeerSelf, testAccountID, msgID, 990, bytes.Repeat([]byte("saved"), 128))
	sources, _, _ := sourceFixture(t, fake, fake)
	candidates, err := sources.ListSourceCandidates(ctx)
	if err != nil || len(candidates) != 4 {
		t.Fatalf("candidates = %#v, %v", candidates, err)
	}
	opened := make(map[tgclient.PeerKind]media.OpenResult)
	for kind, body := range bodies {
		connected := connectTyped(t, sources, kind, peerID, "")
		page, err := sources.PageSource(ctx, string(kind), peerID, 0, 10, "", "video")
		if err != nil || len(page.Items) != 1 || page.PeerKind != string(kind) || page.PeerID != peerID {
			t.Fatalf("%s page = %#v, %v", kind, page, err)
		}
		result, err := sources.OpenSourceWithGate(ctx, string(kind), peerID, msgID, testAccountID, connected.Generation,
			func(add func() error) error { return add() })
		if err != nil {
			t.Fatalf("%s open: %v", kind, err)
		}
		if result.Info.SourcePeerKind != string(kind) || result.Info.SourceAccountID != testAccountID {
			t.Fatalf("%s source info = %#v", kind, result.Info)
		}
		readOpened(t, result.URL, body)
		opened[kind] = result
	}
	self := connectTyped(t, sources, tgclient.PeerSelf, testAccountID, "")
	if _, err := sources.PageSource(ctx, self.PeerKind, self.PeerID, 0, 10, "", "video"); err != nil {
		t.Fatal(err)
	}
	all, err := sources.ListAllConnected(ctx)
	if err != nil || len(all) != 4 {
		t.Fatalf("connected = %#v, %v", all, err)
	}
	userGeneration := ""
	for _, source := range all {
		if source.PeerKind == string(tgclient.PeerUser) {
			userGeneration = source.Generation
		}
	}
	tokens, err := sources.DisconnectSourceWithGate(ctx, string(tgclient.PeerUser), peerID, testAccountID,
		userGeneration, func(remove func() error) error { return remove() })
	if err != nil || len(tokens) != 1 || tokens[0] != opened[tgclient.PeerUser].Token {
		t.Fatalf("disconnect user tokens = %v, %v", tokens, err)
	}
	response, err := http.Get(opened[tgclient.PeerUser].URL)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusNotFound {
		t.Fatalf("revoked user URL status = %d", response.StatusCode)
	}
	readOpened(t, opened[tgclient.PeerGroup].URL, bodies[tgclient.PeerGroup])
	fake.SetSelfID(testAccountID + 1)
	if other, err := sources.ListAllConnected(ctx); err != nil || len(other) != 0 {
		t.Fatalf("other account = %#v, %v", other, err)
	}
	if len(fake.LeftChannels()) != 0 {
		t.Fatal("disconnect left a Telegram chat")
	}
}

func TestCandidatePickerExcludesDrivesWithoutHidingSameIDGroup(t *testing.T) {
	fake := tgclient.NewFake(testAccountID)
	fake.SeedMediaSourcePeers(
		tgclient.SourcePeer{Kind: tgclient.PeerChannel, ID: 42, AccessHash: 91, Title: "Alpha"},
		tgclient.SourcePeer{Kind: tgclient.PeerGroup, ID: 42, Title: "Alpha"},
		tgclient.SourcePeer{Kind: tgclient.PeerSupergroup, ID: 43, AccessHash: 92, Title: "Alpha"},
		tgclient.SourcePeer{Kind: tgclient.PeerUser, ID: 44, AccessHash: 93, Title: "Alpha"},
		tgclient.SourcePeer{Kind: tgclient.PeerBot, ID: 45, AccessHash: 94, Title: "Restricted", Restricted: true},
	)
	sources, _, db := sourceFixture(t, fake, fake)
	if err := projection.InsertChannel(db, projection.Channel{ChannelID: 42, AccessHash: 91, Title: "Alpha", Kind: projection.KindPersonal}); err != nil {
		t.Fatal(err)
	}
	candidates, err := sources.ListSourceCandidates(t.Context())
	if err != nil || len(candidates) != 3 {
		t.Fatalf("candidates = %#v, %v", candidates, err)
	}
	for index, kind := range []string{"group", "supergroup", "user"} {
		if candidates[index].PeerKind != kind {
			t.Fatalf("candidate %d = %s, want %s", index, candidates[index].PeerKind, kind)
		}
	}
	if _, err := sources.ConnectSourceWithGate(t.Context(), "channel", 42, "", testAccountID,
		func(save func() error) error { return save() }); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("drive connect = %v", err)
	}
	connectTyped(t, sources, tgclient.PeerGroup, 42, "")
}

func TestPublicChannelResolveConnectAndReadWithoutJoining(t *testing.T) {
	fake := tgclient.NewFake(testAccountID)
	fake.SeedPublicChannel("publicfilms", tgclient.SourcePeer{ID: 8811, AccessHash: 898, Title: "Public Films"})
	seedTypedVideo(fake, tgclient.PeerChannel, 8811, 7, 707, bytes.Repeat([]byte("film"), 128))
	sources, _, _ := sourceFixture(t, fake, fake)
	for _, input := range []string{"@publicfilms", "t.me/publicfilms", "https://t.me/publicfilms/7"} {
		candidate, err := sources.ResolvePublicSource(t.Context(), input)
		if err != nil || candidate.PeerKind != "channel" || candidate.PeerID != 8811 {
			t.Fatalf("%q = %#v, %v", input, candidate, err)
		}
	}
	for _, input := range []string{"https://t.me/c/8811/7", "https://t.me/+invite", "https://evil.test/publicfilms", "@a", "https://t.me/publicfilms?start=1"} {
		if _, err := sources.ResolvePublicSource(t.Context(), input); !errors.Is(err, ErrInvalidPublicLink) {
			t.Errorf("%q: %v", input, err)
		}
	}
	connected := connectTyped(t, sources, tgclient.PeerChannel, 8811, "publicfilms")
	page, err := sources.PageSource(t.Context(), "channel", 8811, 0, 10, "", "video")
	if err != nil || len(page.Items) != 1 || page.Items[0].TelegramURL != "https://t.me/publicfilms/7" {
		t.Fatalf("public page = %#v, %v", page, err)
	}
	result, err := sources.OpenSourceWithGate(t.Context(), "channel", 8811, 7, testAccountID, connected.Generation,
		func(add func() error) error { return add() })
	if err != nil {
		t.Fatal(err)
	}
	readOpened(t, result.URL, bytes.Repeat([]byte("film"), 128))
	if len(fake.LeftChannels()) != 0 {
		t.Fatal("public source changed membership")
	}

	reassigned := tgclient.NewFake(testAccountID)
	reassigned.SeedPublicChannel("publicfilms", tgclient.SourcePeer{ID: 8811, AccessHash: 898, Title: "Public Films"})
	otherSources, _, _ := sourceFixture(t, reassigned, reassigned)
	selected, err := otherSources.ResolvePublicSource(t.Context(), "@publicfilms")
	if err != nil {
		t.Fatal(err)
	}
	reassigned.SeedPublicChannel("publicfilms", tgclient.SourcePeer{ID: 9911, AccessHash: 999, Title: "Reassigned"})
	_, err = otherSources.ConnectSourceWithGate(t.Context(), selected.PeerKind, selected.PeerID, selected.Username,
		selected.AccountID, func(save func() error) error { return save() })
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("stale public handle connect = %v", err)
	}
	all, err := otherSources.ListAllConnected(t.Context())
	if err != nil || len(all) != 0 {
		t.Fatalf("reassigned handle connected a peer: %#v, %v", all, err)
	}
}

type deniedRange struct{ tgclient.RangeClient }

func (d deniedRange) ReadDocumentRange(context.Context, tgclient.DocumentRef, int64, []byte) (int, error) {
	return 0, tgclient.ErrChannelUnavailable
}

func TestPublicChannelMustServeBytesBeforeSessionPublication(t *testing.T) {
	fake := tgclient.NewFake(testAccountID)
	fake.SeedPublicChannel("publicfilms", tgclient.SourcePeer{ID: 8811, AccessHash: 898, Title: "Public Films"})
	seedTypedVideo(fake, tgclient.PeerChannel, 8811, 7, 707, bytes.Repeat([]byte("film"), 128))
	sources, _, _ := sourceFixture(t, fake, deniedRange{fake})
	connected := connectTyped(t, sources, tgclient.PeerChannel, 8811, "publicfilms")
	_, err := sources.OpenSourceWithGate(t.Context(), "channel", 8811, 7, testAccountID, connected.Generation,
		func(add func() error) error { return add() })
	if !errors.Is(err, tgclient.ErrChannelUnavailable) {
		t.Fatalf("unreadable public document = %v", err)
	}
}

func TestProtectedGroupBotAndDMPostsStayOutsideMediaSessions(t *testing.T) {
	fake := tgclient.NewFake(testAccountID)
	fake.SeedMediaSourcePeers(
		tgclient.SourcePeer{Kind: tgclient.PeerGroup, ID: 80, Title: "Protected group", Protected: true},
		tgclient.SourcePeer{Kind: tgclient.PeerBot, ID: 81, AccessHash: 98, Title: "Bot", Username: "namedbot"},
		tgclient.SourcePeer{Kind: tgclient.PeerUser, ID: 82, AccessHash: 99, Title: "Alice"},
	)
	seedTypedVideo(fake, tgclient.PeerGroup, 80, 5, 500, []byte("protected group bytes"))
	fake.SeedHistory(tgclient.HistoryMessage{PeerKind: tgclient.PeerBot, ChannelID: 81, MsgID: 5,
		HasMedia: true, DocumentID: 501, DocumentName: "clip.mp4", MediaSize: 20, NoForwards: true})
	fake.SeedHistory(tgclient.HistoryMessage{PeerKind: tgclient.PeerUser, ChannelID: 82, MsgID: 5,
		HasMedia: true, DocumentID: 502, DocumentName: "clip.mp4", MediaSize: 20, NoForwards: true})
	sources, _, _ := sourceFixture(t, fake, fake)
	for _, peer := range []struct {
		kind tgclient.PeerKind
		id   int64
	}{{tgclient.PeerGroup, 80}, {tgclient.PeerBot, 81}, {tgclient.PeerUser, 82}} {
		username := ""
		if peer.kind == tgclient.PeerBot {
			username = "namedbot"
		}
		connected := connectTyped(t, sources, peer.kind, peer.id, username)
		page, err := sources.PageSource(t.Context(), string(peer.kind), peer.id, 0, 10, "", "video")
		if err != nil || len(page.Items) != 1 || page.Items[0].Streamable || page.Items[0].BlockReason != "protected" {
			t.Fatalf("%s protected page = %#v, %v", peer.kind, page, err)
		}
		_, err = sources.OpenSourceWithGate(t.Context(), string(peer.kind), peer.id, 5, testAccountID, connected.Generation,
			func(add func() error) error { return add() })
		if !errors.Is(err, media.ErrExternalRestricted) {
			t.Fatalf("%s open = %v", peer.kind, err)
		}
	}
}

func TestLegacyChannelMigrationPreservesGenerationWithoutResurrection(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	if err := projection.EnsureSchema(db); err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`CREATE TABLE connected_channel_sources (
		account_id INTEGER NOT NULL, channel_id INTEGER NOT NULL, title TEXT NOT NULL,
		username TEXT NOT NULL, protected INTEGER NOT NULL, generation TEXT NOT NULL,
		access_hash INTEGER NOT NULL, photo_id INTEGER NOT NULL, photo BLOB,
		PRIMARY KEY(account_id, channel_id));
		INSERT INTO connected_channel_sources VALUES (1234, 7711, 'Cinema', 'cinema', 0, 'kept-generation', 0, 501, x'010203');`)
	if err != nil {
		t.Fatal(err)
	}
	fake := tgclient.NewFake(testAccountID)
	fake.SeedMediaSourcePeers(tgclient.SourcePeer{Kind: tgclient.PeerChannel, ID: 7711, AccessHash: 77, Title: "Cinema", Username: "cinema", PhotoID: 501})
	stream := media.NewService(media.Config{DB: db, Ranges: fake})
	t.Cleanup(func() { _ = stream.Close() })
	sources, err := NewService(db, fake, stream, fake.SelfID)
	if err != nil {
		t.Fatal(err)
	}
	all, err := sources.ListAllConnected(t.Context())
	if err != nil || len(all) != 1 || all[0].Generation != "kept-generation" || all[0].PeerKind != "channel" {
		t.Fatalf("migrated sources = %#v, %v", all, err)
	}
	photo, err := sources.SourcePhoto(t.Context(), "channel", 7711, testAccountID, "kept-generation")
	if err != nil || !bytes.Equal(photo, []byte{1, 2, 3}) {
		t.Fatalf("migrated photo = %v, %v", photo, err)
	}
	if _, err := sources.PageSource(t.Context(), "channel", 7711, 0, 10, "", "all"); err != nil {
		t.Fatal(err)
	}
	var restoredHash int64
	if err := db.QueryRow(`SELECT access_hash FROM connected_media_sources WHERE account_id=1234 AND peer_kind='channel' AND peer_id=7711`).Scan(&restoredHash); err != nil || restoredHash != 77 {
		t.Fatalf("restored channel hash = %d, %v", restoredHash, err)
	}
	_, err = sources.DisconnectSourceWithGate(t.Context(), "channel", 7711, testAccountID, "kept-generation",
		func(remove func() error) error { return remove() })
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewService(db, fake, stream, fake.SelfID); err != nil {
		t.Fatal(err)
	}
	all, err = sources.ListAllConnected(t.Context())
	if err != nil || len(all) != 0 {
		t.Fatalf("resurrected sources = %#v, %v", all, err)
	}
}

func TestTypedChannelDisconnectAlsoRevokesLegacyChannelSession(t *testing.T) {
	fake := tgclient.NewFake(testAccountID)
	channel := tgclient.SourcePeer{Kind: tgclient.PeerChannel, ID: testChannelID, AccessHash: 77, Title: "Cinema"}
	fake.SeedJoinedBroadcastChannels(channel)
	fake.SeedMediaSourcePeers(channel)
	seedVideo(fake, testChannelID, 8, bytes.Repeat([]byte("film"), 128))
	sources, _, _ := sourceFixture(t, fake, fake)
	legacy, err := sources.Connect(t.Context(), testChannelID)
	if err != nil {
		t.Fatal(err)
	}
	opened, err := sources.Open(t.Context(), testChannelID, 8, legacy.AccountID, legacy.Generation)
	if err != nil {
		t.Fatal(err)
	}
	connected := connectTyped(t, sources, tgclient.PeerChannel, testChannelID, "")
	tokens, err := sources.DisconnectSourceWithGate(t.Context(), "channel", testChannelID, testAccountID,
		connected.Generation, func(remove func() error) error { return remove() })
	if err != nil || len(tokens) != 1 || tokens[0] != opened.Token {
		t.Fatalf("legacy token = %v, %v", tokens, err)
	}
	response, err := http.Get(opened.URL)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusNotFound {
		t.Fatalf("legacy URL status = %d", response.StatusCode)
	}
}

func TestTypedSourcePhotoIsCachedAndReplacedWithinItsPeer(t *testing.T) {
	fake := tgclient.NewFake(testAccountID)
	fake.SeedMediaSourcePeers(
		tgclient.SourcePeer{Kind: tgclient.PeerUser, ID: 42, AccessHash: 100, Title: "Alice", PhotoID: 501},
		tgclient.SourcePeer{Kind: tgclient.PeerGroup, ID: 42, Title: "Friends", PhotoID: 502},
	)
	fake.SeedChannelPhoto(501, []byte("alice picture"))
	fake.SeedChannelPhoto(502, []byte("group picture"))
	fake.SeedChannelPhoto(503, []byte("new alice picture"))
	sources, _, _ := sourceFixture(t, fake, fake)
	userSource := connectTyped(t, sources, tgclient.PeerUser, 42, "")
	groupSource := connectTyped(t, sources, tgclient.PeerGroup, 42, "")
	for range 2 {
		user, err := sources.SourcePhoto(t.Context(), "user", 42, testAccountID, userSource.Generation)
		if err != nil || string(user) != "alice picture" {
			t.Fatalf("user photo = %q, %v", user, err)
		}
		group, err := sources.SourcePhoto(t.Context(), "group", 42, testAccountID, groupSource.Generation)
		if err != nil || string(group) != "group picture" {
			t.Fatalf("group photo = %q, %v", group, err)
		}
	}
	if fake.PhotoDownloads() != 2 {
		t.Fatalf("photo downloads = %d, want 2", fake.PhotoDownloads())
	}
	fake.SeedMediaSourcePeers(
		tgclient.SourcePeer{Kind: tgclient.PeerUser, ID: 42, AccessHash: 100, Title: "Alice", PhotoID: 503},
		tgclient.SourcePeer{Kind: tgclient.PeerGroup, ID: 42, Title: "Friends", PhotoID: 502},
	)
	if _, err := sources.PageSource(t.Context(), "user", 42, 0, 10, "", "all"); err != nil {
		t.Fatal(err)
	}
	user, err := sources.SourcePhoto(t.Context(), "user", 42, testAccountID, userSource.Generation)
	if err != nil || string(user) != "new alice picture" || fake.PhotoDownloads() != 3 {
		t.Fatalf("changed user photo = %q, %v; downloads %d", user, err, fake.PhotoDownloads())
	}
	group, err := sources.SourcePhoto(t.Context(), "group", 42, testAccountID, groupSource.Generation)
	if err != nil || string(group) != "group picture" {
		t.Fatalf("unaffected group photo = %q, %v", group, err)
	}
}

type blockingPhotoClient struct {
	*tgclient.Fake
	entered chan struct{}
	release chan struct{}
}

func (c *blockingPhotoClient) DownloadChannelPhoto(ctx context.Context, peer tgclient.InputPeer, photoID int64) ([]byte, error) {
	select {
	case c.entered <- struct{}{}:
	default:
	}
	select {
	case <-c.release:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	return c.Fake.DownloadChannelPhoto(ctx, peer, photoID)
}

func TestPhotoRejectsLateAccountOrConnectionChange(t *testing.T) {
	for _, change := range []string{"account", "generation"} {
		t.Run(change, func(t *testing.T) {
			fake := tgclient.NewFake(testAccountID)
			fake.SeedMediaSourcePeers(tgclient.SourcePeer{Kind: tgclient.PeerUser, ID: 42, AccessHash: 100, Title: "Alice", PhotoID: 501})
			fake.SeedChannelPhoto(501, []byte("picture"))
			client := &blockingPhotoClient{Fake: fake, entered: make(chan struct{}, 1), release: make(chan struct{})}
			sources, _, _ := sourceFixture(t, client, fake)
			connected := connectTyped(t, sources, tgclient.PeerUser, 42, "")
			completed := make(chan error, 1)
			go func() {
				_, err := sources.SourcePhoto(t.Context(), "user", 42, testAccountID, connected.Generation)
				completed <- err
			}()
			select {
			case <-client.entered:
			case <-time.After(2 * time.Second):
				t.Fatal("photo did not reach Telegram")
			}
			if change == "account" {
				fake.SetSelfID(testAccountID + 1)
			} else {
				_, err := sources.DisconnectSourceWithGate(t.Context(), "user", 42, testAccountID, connected.Generation,
					func(remove func() error) error { return remove() })
				if err != nil {
					t.Fatal(err)
				}
			}
			close(client.release)
			select {
			case err := <-completed:
				want := ErrUnavailable
				if change == "generation" {
					want = ErrNotConnected
				}
				if !errors.Is(err, want) {
					t.Fatalf("late photo = %v, want %v", err, want)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("photo did not finish")
			}
		})
	}
}

func TestTypedPageRejectsMissingAndCanceledReads(t *testing.T) {
	fake := tgclient.NewFake(testAccountID)
	fake.SeedMediaSourcePeers(tgclient.SourcePeer{Kind: tgclient.PeerUser, ID: 80, AccessHash: 90, Title: "Alice"})
	sources, _, _ := sourceFixture(t, fake, fake)
	connected := connectTyped(t, sources, tgclient.PeerUser, 80, "")
	_, err := sources.OpenSourceWithGate(t.Context(), "user", 80, 99, testAccountID, connected.Generation,
		func(add func() error) error { return add() })
	if !errors.Is(err, tgclient.ErrMessageNotFound) {
		t.Fatalf("missing message = %v", err)
	}
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	_, err = sources.PageSource(canceled, "user", 80, 0, 10, "", "video")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled page = %v", err)
	}
	if _, err := sources.ConnectSourceWithGate(t.Context(), "unsupported", 80, "", testAccountID,
		func(save func() error) error { return save() }); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("invalid kind = %v", err)
	}
	if _, err := sources.ResolvePublicSource(t.Context(), strings.Repeat("a", 33)); !errors.Is(err, ErrInvalidPublicLink) {
		t.Fatalf("long handle = %v", err)
	}
}

func TestTypedReferenceRefreshRechecksAccountAndGroupProtection(t *testing.T) {
	for _, test := range []struct {
		name   string
		change func(*tgclient.Fake)
	}{
		{"protection enabled", func(fake *tgclient.Fake) {
			fake.SeedMediaSourcePeers(tgclient.SourcePeer{Kind: tgclient.PeerGroup, ID: 80, Title: "Group", Protected: true})
		}},
		{"account changed", func(fake *tgclient.Fake) { fake.SetSelfID(testAccountID + 1) }},
	} {
		t.Run(test.name, func(t *testing.T) {
			fake := tgclient.NewFake(testAccountID)
			fake.SeedMediaSourcePeers(tgclient.SourcePeer{Kind: tgclient.PeerGroup, ID: 80, Title: "Group"})
			body := bytes.Repeat([]byte("group"), 128)
			seedTypedVideo(fake, tgclient.PeerGroup, 80, 9, 901, body)
			ranges := &rangeFault{RangeClient: fake}
			sources, _, _ := sourceFixture(t, fake, ranges)
			connected := connectTyped(t, sources, tgclient.PeerGroup, 80, "")
			opened, err := sources.OpenSourceWithGate(t.Context(), "group", 80, 9, testAccountID, connected.Generation,
				func(add func() error) error { return add() })
			if err != nil {
				t.Fatal(err)
			}
			test.change(fake)
			response, err := http.Get(opened.URL)
			if err != nil {
				t.Fatal(err)
			}
			actual, err := io.ReadAll(response.Body)
			response.Body.Close()
			if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) {
				t.Fatal(err)
			}
			if bytes.Equal(actual, body) || ranges.resolves.Load() < 1 {
				t.Fatalf("changed source served body; status=%d resolves=%d", response.StatusCode, ranges.resolves.Load())
			}
		})
	}
}
