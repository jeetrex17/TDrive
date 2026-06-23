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

	// Healthy buffer → precompute runs; coarse-to-fine builds the coarse bucket at
	// the playhead (currentTime 0) first.
	thumbs.UpdatePlayback(0, 120, 60)
	got := waitForGeneratorEntry(t, gen.entered)
	if got != 0 {
		t.Fatalf("precomputed bucket = %d, want 0", got)
	}
	if !waitForCachedThumbnail(t, thumbs, 0) {
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

func TestVideoThumbnailerSerializesPersistentForegroundAndPrecompute(t *testing.T) {
	persistent := &blockingVideoThumbSession{
		entered: make(chan int, 2),
		release: make(chan struct{}),
	}
	gen := &blockingStatefulVideoThumbGenerator{session: persistent}
	session := testVideoThumbSession()
	session.setThumbnailURLs("http://127.0.0.1/thumb-source", "http://127.0.0.1/thumb")

	thumbs := newVideoThumbnailer(session, thumbnail.NewCache(t.TempDir(), 1<<20), gen)
	defer thumbs.Close()

	backgroundDone := make(chan struct{})
	go func() {
		defer close(backgroundDone)
		thumbs.generate(10, true)
	}()
	if got := waitForGeneratorEntry(t, persistent.entered); got != 10 {
		t.Fatalf("background bucket = %d, want 10", got)
	}

	foregroundDone := make(chan struct{})
	go func() {
		defer close(foregroundDone)
		thumbs.generate(20, false)
	}()

	select {
	case got := <-persistent.entered:
		t.Fatalf("foreground entered persistent extractor before background released; got bucket %d", got)
	case <-time.After(100 * time.Millisecond):
	}

	close(persistent.release)
	select {
	case <-backgroundDone:
	case <-time.After(2 * time.Second):
		t.Fatal("background generation did not finish")
	}
	if got := waitForGeneratorEntry(t, persistent.entered); got != 20 {
		t.Fatalf("foreground bucket = %d, want 20", got)
	}
	select {
	case <-foregroundDone:
	case <-time.After(2 * time.Second):
		t.Fatal("foreground generation did not finish")
	}
}

func TestVideoThumbnailerBufferBandGating(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	cases := []struct {
		name        string
		bufferAhead float64
		wantFGWait  bool
		wantPC      bool
	}{
		{"healthy", thumbBufferHealthyStart + 5, false, true},
		{"emergency", thumbBufferEmergency - 1, true, false},
		{"mid band aging tick", (thumbBufferEmergency + thumbBufferHealthyStop) / 2, false, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			vt := &videoThumbnailer{}
			vt.UpdatePlayback(10, 120, tc.bufferAhead)
			vt.mu.Lock()
			fgWait := vt.foregroundShouldWaitLocked(now)
			pc := vt.precomputeAllowedLocked(now)
			vt.mu.Unlock()
			if fgWait != tc.wantFGWait {
				t.Fatalf("foregroundShouldWait = %v, want %v", fgWait, tc.wantFGWait)
			}
			if pc != tc.wantPC {
				t.Fatalf("precomputeAllowed = %v, want %v", pc, tc.wantPC)
			}
		})
	}
}

func TestVideoThumbnailerPrecomputeAgingFloor(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	vt := &videoThumbnailer{}
	vt.UpdatePlayback(10, 120, (thumbBufferEmergency+thumbBufferHealthyStop)/2) // mid band
	vt.mu.Lock()
	defer vt.mu.Unlock()

	vt.lastPrecomputeAt = time.Time{}
	if !vt.precomputeAllowedLocked(now) {
		t.Fatal("mid band should allow an aging precompute tick")
	}
	vt.lastPrecomputeAt = now
	if vt.precomputeAllowedLocked(now) {
		t.Fatal("mid band should throttle precompute right after a tick")
	}
	if !vt.precomputeAllowedLocked(now.Add(thumbPrecomputeAging)) {
		t.Fatal("mid band should allow another tick once the aging interval elapses")
	}
}

func TestVideoThumbnailerPrecomputeHysteresis(t *testing.T) {
	vt := &videoThumbnailer{}
	fullSpeed := func() bool {
		vt.mu.Lock()
		defer vt.mu.Unlock()
		return vt.precomputeFullSpeed
	}

	vt.UpdatePlayback(10, 120, thumbBufferHealthyStart)
	if !fullSpeed() {
		t.Fatal("should latch full speed at the healthy-start watermark")
	}
	// Draining within the hysteresis band must not flap back off.
	vt.UpdatePlayback(10, 120, (thumbBufferHealthyStart+thumbBufferHealthyStop)/2)
	if !fullSpeed() {
		t.Fatal("should hold full speed inside the hysteresis band")
	}
	// Below the stop watermark it releases.
	vt.UpdatePlayback(10, 120, thumbBufferHealthyStop-1)
	if fullSpeed() {
		t.Fatal("should release full speed below the healthy-stop watermark")
	}
}

func TestVideoThumbnailerPrecomputeCoarseToFine(t *testing.T) {
	vt := &videoThumbnailer{
		session:  testVideoThumbSession(),
		cache:    thumbnail.NewCache(t.TempDir(), 1<<20),
		ready:    map[int]string{},
		inflight: map[int]struct{}{},
		failed:   map[int]time.Time{},
	}
	vt.UpdatePlayback(0, 600, 60) // healthy buffer so precompute is allowed
	coarse := thumbnailInterval(600) * precomputeCoarseFactor

	// The first targets must all land on the coarse grid (whole-bar coverage first).
	for i := range 4 {
		bucket, ok := vt.nextPrecomputeBucket()
		if !ok {
			t.Fatalf("iteration %d: expected a precompute bucket", i)
		}
		if bucket%coarse != 0 {
			t.Fatalf("iteration %d: bucket %d is not on the coarse grid (%d)", i, bucket, coarse)
		}
		vt.mu.Lock()
		vt.ready[bucket] = "x" // simulate it being generated
		vt.mu.Unlock()
	}
}

func TestVideoThumbnailerPrecomputeAnchorTierFirst(t *testing.T) {
	vt := &videoThumbnailer{
		session:  testVideoThumbSession(),
		cache:    thumbnail.NewCache(t.TempDir(), 1<<20),
		ready:    map[int]string{},
		inflight: map[int]struct{}{},
		failed:   map[int]time.Time{},
	}
	const duration = 3.0 * 60 * 60 // 3 hours
	vt.UpdatePlayback(0, duration, 120)

	anchor := anchorPrecomputeInterval(duration, thumbnailInterval(duration)*precomputeCoarseFactor)
	if anchor <= 0 {
		t.Fatal("expected an anchor tier for a 3h video")
	}

	// The first targets must be evenly-spaced anchors across the whole timeline.
	for i := range 5 {
		bucket, ok := vt.nextPrecomputeBucket()
		if !ok {
			t.Fatalf("iteration %d: expected a precompute bucket", i)
		}
		if bucket%anchor != 0 {
			t.Fatalf("iteration %d: bucket %d not on the anchor grid (%d)", i, bucket, anchor)
		}
		vt.mu.Lock()
		vt.ready[bucket] = "x"
		vt.mu.Unlock()
	}
}

func TestVideoThumbnailerPrecomputeDoesNotHotLoopOnDefer(t *testing.T) {
	gen := &deferringVideoThumbGenerator{available: true}
	session := testVideoThumbSession()
	session.setThumbnailURLs("http://127.0.0.1/thumb-source", "http://127.0.0.1/thumb")

	thumbs := newVideoThumbnailer(session, thumbnail.NewCache(t.TempDir(), 1<<20), gen)
	defer thumbs.Close()

	// Healthy buffer makes precompute want to run; every generate "defers", which
	// is intentionally not marked failed. A regressed inner loop would re-pick the
	// same bucket with no delay and spin hundreds of times.
	thumbs.UpdatePlayback(30, 120, 60)
	time.Sleep(300 * time.Millisecond)
	if n := gen.count(); n > 20 {
		t.Fatalf("precompute hot-looped on deferred generation: %d calls in 300ms", n)
	}
}

type deferringVideoThumbGenerator struct {
	available bool
	mu        sync.Mutex
	calls     int
}

func (g *deferringVideoThumbGenerator) Available() bool { return g != nil && g.available }

func (g *deferringVideoThumbGenerator) GenerateVideoThumbnail(_ context.Context, _, _ string, _ int) error {
	g.mu.Lock()
	g.calls++
	g.mu.Unlock()
	return context.Canceled
}

func (g *deferringVideoThumbGenerator) count() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.calls
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

type blockingStatefulVideoThumbGenerator struct {
	session *blockingVideoThumbSession
}

func (g *blockingStatefulVideoThumbGenerator) Available() bool { return true }

func (g *blockingStatefulVideoThumbGenerator) GenerateVideoThumbnail(_ context.Context, _ string, _ string, _ int) error {
	return errors.New("cold extractor should not be used")
}

func (g *blockingStatefulVideoThumbGenerator) NewVideoThumbnailSession(_ string) (VideoThumbnailSession, error) {
	return g.session, nil
}

type blockingVideoThumbSession struct {
	entered chan int
	release chan struct{}
}

func (s *blockingVideoThumbSession) GenerateVideoThumbnail(ctx context.Context, outPath string, seconds int) error {
	select {
	case s.entered <- seconds:
	case <-ctx.Done():
		return ctx.Err()
	}
	if seconds == 10 {
		select {
		case <-s.release:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return os.WriteFile(outPath, []byte{byte(seconds)}, videoThumbFileMode)
}

func (s *blockingVideoThumbSession) Close() {}
