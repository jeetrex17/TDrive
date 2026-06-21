package media

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"TDrive/backend/projection"
	"TDrive/backend/tgclient"
	"TDrive/backend/thumbnail"

	_ "modernc.org/sqlite"
)

func TestMediaServerServesTokenizedRanges(t *testing.T) {
	db := newResolverTestDB(t)
	body := testBytes(2048)
	mustApplyOp(t, db, 10, projection.Op{
		Type:     projection.OpFileUpload,
		Parent:   projection.RootParent,
		Name:     "clip.mp4",
		FileSize: int64(len(body)),
	})
	ranges := newMediaRangeFake(map[int64][]byte{10: body})
	svc := NewService(Config{
		DB:     db,
		Peers:  staticPeerResolver{peer: ranges.peer},
		Ranges: ranges,
	})
	defer svc.Close()

	opened, err := svc.Open(context.Background(), testChannelID, 10)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if opened.URL == "" || opened.Token == "" {
		t.Fatalf("open result missing URL/token: %+v", opened)
	}

	req, err := http.NewRequest(http.MethodGet, opened.URL, nil)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	req.Header.Set("Range", "bytes=100-199")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer resp.Body.Close()
	got, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if resp.StatusCode != http.StatusPartialContent {
		t.Fatalf("status = %d, want 206", resp.StatusCode)
	}
	if resp.Header.Get("Content-Range") != "bytes 100-199/2048" {
		t.Fatalf("Content-Range = %q", resp.Header.Get("Content-Range"))
	}
	if !bytes.Equal(got, body[100:200]) {
		t.Fatal("range body mismatch")
	}

	badURL := opened.URL[:len(opened.URL)-len(opened.Token)] + "bad-token"
	resp, err = http.Get(badURL)
	if err != nil {
		t.Fatalf("bad token GET: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("bad token status = %d, want 404", resp.StatusCode)
	}
}

func TestMediaServerRangeForms(t *testing.T) {
	db := newResolverTestDB(t)
	body := testBytes(512)
	mustApplyOp(t, db, 10, projection.Op{
		Type:     projection.OpFileUpload,
		Parent:   projection.RootParent,
		Name:     "clip.mp4",
		FileSize: int64(len(body)),
	})
	ranges := newMediaRangeFake(map[int64][]byte{10: body})
	svc := NewService(Config{DB: db, Peers: staticPeerResolver{peer: ranges.peer}, Ranges: ranges})
	defer svc.Close()
	opened, err := svc.Open(context.Background(), testChannelID, 10)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	tests := []struct {
		header string
		want   []byte
		crange string
	}{
		{header: "bytes=100-", want: body[100:], crange: "bytes 100-511/512"},
		{header: "bytes=-16", want: body[496:], crange: "bytes 496-511/512"},
	}
	for _, tt := range tests {
		req, _ := http.NewRequest(http.MethodGet, opened.URL, nil)
		req.Header.Set("Range", tt.header)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("%s: %v", tt.header, err)
		}
		got, err := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if err != nil {
			t.Fatalf("%s read: %v", tt.header, err)
		}
		if resp.StatusCode != http.StatusPartialContent || resp.Header.Get("Content-Range") != tt.crange {
			t.Fatalf("%s status/range = %d %q", tt.header, resp.StatusCode, resp.Header.Get("Content-Range"))
		}
		if !bytes.Equal(got, tt.want) {
			t.Fatalf("%s body mismatch", tt.header)
		}
	}

	req, _ := http.NewRequest(http.MethodGet, opened.URL, nil)
	req.Header.Set("Range", "bytes=999-1000")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("invalid range: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusRequestedRangeNotSatisfiable {
		t.Fatalf("invalid range status = %d, want 416", resp.StatusCode)
	}
}

func TestMediaServerClosesSessionAndReader(t *testing.T) {
	db := newResolverTestDB(t)
	body := testBytes(256)
	mustApplyOp(t, db, 10, projection.Op{
		Type:     projection.OpFileUpload,
		Parent:   projection.RootParent,
		Name:     "clip.mp4",
		FileSize: int64(len(body)),
	})
	ranges := newMediaRangeFake(map[int64][]byte{10: body})
	svc := NewService(Config{DB: db, Peers: staticPeerResolver{peer: ranges.peer}, Ranges: ranges})
	defer svc.Close()
	opened, err := svc.Open(context.Background(), testChannelID, 10)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := svc.CloseSession(opened.Token); err != nil {
		t.Fatalf("CloseSession: %v", err)
	}
	resp, err := http.Get(opened.URL)
	if err != nil {
		t.Fatalf("GET after close: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status after close = %d, want 404", resp.StatusCode)
	}
	if err := svc.CloseSession(opened.Token); !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("second close err = %v, want ErrSessionNotFound", err)
	}
}

func TestMediaSessionReadsAcrossMultipartSegments(t *testing.T) {
	db := newResolverTestDB(t)
	part1 := testBytes(80)
	part2 := testBytes(90)
	applyMultipart(t, db, "upload-media", []partSpec{
		{msgID: 101, size: int64(len(part1))},
		{msgID: 102, size: int64(len(part2))},
	}, 200, projection.Op{
		Type:       projection.OpFileManifest,
		UploadUUID: "upload-media",
		Parent:     projection.RootParent,
		Name:       "movie.mkv",
		FileSize:   int64(len(part1) + len(part2)),
		PartCount:  2,
	})
	ranges := newMediaRangeFake(map[int64][]byte{101: part1, 102: part2})
	svc := NewService(Config{DB: db, Peers: staticPeerResolver{peer: ranges.peer}, Ranges: ranges})
	defer svc.Close()
	opened, err := svc.Open(context.Background(), testChannelID, 200)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	req, _ := http.NewRequest(http.MethodGet, opened.URL, nil)
	req.Header.Set("Range", "bytes=70-99")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	got, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	want := append(append([]byte(nil), part1[70:]...), part2[:20]...)
	if !bytes.Equal(got, want) {
		t.Fatalf("multipart range mismatch: got %d want %d", len(got), len(want))
	}
}

func TestMediaServerQueuesAndServesVideoThumbnail(t *testing.T) {
	db := newResolverTestDB(t)
	body := testBytes(4096)
	mustApplyOp(t, db, 10, projection.Op{
		Type:     projection.OpFileUpload,
		Parent:   projection.RootParent,
		Name:     "clip.mp4",
		FileSize: int64(len(body)),
	})
	ranges := newMediaRangeFake(map[int64][]byte{10: body})
	gen := &fakeVideoThumbGenerator{available: true}
	svc := NewService(Config{
		DB:             db,
		Peers:          staticPeerResolver{peer: ranges.peer},
		Ranges:         ranges,
		Thumbs:         thumbnail.NewCache(t.TempDir(), 1<<20),
		ThumbGenerator: gen,
	})
	defer svc.Close()
	opened, err := svc.Open(context.Background(), testChannelID, 10)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if opened.ThumbnailURL == "" {
		t.Fatalf("Open missing ThumbnailURL: %+v", opened)
	}

	resp, err := http.Get(opened.ThumbnailURL + "?t=14")
	if err != nil {
		t.Fatalf("first thumbnail GET: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("first thumbnail status = %d, want 202", resp.StatusCode)
	}

	got := waitForThumbnail(t, opened.ThumbnailURL+"?t=14")
	wantPrefix := []byte("thumb:10:")
	if !bytes.HasPrefix(got, wantPrefix) {
		t.Fatalf("thumbnail body %q does not start with %q", got, wantPrefix)
	}

	call := gen.firstCall(t)
	if call.seconds != 10 {
		t.Fatalf("generated second = %d, want rounded bucket 10", call.seconds)
	}
	if !strings.Contains(call.sourceURL, mediaThumbSourcePrefix) {
		t.Fatalf("generator source URL = %q, want thumbnail source route", call.sourceURL)
	}
	if !bytes.Equal(call.sourceBytes, body[16:24]) {
		t.Fatalf("generator source bytes = %x, want %x", call.sourceBytes, body[16:24])
	}
}

func TestMediaServerThumbnailUnavailableWithoutGenerator(t *testing.T) {
	db := newResolverTestDB(t)
	body := testBytes(256)
	mustApplyOp(t, db, 10, projection.Op{
		Type:     projection.OpFileUpload,
		Parent:   projection.RootParent,
		Name:     "clip.mp4",
		FileSize: int64(len(body)),
	})
	ranges := newMediaRangeFake(map[int64][]byte{10: body})
	svc := NewService(Config{
		DB:             db,
		Peers:          staticPeerResolver{peer: ranges.peer},
		Ranges:         ranges,
		ThumbGenerator: &fakeVideoThumbGenerator{available: false},
	})
	defer svc.Close()
	opened, err := svc.Open(context.Background(), testChannelID, 10)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	resp, err := http.Get(opened.ThumbnailURL + "?t=1")
	if err != nil {
		t.Fatalf("thumbnail GET: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("thumbnail status = %d, want 503", resp.StatusCode)
	}
}

func TestMediaOpenRejectsEncryptedFiles(t *testing.T) {
	db := newResolverTestDB(t)
	mustApplyOp(t, db, 10, projection.Op{
		Type:          projection.OpFileUpload,
		Parent:        projection.RootParent,
		Name:          "secret.mp4",
		FileSize:      200,
		Encrypted:     true,
		PlaintextSize: 100,
	})
	ranges := newMediaRangeFake(map[int64][]byte{10: testBytes(200)})
	svc := NewService(Config{DB: db, Peers: staticPeerResolver{peer: ranges.peer}, Ranges: ranges})
	defer svc.Close()

	_, err := svc.Open(context.Background(), testChannelID, 10)
	if !errors.Is(err, ErrEncryptedUnsupported) {
		t.Fatalf("err = %v, want ErrEncryptedUnsupported", err)
	}
}

func TestMediaOpenRejectsUnsupportedFileTypes(t *testing.T) {
	db := newResolverTestDB(t)
	mustApplyOp(t, db, 10, projection.Op{
		Type:     projection.OpFileUpload,
		Parent:   projection.RootParent,
		Name:     "notes.txt",
		FileSize: 200,
	})
	ranges := newMediaRangeFake(map[int64][]byte{10: testBytes(200)})
	svc := NewService(Config{DB: db, Peers: staticPeerResolver{peer: ranges.peer}, Ranges: ranges})
	defer svc.Close()

	_, err := svc.Open(context.Background(), testChannelID, 10)
	if !errors.Is(err, ErrUnsupportedMediaType) {
		t.Fatalf("err = %v, want ErrUnsupportedMediaType", err)
	}
}

type staticPeerResolver struct {
	peer tgclient.InputPeer
}

func (s staticPeerResolver) ResolvePeer(context.Context, int64) (tgclient.InputPeer, error) {
	return s.peer, nil
}

type mediaRangeFake struct {
	peer   tgclient.InputPeer
	bodies map[int64][]byte
}

type fakeVideoThumbCall struct {
	seconds     int
	sourceURL   string
	sourceBytes []byte
}

type fakeVideoThumbGenerator struct {
	available bool

	mu    sync.Mutex
	calls []fakeVideoThumbCall
}

func (f *fakeVideoThumbGenerator) Available() bool {
	return f != nil && f.available
}

func (f *fakeVideoThumbGenerator) GenerateVideoThumbnail(ctx context.Context, sourceURL, outPath string, seconds int) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, sourceURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Range", "bytes=16-23")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	source, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusPartialContent {
		return fmt.Errorf("source status = %d", resp.StatusCode)
	}
	f.mu.Lock()
	f.calls = append(f.calls, fakeVideoThumbCall{
		seconds:     seconds,
		sourceURL:   sourceURL,
		sourceBytes: append([]byte(nil), source...),
	})
	f.mu.Unlock()
	return os.WriteFile(outPath, []byte(fmt.Sprintf("thumb:%d:%x", seconds, source)), 0o600)
}

func (f *fakeVideoThumbGenerator) firstCall(t *testing.T) fakeVideoThumbCall {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		f.mu.Lock()
		if len(f.calls) > 0 {
			call := f.calls[0]
			f.mu.Unlock()
			return call
		}
		f.mu.Unlock()
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("thumbnail generator was not called")
	return fakeVideoThumbCall{}
}

func waitForThumbnail(t *testing.T, url string) []byte {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := http.Get(url)
		if err != nil {
			t.Fatalf("thumbnail GET: %v", err)
		}
		body, readErr := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if readErr != nil {
			t.Fatalf("thumbnail read: %v", readErr)
		}
		if resp.StatusCode == http.StatusOK {
			return body
		}
		if resp.StatusCode != http.StatusAccepted {
			t.Fatalf("thumbnail status = %d body=%q", resp.StatusCode, body)
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("thumbnail did not become ready")
	return nil
}

func newMediaRangeFake(bodies map[int64][]byte) *mediaRangeFake {
	cp := make(map[int64][]byte, len(bodies))
	for id, body := range bodies {
		cp[id] = append([]byte(nil), body...)
	}
	return &mediaRangeFake{
		peer:   tgclient.InputPeer{ChannelID: testChannelID, AccessHash: 123},
		bodies: cp,
	}
}

func (f *mediaRangeFake) ResolveDocument(ctx context.Context, peer tgclient.InputPeer, msgID int64) (tgclient.DocumentRef, error) {
	body, ok := f.bodies[msgID]
	if !ok {
		return tgclient.DocumentRef{}, tgclient.ErrMessageNotFound
	}
	return tgclient.DocumentRef{
		Peer:       peer,
		MsgID:      msgID,
		Size:       int64(len(body)),
		Name:       "media.bin",
		DocumentID: msgID,
	}, nil
}

func (f *mediaRangeFake) ReadDocumentRange(ctx context.Context, ref tgclient.DocumentRef, offset int64, dst []byte) (int, error) {
	body, ok := f.bodies[ref.MsgID]
	if !ok {
		return 0, tgclient.ErrMessageNotFound
	}
	if offset+int64(len(dst)) > int64(len(body)) {
		return 0, io.ErrUnexpectedEOF
	}
	return copy(dst, body[offset:offset+int64(len(dst))]), nil
}

func TestParseRangeHeader(t *testing.T) {
	tests := []struct {
		raw     string
		size    int64
		start   int64
		end     int64
		partial bool
		ok      bool
	}{
		{raw: "", size: 10, partial: false, ok: true},
		{raw: "bytes=2-5", size: 10, start: 2, end: 5, partial: true, ok: true},
		{raw: "bytes=2-", size: 10, start: 2, end: 9, partial: true, ok: true},
		{raw: "bytes=-3", size: 10, start: 7, end: 9, partial: true, ok: true},
		{raw: "bytes=8-99", size: 10, start: 8, end: 9, partial: true, ok: true},
		{raw: "bytes=10-11", size: 10, partial: true, ok: false},
		{raw: "bytes=4-2", size: 10, partial: true, ok: false},
		{raw: "bytes=1-2,3-4", size: 10, partial: true, ok: false},
	}
	for _, tt := range tests {
		start, end, partial, ok := parseRangeHeader(tt.raw, tt.size)
		if start != tt.start || end != tt.end || partial != tt.partial || ok != tt.ok {
			t.Fatalf("%q = %d,%d,%v,%v want %d,%d,%v,%v", tt.raw, start, end, partial, ok, tt.start, tt.end, tt.partial, tt.ok)
		}
	}
}

func TestContentTypeForCommonVideoContainers(t *testing.T) {
	tests := map[string]string{
		"clip.mp4":  "video/mp4",
		"clip.mov":  "video/quicktime",
		"clip.webm": "video/webm",
		"clip.mkv":  "video/x-matroska",
		"clip.avi":  "video/x-msvideo",
		"clip.ts":   "video/mp2t",
	}
	for name, want := range tests {
		if got := contentTypeFor(name); got != want {
			t.Fatalf("contentTypeFor(%q) = %q, want %q", name, got, want)
		}
	}
}

func TestMediaServiceRequiresRangeClient(t *testing.T) {
	db := newResolverTestDB(t)
	svc := NewService(Config{DB: db, Peers: staticPeerResolver{peer: tgclient.InputPeer{ChannelID: testChannelID}}})
	_, err := svc.Open(context.Background(), testChannelID, 10)
	if !errors.Is(err, ErrRangeClientNotReady) {
		t.Fatalf("err = %v, want ErrRangeClientNotReady", err)
	}
}

func TestMediaServiceRequiresPeerResolver(t *testing.T) {
	db := newResolverTestDB(t)
	svc := NewService(Config{DB: db, Ranges: newMediaRangeFake(nil)})
	_, err := svc.Open(context.Background(), testChannelID, 10)
	if !errors.Is(err, ErrPeerResolverNotReady) {
		t.Fatalf("err = %v, want ErrPeerResolverNotReady", err)
	}
}

func newMediaTestDB(t *testing.T) *sql.DB {
	t.Helper()
	return newResolverTestDB(t)
}
