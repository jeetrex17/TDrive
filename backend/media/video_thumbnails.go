package media

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"TDrive/backend/thumbnail"
)

const (
	videoThumbIntervalSeconds = 10
	videoThumbTimeout         = 8 * time.Second
	videoThumbTempPrefix      = "tdrive-media-thumbs-"
	videoThumbTempMaxAge      = 24 * time.Hour
	videoThumbDirMode         = 0o700
	videoThumbFileMode        = 0o600
	videoThumbMime            = "image/jpeg"
)

// VideoThumbnailGenerator extracts one preview frame from a media URL.
// Implementations must write an image file at outPath or return an error.
type VideoThumbnailGenerator interface {
	GenerateVideoThumbnail(ctx context.Context, sourceURL, outPath string, seconds int) error
	Available() bool
}

type videoThumbnailer struct {
	session   *Session
	cache     *thumbnail.Cache
	generator VideoThumbnailGenerator
	dir       string

	ctx    context.Context
	cancel context.CancelFunc
	jobs   chan int
	wg     sync.WaitGroup

	mu       sync.Mutex
	ready    map[int]string
	queued   map[int]struct{}
	inflight map[int]struct{}
	failed   map[int]time.Time
}

func newVideoThumbnailer(session *Session, cache *thumbnail.Cache, generator VideoThumbnailGenerator) *videoThumbnailer {
	ctx, cancel := context.WithCancel(context.Background())
	t := &videoThumbnailer{
		session:   session,
		cache:     cache,
		generator: generator,
		ctx:       ctx,
		cancel:    cancel,
		jobs:      make(chan int, 16),
		ready:     make(map[int]string),
		queued:    make(map[int]struct{}),
		inflight:  make(map[int]struct{}),
		failed:    make(map[int]time.Time),
	}
	t.wg.Add(1)
	go t.worker()
	return t
}

func (t *videoThumbnailer) Get(ctx context.Context, seconds float64) ([]byte, error) {
	if t == nil || t.session == nil || t.generator == nil || !t.generator.Available() {
		return nil, ErrThumbnailUnavailable
	}
	bucket := thumbnailBucket(seconds, t.session.Size())
	if bucket < 0 {
		return nil, ErrThumbnailUnavailable
	}
	if data, ok := t.cached(bucket); ok {
		return data, nil
	}
	if err := t.queue(ctx, bucket); err != nil {
		return nil, err
	}
	return nil, ErrThumbnailPending
}

func (t *videoThumbnailer) Close() {
	if t == nil {
		return
	}
	t.cancel()
	done := make(chan struct{})
	go func() {
		t.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
	}
	t.mu.Lock()
	dir := t.dir
	t.dir = ""
	t.mu.Unlock()
	if dir != "" {
		_ = os.RemoveAll(dir)
	}
}

func (t *videoThumbnailer) cached(bucket int) ([]byte, bool) {
	if data, ok := t.cache.Get(t.cacheKey(bucket)); ok {
		return data, true
	}

	t.mu.Lock()
	path := t.ready[bucket]
	t.mu.Unlock()
	if path == "" {
		return nil, false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.mu.Lock()
		delete(t.ready, bucket)
		t.mu.Unlock()
		return nil, false
	}
	return data, true
}

func (t *videoThumbnailer) queue(ctx context.Context, bucket int) error {
	t.mu.Lock()
	if failedAt, failed := t.failed[bucket]; failed && time.Since(failedAt) < 15*time.Second {
		t.mu.Unlock()
		return ErrThumbnailUnavailable
	}
	if _, ok := t.queued[bucket]; ok {
		t.mu.Unlock()
		return nil
	}
	if _, ok := t.inflight[bucket]; ok {
		t.mu.Unlock()
		return nil
	}
	t.queued[bucket] = struct{}{}
	t.mu.Unlock()

	select {
	case t.jobs <- bucket:
		t.queueNeighbor(bucket - videoThumbIntervalSeconds)
		t.queueNeighbor(bucket + videoThumbIntervalSeconds)
		return nil
	case <-t.ctx.Done():
		return ErrSessionNotFound
	case <-ctx.Done():
		t.mu.Lock()
		delete(t.queued, bucket)
		t.mu.Unlock()
		return ctx.Err()
	default:
		t.mu.Lock()
		delete(t.queued, bucket)
		t.mu.Unlock()
		return nil
	}
}

func (t *videoThumbnailer) queueNeighbor(bucket int) {
	if bucket < 0 {
		return
	}
	t.mu.Lock()
	if _, ready := t.ready[bucket]; ready {
		t.mu.Unlock()
		return
	}
	if _, queued := t.queued[bucket]; queued {
		t.mu.Unlock()
		return
	}
	if _, inflight := t.inflight[bucket]; inflight {
		t.mu.Unlock()
		return
	}
	t.queued[bucket] = struct{}{}
	t.mu.Unlock()

	select {
	case t.jobs <- bucket:
	default:
		t.mu.Lock()
		delete(t.queued, bucket)
		t.mu.Unlock()
	}
}

func (t *videoThumbnailer) worker() {
	defer t.wg.Done()
	for {
		select {
		case <-t.ctx.Done():
			return
		case bucket := <-t.jobs:
			t.generate(bucket)
		}
	}
}

func (t *videoThumbnailer) generate(bucket int) {
	t.mu.Lock()
	delete(t.queued, bucket)
	if _, ok := t.ready[bucket]; ok {
		t.mu.Unlock()
		return
	}
	t.inflight[bucket] = struct{}{}
	t.mu.Unlock()

	defer func() {
		t.mu.Lock()
		delete(t.inflight, bucket)
		t.mu.Unlock()
	}()

	if _, ok := t.cache.Get(t.cacheKey(bucket)); ok {
		return
	}

	dir, err := t.ensureDir()
	if err != nil {
		t.markFailed(bucket)
		return
	}
	outPath := filepath.Join(dir, fmt.Sprintf("thumb-%d.jpg", bucket))
	sourceURL := t.session.thumbnailSourceURL()
	if sourceURL == "" {
		t.markFailed(bucket)
		return
	}

	runCtx, cancel := context.WithTimeout(t.ctx, videoThumbTimeout)
	defer cancel()
	if err := t.generator.GenerateVideoThumbnail(runCtx, sourceURL, outPath, bucket); err != nil {
		_ = os.Remove(outPath)
		t.markFailed(bucket)
		return
	}
	_ = os.Chmod(outPath, videoThumbFileMode)

	data, err := os.ReadFile(outPath)
	if err != nil || len(data) == 0 {
		_ = os.Remove(outPath)
		t.markFailed(bucket)
		return
	}
	_ = t.cache.Put(t.cacheKey(bucket), data)

	t.mu.Lock()
	t.ready[bucket] = outPath
	delete(t.failed, bucket)
	t.mu.Unlock()
}

func (t *videoThumbnailer) ensureDir() (string, error) {
	t.mu.Lock()
	if t.dir != "" {
		dir := t.dir
		t.mu.Unlock()
		return dir, nil
	}
	t.mu.Unlock()

	dir, err := os.MkdirTemp("", videoThumbTempPrefix)
	if err != nil {
		return "", err
	}
	_ = os.Chmod(dir, videoThumbDirMode)

	t.mu.Lock()
	if t.dir == "" {
		t.dir = dir
		t.mu.Unlock()
		return dir, nil
	}
	existing := t.dir
	t.mu.Unlock()
	_ = os.RemoveAll(dir)
	return existing, nil
}

func (t *videoThumbnailer) markFailed(bucket int) {
	t.mu.Lock()
	t.failed[bucket] = time.Now()
	t.mu.Unlock()
}

func (t *videoThumbnailer) cacheKey(bucket int) string {
	file := t.session.file
	return fmt.Sprintf("video-thumb-v1-ch%d-file%d-size%d-t%d", file.ChannelID, file.FileID, file.StoredSize, bucket)
}

func thumbnailBucket(seconds float64, size int64) int {
	if size <= 0 || seconds < 0 {
		return -1
	}
	bucket := int((seconds + float64(videoThumbIntervalSeconds)/2) / float64(videoThumbIntervalSeconds))
	bucket *= videoThumbIntervalSeconds
	if bucket < 0 {
		return 0
	}
	return bucket
}

type MPVThumbnailGenerator struct {
	path string
}

func NewMPVThumbnailGenerator() *MPVThumbnailGenerator {
	path, _ := findMPVBinary()
	return &MPVThumbnailGenerator{path: path}
}

func (g *MPVThumbnailGenerator) Available() bool {
	return g != nil && g.path != ""
}

func (g *MPVThumbnailGenerator) GenerateVideoThumbnail(ctx context.Context, sourceURL, outPath string, seconds int) error {
	if !g.Available() {
		return ErrThumbnailUnavailable
	}
	dir := filepath.Dir(outPath)
	// mpv's image VO owns the output filename. This glob-diff attribution is
	// safe because videoThumbnailer runs one worker per session.
	before, _ := filepath.Glob(filepath.Join(dir, "*.jpg"))
	beforeSet := make(map[string]struct{}, len(before))
	for _, path := range before {
		beforeSet[path] = struct{}{}
	}

	cmd := exec.CommandContext(ctx, g.path,
		"--no-config",
		"--really-quiet",
		"--terminal=no",
		"--force-window=no",
		"--ytdl=no",
		"--vo=image",
		"--vo-image-format=jpg",
		"--vo-image-jpeg-quality=72",
		"--vo-image-outdir="+dir,
		"--frames=1",
		"--start="+strconv.Itoa(seconds),
		"--hr-seek=no",
		"--aid=no",
		"--sid=no",
		"--vf=scale=192:-2",
		"--demuxer-readahead-secs=0.5",
		"--demuxer-max-bytes=4194304",
		"--",
		sourceURL,
	)
	output, err := cmd.CombinedOutput()
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("media: mpv thumbnail: %w: %s", err, string(output))
	}

	after, err := filepath.Glob(filepath.Join(dir, "*.jpg"))
	if err != nil {
		return err
	}
	newest := ""
	var newestMod time.Time
	for _, path := range after {
		if _, existed := beforeSet[path]; existed {
			continue
		}
		info, err := os.Stat(path)
		if err != nil || info.IsDir() || info.Size() == 0 {
			continue
		}
		if newest == "" || info.ModTime().After(newestMod) {
			newest = path
			newestMod = info.ModTime()
		}
	}
	if newest == "" {
		return ErrThumbnailUnavailable
	}
	if newest != outPath {
		_ = os.Remove(outPath)
		if err := os.Rename(newest, outPath); err != nil {
			return err
		}
	}
	return nil
}

func findMPVBinary() (string, error) {
	if override := os.Getenv("TDRIVE_MPV_BIN"); override != "" {
		st, err := os.Stat(override)
		if err != nil || st.IsDir() {
			return "", fmt.Errorf("TDRIVE_MPV_BIN is not executable")
		}
		return override, nil
	}
	if bundled, err := bundledMPVBinaryPath(); err == nil {
		return bundled, nil
	}
	return exec.LookPath("mpv")
}

func bundledMPVBinaryPath() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	dir := filepath.Dir(exe)
	candidates := []string{
		filepath.Join(dir, "mpv"),
		filepath.Join(dir, "mpv.exe"),
		filepath.Join(dir, "..", "Resources", "media", "mpv"),
		filepath.Join(dir, "..", "Resources", "media", "mpv.exe"),
	}
	for _, candidate := range candidates {
		st, err := os.Stat(candidate)
		if err == nil && !st.IsDir() {
			return candidate, nil
		}
	}
	return "", os.ErrNotExist
}

func sweepStaleVideoThumbnailDirs() {
	entries, err := os.ReadDir(os.TempDir())
	if err != nil {
		return
	}
	cutoff := time.Now().Add(-videoThumbTempMaxAge)
	for _, entry := range entries {
		if !entry.IsDir() || !strings.HasPrefix(entry.Name(), videoThumbTempPrefix) {
			continue
		}
		info, err := entry.Info()
		if err != nil || info.ModTime().After(cutoff) {
			continue
		}
		_ = os.RemoveAll(filepath.Join(os.TempDir(), entry.Name()))
	}
}
