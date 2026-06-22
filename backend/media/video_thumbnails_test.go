package media

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"TDrive/backend/thumbnail"
)

func TestVideoThumbnailerPrioritizesLatestRequest(t *testing.T) {
	gen := &recordingVideoThumbGenerator{
		available: true,
		entered:   make(chan int, 4),
		release:   make(chan struct{}),
	}
	session := testVideoThumbSession()
	session.setThumbnailURLs("http://127.0.0.1/thumb-source", "http://127.0.0.1/thumb")

	thumbs := newVideoThumbnailer(session, thumbnail.NewCache(t.TempDir(), 1<<20), gen)
	defer thumbs.Close()

	if _, err := thumbs.Get(context.Background(), 10); err != ErrThumbnailPending {
		t.Fatalf("first Get err = %v, want ErrThumbnailPending", err)
	}
	if got := waitForGeneratorEntry(t, gen.entered); got != 10 {
		t.Fatalf("first generated bucket = %d, want 10", got)
	}

	if _, err := thumbs.Get(context.Background(), 20); err != ErrThumbnailPending {
		t.Fatalf("second Get err = %v, want ErrThumbnailPending", err)
	}
	if _, err := thumbs.Get(context.Background(), 30); err != ErrThumbnailPending {
		t.Fatalf("third Get err = %v, want ErrThumbnailPending", err)
	}

	close(gen.release)
	if got := waitForGeneratorEntry(t, gen.entered); got != 30 {
		t.Fatalf("next generated bucket = %d, want latest bucket 30", got)
	}

	calls := gen.calls()
	if len(calls) != 2 || calls[0] != 10 || calls[1] != 30 {
		t.Fatalf("generated calls = %v, want [10 30]", calls)
	}
}

func TestVideoThumbnailerKeepsPersistentOnTimeout(t *testing.T) {
	gen := &statefulVideoThumbGenerator{
		session: &fakeVideoThumbSession{err: context.DeadlineExceeded},
	}
	thumbs := &videoThumbnailer{generator: gen}

	err := thumbs.generateThumbnail(context.Background(), "http://127.0.0.1/video", filepath.Join(t.TempDir(), "thumb.jpg"), 120)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("generateThumbnail err = %v, want DeadlineExceeded", err)
	}
	if gen.coldCalls != 0 {
		t.Fatalf("cold fallback calls = %d, want 0", gen.coldCalls)
	}
	if thumbs.persistentOff {
		t.Fatal("persistent extractor was disabled after a timeout")
	}
}

func TestVideoThumbnailerRestartsDeadPersistentSession(t *testing.T) {
	outPath := filepath.Join(t.TempDir(), "thumb.jpg")
	gen := &statefulVideoThumbGenerator{
		sessions: []VideoThumbnailSession{
			&fakeVideoThumbSession{err: errThumbnailSessionDead},
			&fakeVideoThumbSession{data: []byte("warm")},
		},
	}
	thumbs := &videoThumbnailer{generator: gen}

	if err := thumbs.generateThumbnail(context.Background(), "http://127.0.0.1/video", outPath, 120); err != nil {
		t.Fatalf("generateThumbnail err = %v", err)
	}
	if gen.sessionCreations != 2 {
		t.Fatalf("session creations = %d, want 2", gen.sessionCreations)
	}
	if gen.coldCalls != 0 {
		t.Fatalf("cold fallback calls = %d, want 0", gen.coldCalls)
	}
	data, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "warm" {
		t.Fatalf("output = %q, want warm", string(data))
	}
	if thumbs.persistentOff {
		t.Fatal("persistent extractor was disabled after a successful restart")
	}
}

func TestVideoThumbnailerBackgroundPrecomputeGenerates(t *testing.T) {
	gen := &recordingVideoThumbGenerator{available: true, entered: make(chan int, 4)}
	session := testVideoThumbSession()
	session.setThumbnailURLs("http://127.0.0.1/thumb-source", "http://127.0.0.1/thumb")

	thumbs := newVideoThumbnailer(session, thumbnail.NewCache(t.TempDir(), 1<<20), gen)
	defer thumbs.Close()

	thumbs.UpdatePlayback(30, 120, false)
	got := waitForGeneratorEntry(t, gen.entered)
	if got != 30 {
		t.Fatalf("precomputed bucket = %d, want 30", got)
	}
	if !waitForCachedThumbnail(t, thumbs, 30) {
		t.Fatal("precomputed bucket was not cached")
	}
}

func TestVideoThumbnailerBackgroundPrecomputeYieldsToForeground(t *testing.T) {
	gen := &recordingVideoThumbGenerator{available: true, entered: make(chan int, 4)}
	session := testVideoThumbSession()
	session.setThumbnailURLs("http://127.0.0.1/thumb-source", "http://127.0.0.1/thumb")

	thumbs := newVideoThumbnailer(session, thumbnail.NewCache(t.TempDir(), 1<<20), gen)
	defer thumbs.Close()

	thumbs.mu.Lock()
	thumbs.hasLatest = true
	thumbs.mu.Unlock()

	thumbs.generate(30, true)
	select {
	case got := <-gen.entered:
		t.Fatalf("background generator ran for bucket %d while foreground was pending", got)
	default:
	}
	if _, ok := thumbs.cached(30); ok {
		t.Fatal("background bucket was cached even though foreground was pending")
	}
}

type recordingVideoThumbGenerator struct {
	available bool
	entered   chan int
	release   chan struct{}

	mu      sync.Mutex
	records []int
}

func (g *recordingVideoThumbGenerator) Available() bool {
	return g != nil && g.available
}

func (g *recordingVideoThumbGenerator) GenerateVideoThumbnail(ctx context.Context, _ string, outPath string, seconds int) error {
	g.mu.Lock()
	g.records = append(g.records, seconds)
	g.mu.Unlock()

	select {
	case g.entered <- seconds:
	default:
	}

	if seconds == 10 && g.release != nil {
		select {
		case <-g.release:
		case <-ctx.Done():
			return ctx.Err()
		}
	}

	return os.WriteFile(outPath, []byte{byte(seconds)}, videoThumbFileMode)
}

func (g *recordingVideoThumbGenerator) calls() []int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]int(nil), g.records...)
}

func waitForGeneratorEntry(t *testing.T, ch <-chan int) int {
	t.Helper()
	select {
	case got := <-ch:
		return got
	case <-time.After(2 * time.Second):
		t.Fatal("thumbnail generator was not called")
		return 0
	}
}

func waitForCachedThumbnail(t *testing.T, thumbs *videoThumbnailer, bucket int) bool {
	t.Helper()
	deadline := time.After(2 * time.Second)
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for {
		if _, ok := thumbs.cached(bucket); ok {
			return true
		}
		select {
		case <-deadline:
			return false
		case <-tick.C:
		}
	}
}

func testVideoThumbSession() *Session {
	return &Session{
		file: LogicalFile{
			ChannelID:     1,
			FileID:        10,
			Name:          "movie.mkv",
			StoredSize:    1000,
			PlaintextSize: 1000,
		},
		lastTouch: time.Now(),
	}
}

type statefulVideoThumbGenerator struct {
	session  VideoThumbnailSession
	sessions []VideoThumbnailSession

	coldCalls        int
	sessionCreations int
}

func (g *statefulVideoThumbGenerator) Available() bool {
	return true
}

func (g *statefulVideoThumbGenerator) GenerateVideoThumbnail(_ context.Context, _ string, outPath string, _ int) error {
	g.coldCalls++
	return os.WriteFile(outPath, []byte("cold"), videoThumbFileMode)
}

func (g *statefulVideoThumbGenerator) NewVideoThumbnailSession(_ string) (VideoThumbnailSession, error) {
	g.sessionCreations++
	if len(g.sessions) > 0 {
		session := g.sessions[0]
		g.sessions = g.sessions[1:]
		return session, nil
	}
	return g.session, nil
}

type fakeVideoThumbSession struct {
	err  error
	data []byte
}

func (s *fakeVideoThumbSession) GenerateVideoThumbnail(_ context.Context, outPath string, _ int) error {
	if s.err != nil {
		return s.err
	}
	data := s.data
	if len(data) == 0 {
		data = []byte("warm")
	}
	return os.WriteFile(outPath, data, videoThumbFileMode)
}

func (s *fakeVideoThumbSession) Close() {}
