package channelsource

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"image"
	"image/jpeg"
	"io"
	"net/http"
	"slices"
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
	if walks, lookups := fake.TelegramReads(); walks != 1 || lookups != 1 {
		t.Fatalf("picker followed by connect made %d dialog walks and %d peer checks, want 1 each", walks, lookups)
	}
}

func TestCandidatePickerReusesPeerForPhotoAndRejectsRevokedAccess(t *testing.T) {
	fake := tgclient.NewFake(testAccountID)
	fake.SeedMediaSourcePeers(
		tgclient.SourcePeer{Kind: tgclient.PeerBot, ID: 52, AccessHash: 92, Title: "Search bot", PhotoID: 500},
		tgclient.SourcePeer{Kind: tgclient.PeerUser, ID: 53, AccessHash: 93, Title: "Alice"},
	)
	fake.SeedChannelPhoto(500, []byte("bot picture"))
	sources, _, _ := sourceFixture(t, fake, fake)
	if _, err := sources.ListSourceCandidates(t.Context()); err != nil {
		t.Fatal(err)
	}
	photo, err := sources.SourcePhoto(t.Context(), "bot", 52, testAccountID, "")
	if err != nil || !bytes.Equal(photo, []byte("bot picture")) {
		t.Fatalf("picker photo = %q, %v", photo, err)
	}
	// Telegram can revoke a peer after it was shown. A cached hash must
	// still require a fresh single-peer check before connection.
	fake.SeedMediaSourcePeers(tgclient.SourcePeer{Kind: tgclient.PeerUser, ID: 53, AccessHash: 93, Title: "Alice"})
	_, err = sources.ConnectSourceWithGate(t.Context(), "bot", 52, "", testAccountID,
		func(save func() error) error { return save() })
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("revoked candidate connect = %v, want ErrUnavailable", err)
	}
	if walks, lookups := fake.TelegramReads(); walks != 1 || lookups != 1 {
		t.Fatalf("picker, photo and revoked connect made %d dialog walks and %d peer checks, want 1 each", walks, lookups)
	}
	if all, err := sources.ListAllConnected(t.Context()); err != nil || len(all) != 0 {
		t.Fatalf("revoked candidate connected: %#v, %v", all, err)
	}
}

func TestCandidatePickerCacheStaysBoundedWithSavedMessages(t *testing.T) {
	fake := tgclient.NewFake(testAccountID)
	peers := make([]tgclient.SourcePeer, 0, maxCachedCandidates)
	for id := int64(1); id < maxCachedCandidates; id++ {
		peers = append(peers, tgclient.SourcePeer{Kind: tgclient.PeerBot, ID: id, AccessHash: id + 1, Title: "Bot"})
	}
	peers = append(peers, tgclient.SourcePeer{Kind: tgclient.PeerSelf, ID: testAccountID, Title: "Saved Messages"})
	fake.SeedMediaSourcePeers(peers...)
	sources, _, _ := sourceFixture(t, fake, fake)
	if candidates, err := sources.ListSourceCandidates(t.Context()); err != nil || len(candidates) != maxCachedCandidates {
		t.Fatalf("large picker = %d candidates, %v", len(candidates), err)
	}
	fake.SeedPublicChannel("publicfilms", tgclient.SourcePeer{ID: 9000, AccessHash: 9001, Title: "Public Films"})
	if _, err := sources.ResolvePublicSource(t.Context(), "@publicfilms"); err != nil {
		t.Fatal(err)
	}
	connectTyped(t, sources, tgclient.PeerBot, 1, "")
	connectTyped(t, sources, tgclient.PeerSelf, testAccountID, "")
	if walks, lookups := fake.TelegramReads(); walks != 1 || lookups != 2 {
		t.Fatalf("large picker connects made %d walks and %d peer checks, want 1 walk and 2 checks", walks, lookups)
	}
	if got := len(sources.candidates); got > maxCachedCandidates {
		t.Fatalf("candidate cache has %d entries, want at most %d", got, maxCachedCandidates)
	}
}

func TestCandidatePickerDoesNotCrossAccountOrLogoutGate(t *testing.T) {
	fake := tgclient.NewFake(testAccountID)
	fake.SeedMediaSourcePeers(tgclient.SourcePeer{Kind: tgclient.PeerBot, ID: 52, AccessHash: 92, Title: "Search bot"})
	sources, _, db := sourceFixture(t, fake, fake)
	if _, err := sources.ListSourceCandidates(t.Context()); err != nil {
		t.Fatal(err)
	}
	fake.SetSelfID(testAccountID + 1)
	fake.SeedMediaSourcePeers(tgclient.SourcePeer{Kind: tgclient.PeerBot, ID: 52, AccessHash: 193, Title: "Other account bot"})
	connected, err := sources.ConnectSourceWithGate(t.Context(), "bot", 52, "", testAccountID+1,
		func(save func() error) error { return save() })
	if err != nil || !connected.Connected {
		t.Fatalf("other account connect = %#v, %v", connected, err)
	}
	var hash int64
	if err := db.QueryRow(`SELECT access_hash FROM connected_media_sources WHERE account_id=? AND peer_kind='bot' AND peer_id=52`, testAccountID+1).Scan(&hash); err != nil || hash != 193 {
		t.Fatalf("other account stored hash = %d, %v", hash, err)
	}
	if walks, _ := fake.TelegramReads(); walks != 2 {
		t.Fatalf("account switch made %d dialog walks, want separate walk for each account", walks)
	}
	fake.SetSelfID(testAccountID)
	fake.SeedMediaSourcePeers(tgclient.SourcePeer{Kind: tgclient.PeerBot, ID: 53, AccessHash: 94, Title: "Logout bot"})
	_, err = sources.ConnectSourceWithGate(t.Context(), "bot", 53, "", testAccountID,
		func(save func() error) error {
			fake.SetSelfID(testAccountID + 2)
			return save()
		})
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("account switch inside connect gate = %v, want ErrUnavailable", err)
	}
	if all, err := sources.ListAllConnected(t.Context()); err != nil || len(all) != 0 {
		t.Fatalf("new account has connections after logout race: %#v, %v", all, err)
	}
}

func TestPublicChannelResolveConnectAndReadWithoutJoining(t *testing.T) {
	fake := tgclient.NewFake(testAccountID)
	fake.SeedPublicChannel("publicfilms", tgclient.SourcePeer{ID: 8811, AccessHash: 898, Title: "Public Films", PhotoID: 510})
	fake.SeedChannelPhoto(510, []byte("public picture"))
	seedTypedVideo(fake, tgclient.PeerChannel, 8811, 7, 707, bytes.Repeat([]byte("film"), 128))
	sources, _, _ := sourceFixture(t, fake, fake)
	for _, input := range []string{"@publicfilms", "t.me/publicfilms", "https://t.me/publicfilms/7"} {
		candidate, err := sources.ResolvePublicSource(t.Context(), input)
		if err != nil || candidate.PeerKind != "channel" || candidate.PeerID != 8811 {
			t.Fatalf("%q = %#v, %v", input, candidate, err)
		}
	}
	photo, err := sources.SourcePhoto(t.Context(), "channel", 8811, testAccountID, "")
	if err != nil || !bytes.Equal(photo, []byte("public picture")) {
		t.Fatalf("unconnected public photo = %q, %v", photo, err)
	}
	if walks, _ := fake.TelegramReads(); walks != 0 {
		t.Fatalf("public avatar required %d dialog walks, want none", walks)
	}
	for _, input := range []string{"https://t.me/c/8811/7", "https://t.me/+invite", "https://evil.test/publicfilms", "@a", "https://t.me/publicfilms?start=1"} {
		if _, err := sources.ResolvePublicSource(t.Context(), input); !errors.Is(err, ErrInvalidPublicLink) {
			t.Errorf("%q: %v", input, err)
		}
	}
	if _, err := sources.ResolvePublicSource(t.Context(), "@notapublicchannel"); !errors.Is(err, ErrPublicChannelUnavailable) {
		t.Fatalf("unavailable public handle = %v, want user-facing error", err)
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

func TestProtectedGroupBotAndDMPostsRemainViewable(t *testing.T) {
	fake := tgclient.NewFake(testAccountID)
	fake.SeedMediaSourcePeers(
		tgclient.SourcePeer{Kind: tgclient.PeerGroup, ID: 80, Title: "Protected group", Protected: true},
		tgclient.SourcePeer{Kind: tgclient.PeerBot, ID: 81, AccessHash: 98, Title: "Bot", Username: "namedbot"},
		tgclient.SourcePeer{Kind: tgclient.PeerUser, ID: 82, AccessHash: 99, Title: "Alice"},
	)
	seedTypedVideo(fake, tgclient.PeerGroup, 80, 5, 500, []byte("protected group bytes"))
	fake.SeedHistory(tgclient.HistoryMessage{PeerKind: tgclient.PeerBot, ChannelID: 81, MsgID: 5,
		HasMedia: true, DocumentID: 501, DocumentName: "clip.mp4", MediaSize: 20, NoForwards: true})
	fake.SeedSourceDocumentBody(tgclient.PeerBot, 81, 5, []byte("protected bot bytes!"))
	fake.SeedHistory(tgclient.HistoryMessage{PeerKind: tgclient.PeerUser, ChannelID: 82, MsgID: 5,
		HasMedia: true, DocumentID: 502, DocumentName: "clip.mp4", MediaSize: 20, NoForwards: true})
	fake.SeedSourceDocumentBody(tgclient.PeerUser, 82, 5, []byte("protected user bytes"))
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
		if err != nil || len(page.Items) != 1 || !page.Items[0].Streamable || !page.Items[0].Protected || page.Items[0].BlockReason != "" {
			t.Fatalf("%s protected page = %#v, %v", peer.kind, page, err)
		}
		opened, err := sources.OpenSourceWithGate(t.Context(), string(peer.kind), peer.id, 5, testAccountID, connected.Generation,
			func(add func() error) error { return add() })
		if err != nil || !opened.Info.Protected || opened.ThumbnailURL != "" {
			t.Fatalf("%s protected open = %#v, %v", peer.kind, opened, err)
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

type sourceViewerCase struct {
	name, filename, mime, kind, filter string
	body                               []byte
}

type sourceViewerScope struct {
	name                                 string
	peer                                 tgclient.PeerKind
	legacy, peerProtected, postProtected bool
}

func TestSourceViewersServeAllSupportedKinds(t *testing.T) {
	var photo bytes.Buffer
	if err := jpeg.Encode(&photo, image.NewRGBA(image.Rect(0, 0, 2, 2)), nil); err != nil {
		t.Fatal(err)
	}
	for _, viewer := range []sourceViewerCase{
		{"PDF", "", "application/pdf", "pdf", "document", []byte("%PDF-1.4 source preview")},
		{"text", "notes.md", "text/markdown", "text", "document", []byte("# Source notes\npreview")},
		{"photo", "Telegram photo 9.jpg", "image/jpeg", "image", "image", photo.Bytes()},
		{"audio", "voice.mp3", "audio/mpeg", "audio", "audio", []byte("audio preview bytes")},
		{"video", "clip.mp4", "video/mp4", "video", "video", []byte("video preview bytes")},
	} {
		for _, scope := range []sourceViewerScope{
			{"typed", tgclient.PeerGroup, false, false, false},
			{"typed protected peer", tgclient.PeerGroup, false, true, false},
			{"typed protected post", tgclient.PeerGroup, false, false, true},
			{"legacy", tgclient.PeerChannel, true, false, false},
			{"legacy protected peer", tgclient.PeerChannel, true, true, false},
			{"legacy protected post", tgclient.PeerChannel, true, false, true},
		} {
			t.Run(viewer.name+"/"+scope.name, func(t *testing.T) { testSourceViewer(t, viewer, scope) })
		}
	}
}

func testSourceViewer(t *testing.T, viewer sourceViewerCase, scope sourceViewerScope) {
	t.Helper()
	fake := tgclient.NewFake(testAccountID)
	fake.SeedMediaSourcePeers(tgclient.SourcePeer{Kind: scope.peer, ID: 80, AccessHash: 77, Title: "Files", Protected: scope.peerProtected})
	fake.SeedJoinedBroadcastChannels(tgclient.JoinedBroadcastChannel{ID: 80, AccessHash: 77, Title: "Files", Protected: scope.peerProtected})
	fake.SeedHistory(tgclient.HistoryMessage{PeerKind: scope.peer, ChannelID: 80, MsgID: 9,
		HasMedia: true, DocumentID: 901, DocumentAccessHash: 902, MediaSize: int64(len(viewer.body)),
		DocumentName: viewer.filename, MimeType: viewer.mime, NoForwards: scope.postProtected})
	fake.SeedSourceDocumentBody(scope.peer, 80, 9, viewer.body)
	if scope.legacy {
		fake.SeedDocumentBody(9, viewer.body)
	}
	var ranges tgclient.RangeClient = fake
	if viewer.name == "photo" {
		ranges = photoRange{RangeClient: fake}
	}
	refreshed := &rangeFault{RangeClient: ranges}
	sources, _, _ := sourceFixture(t, fake, refreshed)
	var connected SourceInfo
	var page MediaPage
	var opened media.OpenResult
	var err error
	if scope.legacy {
		connected, err = sources.Connect(t.Context(), 80)
		if err != nil {
			t.Fatal(err)
		}
		page, err = sources.Page(t.Context(), 80, 0, 10, "", viewer.filter)
	} else {
		connected = connectTyped(t, sources, scope.peer, 80, "")
		page, err = sources.PageSource(t.Context(), string(scope.peer), 80, 0, 10, "", viewer.filter)
	}
	protected := scope.peerProtected || scope.postProtected
	if err != nil || len(page.Items) != 1 || page.Items[0].Kind != viewer.kind || !page.Items[0].Streamable || page.Items[0].Protected != protected {
		t.Fatalf("%s page = %#v, %v", viewer.filter, page, err)
	}
	if scope.legacy {
		opened, err = sources.Open(t.Context(), 80, 9, testAccountID, connected.Generation)
	} else {
		opened, err = sources.OpenSourceWithGate(t.Context(), string(scope.peer), 80, 9, testAccountID, connected.Generation, func(add func() error) error { return add() })
	}
	if err != nil {
		t.Fatal(err)
	}
	if string(opened.Kind) != viewer.kind || opened.Name != page.Items[0].Name || opened.Info.Protected != protected {
		t.Fatalf("open = %#v", opened)
	}
	if protected && opened.ThumbnailURL != "" {
		t.Fatal("protected source published a thumbnail URL")
	}
	readOpened(t, opened.URL, viewer.body)
	request, err := http.NewRequestWithContext(t.Context(), http.MethodGet, opened.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	actual, readErr := io.ReadAll(response.Body)
	response.Body.Close()
	if readErr != nil || response.StatusCode != http.StatusOK || !bytes.Equal(actual, viewer.body) {
		t.Fatalf("HTTP body status=%d bytes=%d error=%v", response.StatusCode, len(actual), readErr)
	}
	if !strings.Contains(response.Header.Get("Cache-Control"), "no-store") {
		t.Fatal("source bytes must not be persisted")
	}
	if refreshed.resolves.Load() < 2 {
		t.Fatal("source reference was not refreshed")
	}
}

// The fake uses document references; this selects the photo transport variant
// returned for these same history fields by the production resolver.
type photoRange struct{ tgclient.RangeClient }

func (c photoRange) ResolveDocument(ctx context.Context, peer tgclient.InputPeer, msgID int64) (tgclient.DocumentRef, error) {
	ref, err := c.RangeClient.ResolveDocument(ctx, peer, msgID)
	ref.PhotoSizeType = "y"
	return ref, err
}

func (c photoRange) ReadDocumentRange(ctx context.Context, ref tgclient.DocumentRef, offset int64, dst []byte) (int, error) {
	if ref.PhotoSizeType != "y" {
		return 0, errors.New("photo read lost the selected JPEG variant")
	}
	return c.RangeClient.ReadDocumentRange(ctx, ref, offset, dst)
}

func TestSourceDocumentAndImageFiltersKeepBlockedAttachments(t *testing.T) {
	fake := tgclient.NewFake(testAccountID)
	fake.SeedJoinedBroadcastChannels(tgclient.JoinedBroadcastChannel{ID: testChannelID, AccessHash: 77, Title: "Files"})
	fake.SeedMediaSourcePeers(tgclient.SourcePeer{Kind: tgclient.PeerChannel, ID: testChannelID, AccessHash: 77, Title: "Files"})
	for _, test := range []struct {
		id             int64
		filename, mime string
		protected      bool
	}{
		{1, "guide.pdf", "application/pdf", false}, {2, "notes.txt", "text/plain", false},
		{3, "data.zip", "application/zip", false}, {4, "photo.jpg", "image/jpeg", false},
		{5, "locked.jpg", "image/jpeg", true}, {6, "clip.mp4", "video/mp4", false},
	} {
		fake.SeedHistory(tgclient.HistoryMessage{PeerKind: tgclient.PeerChannel, ChannelID: testChannelID, MsgID: test.id,
			HasMedia: true, DocumentID: 900 + test.id, DocumentAccessHash: 99, MediaSize: 20,
			DocumentName: test.filename, MimeType: test.mime, NoForwards: test.protected})
	}
	sources, _, _ := sourceFixture(t, fake, fake)
	if _, err := sources.Connect(t.Context(), testChannelID); err != nil {
		t.Fatal(err)
	}
	if _, err := sources.ListSourceCandidates(t.Context()); err != nil {
		t.Fatal(err)
	}
	connectTyped(t, sources, tgclient.PeerChannel, testChannelID, "")
	for _, typed := range []bool{false, true} {
		for _, test := range []struct {
			filter string
			ids    []int64
		}{
			{"document", []int64{3, 2, 1}}, {"image", []int64{5, 4}},
		} {
			var page MediaPage
			var err error
			if typed {
				page, err = sources.PageSource(t.Context(), "channel", testChannelID, 0, 10, "", test.filter)
			} else {
				page, err = sources.Page(t.Context(), testChannelID, 0, 10, "", test.filter)
			}
			if err != nil {
				t.Fatal(err)
			}
			var ids []int64
			for _, item := range page.Items {
				ids = append(ids, item.MsgID)
				if item.MsgID == 3 && (item.Name != "data.zip" || item.Streamable || item.BlockReason != "unsupported_format") {
					t.Fatalf("archive = %#v", item)
				}
				if item.MsgID == 5 && (!item.Streamable || !item.Protected || item.BlockReason != "") {
					t.Fatalf("protected photo = %#v", item)
				}
			}
			if !slices.Equal(ids, test.ids) {
				t.Fatalf("typed=%v filter=%s IDs=%v want=%v", typed, test.filter, ids, test.ids)
			}
		}
	}
}

func TestSourceImageAdmissionRejectsMalformedPhotoBeforePublishing(t *testing.T) {
	fake := tgclient.NewFake(testAccountID)
	fake.SeedMediaSourcePeers(tgclient.SourcePeer{Kind: tgclient.PeerGroup, ID: 80, Title: "Photos"})
	body := []byte("this is not a JPEG image")
	fake.SeedHistory(tgclient.HistoryMessage{PeerKind: tgclient.PeerGroup, ChannelID: 80, MsgID: 9,
		HasMedia: true, DocumentID: 901, DocumentAccessHash: 902, MediaSize: int64(len(body)), DocumentName: "photo.jpg", MimeType: "image/jpeg"})
	fake.SeedSourceDocumentBody(tgclient.PeerGroup, 80, 9, body)
	sources, streams, _ := sourceFixture(t, fake, photoRange{RangeClient: fake})
	connected := connectTyped(t, sources, tgclient.PeerGroup, 80, "")
	published := false
	_, err := sources.OpenSourceWithGate(t.Context(), "group", 80, 9, testAccountID, connected.Generation,
		func(add func() error) error { published = true; return add() })
	if !errors.Is(err, media.ErrInvalidImage) || published {
		t.Fatalf("invalid photo error=%v published=%v", err, published)
	}
	if tokens := streams.CloseAllExternalSessions(); len(tokens) != 0 {
		t.Fatalf("invalid photo leaked sessions: %d", len(tokens))
	}
}
