package channelsource

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"TDrive/backend/media"
	"TDrive/backend/projection"
	"TDrive/backend/tgclient"

	"github.com/gotd/td/tgerr"
	_ "modernc.org/sqlite"
)

const (
	testChannelID int64 = 7711
	testAccountID int64 = 1234
)

func sourceFixture(t *testing.T, client tgclient.Client, ranges tgclient.RangeClient) (*Service, *media.Service, *sql.DB) {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	if err := projection.EnsureSchema(db); err != nil {
		t.Fatal(err)
	}
	streams := media.NewService(media.Config{DB: db, Ranges: ranges})
	t.Cleanup(func() { _ = streams.Close() })
	sources, err := NewService(db, client, streams)
	if err != nil {
		t.Fatal(err)
	}
	return sources, streams, db
}

func seedVideo(fake *tgclient.Fake, channelID, msgID int64, body []byte) tgclient.HistoryMessage {
	message := tgclient.HistoryMessage{
		ChannelID: channelID, MsgID: msgID, Date: 123, HasMedia: true,
		Text: "TDX1|t=f|fake=caption", DocumentName: "clip.mp4",
		DocumentID: 9001, DocumentAccessHash: 9002, MediaSize: int64(len(body)),
		MimeType: "video/mp4", Video: true,
	}
	fake.SeedHistory(message)
	fake.SeedDocumentBody(msgID, body)
	return message
}

func TestJoinedSourcesPageAndStreamWithoutProjection(t *testing.T) {
	ctx := t.Context()
	fake := tgclient.NewFake(testAccountID)
	fake.SeedJoinedBroadcastChannels(
		tgclient.JoinedBroadcastChannel{ID: testChannelID, AccessHash: 77, Title: "Cinema"},
		tgclient.JoinedBroadcastChannel{ID: 7722, AccessHash: 78, Title: "Locked", Protected: true},
	)
	body := bytes.Repeat([]byte("0123456789"), 300)
	seedVideo(fake, testChannelID, 1, body)
	for id := int64(2); id <= 55; id++ {
		fake.SeedHistory(tgclient.HistoryMessage{ChannelID: testChannelID, MsgID: id, Text: "ordinary post"})
	}
	sources, streams, db := sourceFixture(t, fake, fake)
	candidates, err := sources.ListCandidates(ctx)
	if err != nil || len(candidates) != 2 || candidates[0].Connected {
		t.Fatalf("candidates = %#v, %v", candidates, err)
	}
	connected, err := sources.Connect(ctx, testChannelID)
	if err != nil || !connected.Connected || connected.Generation == "" {
		t.Fatalf("connect = %#v, %v", connected, err)
	}
	reopened, err := NewService(db, fake, streams)
	if err != nil {
		t.Fatal(err)
	}
	stored, err := reopened.ListConnected(ctx)
	if err != nil || len(stored) != 1 || stored[0].Generation != connected.Generation {
		t.Fatalf("reopened sources = %#v, %v", stored, err)
	}
	candidates, err = reopened.ListCandidates(ctx)
	if err != nil || !candidates[0].Connected || candidates[0].Generation != connected.Generation {
		t.Fatalf("connected candidate = %#v, %v", candidates, err)
	}
	duplicate, err := reopened.Connect(ctx, testChannelID)
	if err != nil || duplicate.Generation != connected.Generation {
		t.Fatalf("idempotent connect = %#v, %v", duplicate, err)
	}
	searched, err := reopened.Page(ctx, testChannelID, 0, 5, "TDX1", "video")
	if err != nil || len(searched.Items) != 1 || searched.Items[0].MsgID != 1 {
		t.Fatalf("server search page = %#v, %v", searched, err)
	}
	fake.InjectReadFloodWaits(1)
	retried, err := reopened.Page(ctx, testChannelID, 0, 1, "", "video")
	if err != nil || len(retried.Items) != 1 {
		t.Fatalf("flood-wait retry page = %#v, %v", retried, err)
	}
	page, err := sources.Page(ctx, testChannelID, 0, 1, "", "video")
	if err != nil || len(page.Items) != 1 || page.Items[0].MsgID != 1 || !page.Items[0].Streamable {
		t.Fatalf("page = %#v, %v", page, err)
	}
	if page.Generation != connected.Generation || page.AccountID != testAccountID || page.Items[0].TelegramURL != "https://t.me/c/7711/1" {
		t.Fatalf("page scope/link = %#v", page)
	}
	var projected int
	if err := db.QueryRow(`SELECT COUNT(*) FROM replay_log`).Scan(&projected); err != nil || projected != 0 {
		t.Fatalf("projected operations = %d, %v", projected, err)
	}
	opened, err := sources.Open(ctx, testChannelID, 1, connected.AccountID, connected.Generation)
	if err != nil {
		t.Fatal(err)
	}
	if opened.Info.SourceKind != "channel" || opened.Info.SourceAccountID != testAccountID {
		t.Fatalf("source info = %#v", opened.Info)
	}
	request, _ := http.NewRequest(http.MethodGet, opened.URL, nil)
	request.Header.Set("Range", "bytes=71-199")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	got, readErr := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if readErr != nil || response.StatusCode != http.StatusPartialContent || !bytes.Equal(got, body[71:200]) {
		t.Fatalf("range = %d %x %v", response.StatusCode, got, readErr)
	}
	if !strings.Contains(response.Header.Get("Cache-Control"), "no-store") {
		t.Fatalf("cache control = %q", response.Header.Get("Cache-Control"))
	}
	tokens, err := sources.Disconnect(ctx, testChannelID)
	if err != nil || len(tokens) != 1 || tokens[0] != opened.Token {
		t.Fatalf("disconnect tokens = %v, %v", tokens, err)
	}
	response, err = http.Get(opened.URL)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusNotFound || len(fake.LeftChannels()) != 0 {
		t.Fatalf("revoked status = %d, left channels = %v", response.StatusCode, fake.LeftChannels())
	}
}

func TestSourceBoundaryValidation(t *testing.T) {
	fake := tgclient.NewFake(testAccountID)
	fake.SeedJoinedBroadcastChannels(tgclient.JoinedBroadcastChannel{ID: testChannelID, AccessHash: 77, Title: "Cinema"})
	sources, _, _ := sourceFixture(t, fake, fake)
	if _, err := sources.Connect(t.Context(), -1); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("negative channel connect = %v", err)
	}
	if _, err := sources.Connect(t.Context(), 8888); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("unjoined channel connect = %v", err)
	}
	if _, err := sources.ConnectWithGate(t.Context(), testChannelID, testAccountID+1, func(save func() error) error { return save() }); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("stale account picker connect = %v", err)
	}
	connected, err := sources.Connect(t.Context(), testChannelID)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		offset      int64
		limit       int
		query, kind string
	}{
		{-1, 20, "", "all"}, {0, 0, "", "all"}, {0, 101, "", "all"},
		{0, 20, strings.Repeat("x", 121), "all"}, {0, 20, "", "image"},
	} {
		if _, err := sources.Page(t.Context(), testChannelID, test.offset, test.limit, test.query, test.kind); !errors.Is(err, ErrInvalidPage) {
			t.Errorf("invalid page %+v = %v", test, err)
		}
	}
	if _, err := sources.Open(t.Context(), testChannelID, -1, connected.AccountID, connected.Generation); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("negative message open = %v", err)
	}
	if _, err := sources.Open(t.Context(), testChannelID, 1, connected.AccountID, "stale"); !errors.Is(err, ErrNotConnected) {
		t.Fatalf("stale generation open = %v", err)
	}
	if _, err := sources.DisconnectWithGate(t.Context(), testChannelID, testAccountID+1, connected.Generation, func(remove func() error) error { return remove() }); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("stale account disconnect = %v", err)
	}
	if _, err := sources.DisconnectWithGate(t.Context(), testChannelID, testAccountID, "old", func(remove func() error) error { return remove() }); !errors.Is(err, ErrNotConnected) {
		t.Fatalf("stale generation disconnect = %v", err)
	}
}

func TestSourceAccountGenerationAndPermissions(t *testing.T) {
	ctx := t.Context()
	fake := tgclient.NewFake(testAccountID)
	fake.SeedJoinedBroadcastChannels(tgclient.JoinedBroadcastChannel{ID: testChannelID, AccessHash: 77, Title: "Cinema"})
	seedVideo(fake, testChannelID, 10, []byte("video data"))
	fake.SeedHistory(tgclient.HistoryMessage{ChannelID: 9999, MsgID: 20, HasMedia: true,
		DocumentID: 20, MediaSize: 5, DocumentName: "wrong.mp4"})
	sources, _, _ := sourceFixture(t, fake, fake)
	first, err := sources.Connect(ctx, testChannelID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sources.Open(ctx, testChannelID, 20, first.AccountID, first.Generation); !errors.Is(err, tgclient.ErrMessageNotFound) {
		t.Fatalf("cross-channel open error = %v", err)
	}
	fake.SetSelfID(testAccountID + 1)
	if got, err := sources.ListConnected(ctx); err != nil || len(got) != 0 {
		t.Fatalf("other account sources = %v, %v", got, err)
	}
	if _, err := sources.Open(ctx, testChannelID, 10, first.AccountID, first.Generation); !errors.Is(err, ErrNotConnected) {
		t.Fatalf("other account open error = %v", err)
	}
	fake.SetSelfID(testAccountID)
	if _, err := sources.Disconnect(ctx, testChannelID); err != nil {
		t.Fatal(err)
	}
	second, err := sources.Connect(ctx, testChannelID)
	if err != nil || second.Generation == first.Generation {
		t.Fatalf("reconnect generation = %q / %q, %v", first.Generation, second.Generation, err)
	}
	if _, err := sources.Open(ctx, testChannelID, 10, first.AccountID, first.Generation); !errors.Is(err, ErrNotConnected) {
		t.Fatalf("stale selected source open error = %v", err)
	}
	fake.SeedJoinedBroadcastChannels(tgclient.JoinedBroadcastChannel{ID: testChannelID, AccessHash: 77, Title: "Cinema", Protected: true})
	page, err := sources.Page(ctx, testChannelID, 0, 20, "", "all")
	if err != nil || len(page.Items) != 1 || page.Items[0].BlockReason != "protected" {
		t.Fatalf("protected page = %#v, %v", page, err)
	}
	if _, err := sources.Open(ctx, testChannelID, 10, second.AccountID, second.Generation); !errors.Is(err, media.ErrExternalRestricted) {
		t.Fatalf("protected open error = %v", err)
	}
}

func TestPostRestrictionsBlockExternalOpen(t *testing.T) {
	fake := tgclient.NewFake(testAccountID)
	fake.SeedJoinedBroadcastChannels(tgclient.JoinedBroadcastChannel{ID: testChannelID, AccessHash: 77, Title: "Cinema"})
	for _, test := range []struct {
		id      int64
		message tgclient.HistoryMessage
		want    string
	}{
		{1, tgclient.HistoryMessage{NoForwards: true}, "protected"},
		{2, tgclient.HistoryMessage{TTLSeconds: 30}, "expires"},
		{3, tgclient.HistoryMessage{Paid: true}, "paid"},
	} {
		message := tgclient.HistoryMessage{ChannelID: testChannelID, MsgID: test.id,
			HasMedia: true, DocumentID: test.id + 100, DocumentAccessHash: 55,
			MediaSize: 16, DocumentName: "clip.mp4", MimeType: "video/mp4", Video: true}
		if test.message.Paid {
			message.DocumentID = 0
		}
		message.NoForwards = test.message.NoForwards
		message.TTLSeconds = test.message.TTLSeconds
		message.Paid = test.message.Paid
		fake.SeedHistory(message)
	}
	sources, _, _ := sourceFixture(t, fake, fake)
	connected, err := sources.Connect(t.Context(), testChannelID)
	if err != nil {
		t.Fatal(err)
	}
	page, err := sources.Page(t.Context(), testChannelID, 0, 20, "", "all")
	if err != nil || len(page.Items) != 3 {
		t.Fatalf("restricted page = %#v, %v", page, err)
	}
	for _, item := range page.Items {
		want := map[int64]string{1: "protected", 2: "expires", 3: "paid"}[item.MsgID]
		if item.Streamable || item.BlockReason != want || item.TelegramURL == "" {
			t.Errorf("item %d = %#v, want blocked %q", item.MsgID, item, want)
		}
		if _, err := sources.Open(t.Context(), testChannelID, item.MsgID, connected.AccountID, connected.Generation); !errors.Is(err, media.ErrExternalRestricted) {
			t.Errorf("open restricted item %d = %v", item.MsgID, err)
		}
	}
}

type rangeFault struct {
	tgclient.RangeClient
	firstRead     atomic.Bool
	resolves      atomic.Int64
	beforeResolve func()
}

func (r *rangeFault) ResolveDocument(ctx context.Context, peer tgclient.InputPeer, msgID int64) (tgclient.DocumentRef, error) {
	if r.beforeResolve != nil {
		r.beforeResolve()
	}
	r.resolves.Add(1)
	return r.RangeClient.ResolveDocument(ctx, peer, msgID)
}

func (r *rangeFault) ReadDocumentRange(ctx context.Context, ref tgclient.DocumentRef, offset int64, dst []byte) (int, error) {
	if !r.firstRead.Swap(true) {
		return 0, tgerr.New(400, "FILE_REFERENCE_EXPIRED")
	}
	return r.RangeClient.ReadDocumentRange(ctx, ref, offset, dst)
}

type messageSwap struct {
	*tgclient.Fake
	changed atomic.Bool
	protect atomic.Bool
}

func (s *messageSwap) GetChannelMessage(ctx context.Context, peer tgclient.InputPeer, msgID int64) (tgclient.HistoryMessage, error) {
	message, err := s.Fake.GetChannelMessage(ctx, peer, msgID)
	if s.changed.Load() {
		message.DocumentID++
	}
	if s.protect.Load() {
		message.NoForwards = true
	}
	return message, err
}

func TestExternalReferenceRefreshRejectsReplacement(t *testing.T) {
	for _, test := range []struct {
		name                string
		replaced, protected bool
	}{
		{"same document", false, false}, {"replacement", true, false}, {"protected after open", false, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			fake := tgclient.NewFake(testAccountID)
			fake.SeedJoinedBroadcastChannels(tgclient.JoinedBroadcastChannel{ID: testChannelID, AccessHash: 77, Title: "Cinema"})
			body := []byte("reference refreshed video bytes")
			seedVideo(fake, testChannelID, 10, body)
			client := &messageSwap{Fake: fake}
			ranges := &rangeFault{RangeClient: fake}
			sources, _, _ := sourceFixture(t, client, ranges)
			connected, err := sources.Connect(t.Context(), testChannelID)
			if err != nil {
				t.Fatal(err)
			}
			opened, err := sources.Open(t.Context(), testChannelID, 10, connected.AccountID, connected.Generation)
			if err != nil {
				t.Fatal(err)
			}
			client.changed.Store(test.replaced)
			client.protect.Store(test.protected)
			response, err := http.Get(opened.URL)
			if err != nil {
				t.Fatal(err)
			}
			got, _ := io.ReadAll(response.Body)
			_ = response.Body.Close()
			if test.replaced || test.protected {
				if bytes.Equal(got, body) || ranges.resolves.Load() < 1 {
					t.Fatalf("restricted/replaced media served complete body; resolves=%d", ranges.resolves.Load())
				}
			} else if response.StatusCode != http.StatusOK || !bytes.Equal(got, body) || ranges.resolves.Load() < 2 {
				t.Fatalf("refreshed response = %d %q resolves=%d", response.StatusCode, got, ranges.resolves.Load())
			}
		})
	}
}
