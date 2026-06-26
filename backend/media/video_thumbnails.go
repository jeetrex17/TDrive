package media

import (
	"context"
	"errors"
	"fmt"
	"math"
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
	videoThumbIntervalSeconds  = 10
	videoThumbLongInterval     = 20
	videoThumbVeryLongInterval = 30
	videoThumbTimeout          = 20 * time.Second
	videoThumbTempPrefix       = "tdrive-media-thumbs-"
	videoThumbTempMaxAge       = 24 * time.Hour
	videoThumbFailureTTL       = 15 * time.Second
	videoThumbPrecomputeIdle   = 1200 * time.Millisecond
	videoThumbFloodWaitPause   = 20 * time.Second
	videoThumbDirMode          = 0o700
	videoThumbFileMode         = 0o600
	videoThumbMime             = "image/jpeg"

	// Playback-buffer watermarks (seconds ahead of the playhead) govern how much
	// the background thumbnail builder may steal from the shared pipe. Foreground
	// playback reads are already reserved at the limiter; these bands keep the
	// *background* precompute from competing while the buffer is at risk.
	//
	// HealthyStart/HealthyStop form a hysteresis band so precompute does not flap:
	// it ramps to full speed at HealthyStart and keeps going until the buffer
	// drains below HealthyStop. Below Emergency even the hovered thumbnail defers
	// so playback can refill.
	thumbBufferHealthyStart = 30.0
	thumbBufferHealthyStop  = 20.0
	thumbBufferEmergency    = 8.0

	// thumbPrecomputeAging lets one background bucket through this often even while
	// the buffer is only in the mid band, so a slow connection still builds the
	// track instead of stalling at zero.
	thumbPrecomputeAging = 3 * time.Second

	// precomputeCoarseFactor sets the coarse precompute grid as a multiple of the
	// fine interval. The builder fills the coarse grid across the whole timeline
	// first (so the entire scrubber has a preview quickly), then refines.
	precomputeCoarseFactor = 4

	// precomputeAnchorCount is the number of evenly-spaced "anchor" thumbnails the
	// builder lays down across the whole timeline before anything else, so even a
	// multi-hour movie has a sparse full-bar preview within the first few seconds.
	// Anchors fall on the coarse grid; the coarse and fine passes fill in between.
	precomputeAnchorCount = 16
)

var errThumbnailSessionDead = errors.New("media: thumbnail session dead")

// VideoThumbnailGenerator extracts one preview frame from a media URL.
// Implementations must write an image file at outPath or return an error.
type VideoThumbnailGenerator interface {
	GenerateVideoThumbnail(ctx context.Context, sourceURL, outPath string, seconds int) error
	Available() bool
}

// VideoThumbnailSession is an optional persistent extractor for one media
// session. It lets mpv keep the container/index warm across hover requests.
type VideoThumbnailSession interface {
	GenerateVideoThumbnail(ctx context.Context, outPath string, seconds int) error
	Close()
}

type statefulVideoThumbnailGenerator interface {
	NewVideoThumbnailSession(sourceURL string) (VideoThumbnailSession, error)
}

type videoThumbnailer struct {
	session        *Session
	cache          *thumbnail.Cache
	generator      VideoThumbnailGenerator
	dir            string
	cacheKeyPrefix string

	ctx            context.Context
	cancel         context.CancelFunc
	wake           chan struct{}
	precomputeWake chan struct{}
	wg             sync.WaitGroup
	extractMu      sync.Mutex

	mu                  sync.Mutex
	ready               map[int]string
	latest              int
	hasLatest           bool
	inflight            map[int]struct{}
	failed              map[int]time.Time
	instrumentLog       bool
	persistent          VideoThumbnailSession
	persistentOff       bool
	playbackTime        float64
	playbackDuration    float64
	playbackBufferAhead float64   // seconds buffered ahead of the playhead
	playbackKnown       bool      // false until the first UpdatePlayback signal
	precomputeFullSpeed bool      // hysteresis latch for the healthy buffer band
	lastPrecomputeAt    time.Time // aging floor: last background bucket generated
	thumbBackoffUntil   time.Time
	lastStatsLog        time.Time
}

func newVideoThumbnailer(session *Session, cache *thumbnail.Cache, generator VideoThumbnailGenerator) *videoThumbnailer {
	ctx, cancel := context.WithCancel(context.Background())
	t := &videoThumbnailer{
		session:        session,
		cache:          cache,
		generator:      generator,
		cacheKeyPrefix: videoThumbnailCacheKeyPrefix(session.file),
		ctx:            ctx,
		cancel:         cancel,
		wake:           make(chan struct{}, 1),
		precomputeWake: make(chan struct{}, 1),
		ready:          make(map[int]string),
		inflight:       make(map[int]struct{}),
		failed:         make(map[int]time.Time),
	}
	t.instrumentLog = os.Getenv("TDRIVE_MEDIA_THUMB_DEBUG") == "1"
	t.wg.Add(2)
	go t.worker()
	go t.precomputeWorker()
	return t
}

func (t *videoThumbnailer) Get(ctx context.Context, seconds float64) ([]byte, error) {
	if t == nil || t.session == nil || t.generator == nil || !t.generator.Available() {
		return nil, ErrThumbnailUnavailable
	}
	bucket := thumbnailBucket(seconds, t.durationHint())
	if bucket < 0 {
		return nil, ErrThumbnailUnavailable
	}
	if data, ok := t.cached(bucket); ok {
		t.logf("hit bucket=%d bytes=%d", bucket, len(data))
		return data, nil
	}
	if err := t.queue(ctx, bucket); err != nil {
		return nil, err
	}
	return nil, ErrThumbnailPending
}

func (t *videoThumbnailer) UpdatePlayback(currentTime, duration, bufferAhead float64) {
	if t == nil {
		return
	}
	if !isFiniteNonNegative(currentTime) {
		currentTime = 0
	}
	if !isFiniteNonNegative(duration) {
		duration = 0
	}
	if duration > 0 && currentTime > duration {
		currentTime = duration
	}
	if !isFiniteNonNegative(bufferAhead) {
		bufferAhead = 0
	}
	t.mu.Lock()
	t.playbackTime = currentTime
	t.playbackDuration = duration
	t.playbackBufferAhead = bufferAhead
	t.playbackKnown = true
	// Hysteresis: ramp precompute to full speed once the buffer is comfortable and
	// hold it there until the buffer drains past the lower watermark.
	if bufferAhead >= thumbBufferHealthyStart {
		t.precomputeFullSpeed = true
	} else if bufferAhead < thumbBufferHealthyStop {
		t.precomputeFullSpeed = false
	}
	t.mu.Unlock()
	// Re-evaluate background work on every signal; the worker bails if the band no
	// longer allows it.
	t.wakePrecompute()
}

func (t *videoThumbnailer) NoteFloodWait(wait time.Duration) {
	if t == nil {
		return
	}
	pause := videoThumbFloodWaitPause
	if wait > pause {
		pause = wait
	}
	until := time.Now().Add(pause)
	t.mu.Lock()
	if until.After(t.thumbBackoffUntil) {
		t.thumbBackoffUntil = until
	}
	t.mu.Unlock()
	t.logf("precompute paused after flood-wait for %s", pause.Round(time.Second))
}

func (t *videoThumbnailer) LogStats(stats MediaStats) {
	if t == nil || !t.instrumentLog {
		return
	}
	now := time.Now()
	t.mu.Lock()
	if !t.lastStatsLog.IsZero() && now.Sub(t.lastStatsLog) < time.Second {
		t.mu.Unlock()
		return
	}
	t.lastStatsLog = now
	t.mu.Unlock()

	floodWait := 0.0
	if stats.Playback.RecentFloodWait {
		floodWait = stats.Playback.LastFloodWaitSeconds
	} else if stats.Thumbnails.RecentFloodWait {
		floodWait = stats.Thumbnails.LastFloodWaitSeconds
	}
	t.logf(
		"stream stats playback=%s thumbs=%s floodwait=%.0fs",
		formatThroughputLog(stats.Playback.BytesPerSecond),
		formatThroughputLog(stats.Thumbnails.BytesPerSecond),
		floodWait,
	)
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
	t.extractMu.Lock()
	t.resetPersistent()
	t.extractMu.Unlock()
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
	if failedAt, failed := t.failed[bucket]; failed && time.Since(failedAt) < videoThumbFailureTTL {
		t.mu.Unlock()
		return ErrThumbnailUnavailable
	}
	if _, ok := t.inflight[bucket]; ok {
		t.mu.Unlock()
		return nil
	}
	t.latest = bucket
	t.hasLatest = true
	t.mu.Unlock()

	select {
	case t.wake <- struct{}{}:
		return nil
	case <-t.ctx.Done():
		return ErrSessionNotFound
	case <-ctx.Done():
		return ctx.Err()
	default:
		return nil
	}
}

func (t *videoThumbnailer) worker() {
	defer t.wg.Done()
	for {
		select {
		case <-t.ctx.Done():
			return
		case <-t.wake:
			for {
				bucket, ok := t.takeLatest()
				if !ok {
					break
				}
				if !t.waitForForegroundTurn(bucket) {
					continue
				}
				t.generate(bucket, false)
			}
			t.wakePrecompute()
		}
	}
}

func (t *videoThumbnailer) precomputeWorker() {
	defer t.wg.Done()
	timer := time.NewTimer(videoThumbPrecomputeIdle)
	defer timer.Stop()

	for {
		select {
		case <-t.ctx.Done():
			return
		case <-t.precomputeWake:
		case <-timer.C:
		}

		for {
			// Stop promptly on shutdown. Without this the loop could spin: a
			// canceled t.ctx makes generate fail instantly, and a deferred result
			// is intentionally not marked failed, so the same bucket is re-picked.
			if t.ctx.Err() != nil {
				return
			}
			bucket, ok := t.nextPrecomputeBucket()
			if !ok {
				break
			}
			t.logf("precompute bucket=%d", bucket)
			t.generate(bucket, true)

			t.mu.Lock()
			_, produced := t.ready[bucket]
			if produced {
				t.lastPrecomputeAt = time.Now()
			}
			t.mu.Unlock()
			// If the bucket did not actually generate (deferred/failed/canceled),
			// don't immediately re-pick it. Yield to the idle timer so retries are
			// spaced instead of hot-looping on a stuck bucket.
			if !produced || !t.precomputeStillIdle() {
				break
			}
		}
		resetTimer(timer, videoThumbPrecomputeIdle)
	}
}

func (t *videoThumbnailer) takeLatest() (int, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.hasLatest {
		return 0, false
	}
	bucket := t.latest
	t.hasLatest = false
	return bucket, true
}

// foregroundShouldWaitLocked reports whether a hovered (foreground) thumbnail
// must defer. Foreground means the user is actively looking at this point on the
// timeline, so only a recent Telegram FLOOD_WAIT blocks it. Buffer-ahead can be
// unknown or underreported by native players, and treating that as an emergency
// made Windows previews stay pending forever.
func (t *videoThumbnailer) foregroundShouldWaitLocked(now time.Time) bool {
	if now.Before(t.thumbBackoffUntil) {
		return true
	}
	return false
}

// precomputeAllowedLocked reports whether a background precompute bucket may run
// now: full speed while the buffer is healthy, an occasional aging tick in the
// mid band so slow links still build, and never while the buffer is critically
// low or Telegram is rate-limiting. Caller holds t.mu.
func (t *videoThumbnailer) precomputeAllowedLocked(now time.Time) bool {
	if now.Before(t.thumbBackoffUntil) {
		return false
	}
	if !t.playbackKnown {
		// Build immediately on open, before the first buffer signal arrives.
		return true
	}
	if t.playbackBufferAhead < thumbBufferEmergency {
		return false
	}
	if t.precomputeFullSpeed {
		return true
	}
	return now.Sub(t.lastPrecomputeAt) >= thumbPrecomputeAging
}

func (t *videoThumbnailer) waitForForegroundTurn(bucket int) bool {
	logged := false
	for {
		now := time.Now()
		t.mu.Lock()
		stale := t.hasLatest
		wait := t.foregroundShouldWaitLocked(now)
		t.mu.Unlock()
		if stale {
			t.logf("foreground preempted bucket=%d", bucket)
			return false
		}
		if !wait {
			return true
		}
		if !logged {
			t.logf("foreground deferred bucket=%d while playback buffer is low", bucket)
			logged = true
		}
		select {
		case <-t.ctx.Done():
			return false
		case <-time.After(250 * time.Millisecond):
		}
	}
}

func (t *videoThumbnailer) nextPrecomputeBucket() (int, bool) {
	if !t.precomputeStillIdle() {
		return 0, false
	}

	now := time.Now()
	t.mu.Lock()
	current := t.playbackTime
	duration := t.playbackDuration
	t.mu.Unlock()
	if duration <= 0 {
		return 0, false
	}

	fine := thumbnailInterval(duration)
	coarse := fine * precomputeCoarseFactor
	// Tiered coarse-to-fine. Each pass walks outward from the playhead:
	//  1. anchors  — ~16 evenly-spaced frames so the whole bar has a preview ASAP,
	//  2. coarse   — fills the coarse grid,
	//  3. fine     — refines into every bucket.
	// Anchors and coarse buckets are multiples of the fine interval, so they share
	// the same grid the frontend requests on hover.
	if anchor := anchorPrecomputeInterval(duration, coarse); anchor > coarse {
		if bucket, ok := t.scanPrecompute(current, duration, anchor, now); ok {
			return bucket, true
		}
	}
	if coarse > fine {
		if bucket, ok := t.scanPrecompute(current, duration, coarse, now); ok {
			return bucket, true
		}
	}
	return t.scanPrecompute(current, duration, fine, now)
}

// scanPrecompute walks the timeline outward from the current playhead at the
// given bucket interval (ahead first, then behind) and returns the nearest bucket
// that still needs generating, if any.
func (t *videoThumbnailer) scanPrecompute(current, duration float64, interval int, now time.Time) (int, bool) {
	if interval <= 0 {
		return 0, false
	}
	currentBucket := int(math.Floor(current/float64(interval))) * interval
	maxBucket := int(math.Floor(duration/float64(interval))) * interval
	for step := 0; step <= maxBucket+interval; step += interval {
		for _, bucket := range precomputeCandidates(currentBucket, step, maxBucket) {
			if t.precomputeCandidateReady(bucket, now) {
				return bucket, true
			}
		}
	}
	return 0, false
}

// anchorPrecomputeInterval returns the spacing for the evenly-spaced anchor pass,
// snapped to (a multiple of) the coarse grid so anchors stay on the shared bucket
// grid. It returns 0 when the video is short enough that an anchor tier coarser
// than the coarse grid would be pointless.
func anchorPrecomputeInterval(duration float64, coarse int) int {
	if coarse <= 0 || duration <= 0 {
		return 0
	}
	steps := int(duration) / (precomputeAnchorCount * coarse)
	if steps < 1 {
		return 0
	}
	return steps * coarse
}

func precomputeCandidates(currentBucket, step, maxBucket int) []int {
	if step == 0 {
		return []int{currentBucket}
	}
	out := make([]int, 0, 2)
	if ahead := currentBucket + step; ahead <= maxBucket {
		out = append(out, ahead)
	}
	if behind := currentBucket - step; behind >= 0 {
		out = append(out, behind)
	}
	return out
}

func (t *videoThumbnailer) precomputeCandidateReady(bucket int, now time.Time) bool {
	if bucket < 0 {
		return false
	}
	t.mu.Lock()
	if t.hasLatest || len(t.inflight) > 0 || !t.precomputeAllowedLocked(now) {
		t.mu.Unlock()
		return false
	}
	if _, ready := t.ready[bucket]; ready {
		t.mu.Unlock()
		return false
	}
	if failedAt, failed := t.failed[bucket]; failed && now.Sub(failedAt) < videoThumbFailureTTL {
		t.mu.Unlock()
		return false
	}
	t.mu.Unlock()
	return !t.cache.Has(t.cacheKey(bucket))
}

func (t *videoThumbnailer) precomputeStillIdle() bool {
	if t == nil {
		return false
	}
	now := time.Now()
	t.mu.Lock()
	defer t.mu.Unlock()
	return !t.hasLatest && len(t.inflight) == 0 && t.precomputeAllowedLocked(now)
}

func (t *videoThumbnailer) precomputeShouldYield() bool {
	if t == nil {
		return true
	}
	now := time.Now()
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.hasLatest || !t.precomputeAllowedLocked(now)
}

func (t *videoThumbnailer) wakePrecompute() {
	if t == nil {
		return
	}
	select {
	case t.precomputeWake <- struct{}{}:
	default:
	}
}

func (t *videoThumbnailer) durationHint() float64 {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.playbackDuration
}

func (t *videoThumbnailer) generate(bucket int, background bool) {
	start := time.Now()
	t.mu.Lock()
	if _, ok := t.ready[bucket]; ok {
		t.mu.Unlock()
		return
	}
	if _, ok := t.inflight[bucket]; ok {
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
		t.logf("persistent-hit bucket=%d background=%t", bucket, background)
		return
	}

	dir, err := t.ensureDir()
	if err != nil {
		t.markFailed(bucket, err, background)
		return
	}
	outPath := filepath.Join(dir, fmt.Sprintf("thumb-%d.jpg", bucket))
	sourceURL := t.session.thumbnailSourceURL()
	if sourceURL == "" {
		t.markFailed(bucket, fmt.Errorf("missing source URL"), background)
		return
	}

	t.extractMu.Lock()
	defer t.extractMu.Unlock()

	if background && t.precomputeShouldYield() {
		t.logf("precompute preempted bucket=%d before mpv", bucket)
		return
	}

	runCtx, cancel := context.WithTimeout(t.ctx, videoThumbTimeout)
	defer cancel()
	if err := t.generateThumbnail(runCtx, sourceURL, outPath, bucket); err != nil {
		_ = os.Remove(outPath)
		t.markFailed(bucket, err, background)
		return
	}
	_ = os.Chmod(outPath, videoThumbFileMode)

	data, err := os.ReadFile(outPath)
	if err != nil || len(data) == 0 {
		_ = os.Remove(outPath)
		if err == nil {
			err = fmt.Errorf("empty output")
		}
		t.markFailed(bucket, err, background)
		return
	}
	_ = t.cache.Put(t.cacheKey(bucket), data)

	t.mu.Lock()
	t.ready[bucket] = outPath
	delete(t.failed, bucket)
	t.mu.Unlock()
	t.logf("generated bucket=%d bytes=%d took=%s background=%t", bucket, len(data), time.Since(start).Round(time.Millisecond), background)
}

func (t *videoThumbnailer) generateThumbnail(ctx context.Context, sourceURL, outPath string, bucket int) error {
	if t == nil || t.generator == nil {
		return ErrThumbnailUnavailable
	}
	if !t.persistentOff {
		err := t.generatePersistent(ctx, sourceURL, outPath, bucket)
		if err == nil {
			return nil
		}
		if thumbnailWorkDeferred(err) {
			t.logf("persistent deferred bucket=%d err=%v", bucket, err)
			return err
		}
		if errors.Is(err, errThumbnailSessionDead) {
			t.logf("persistent extractor restarting after bucket=%d err=%v", bucket, err)
			t.resetPersistent()
			err = t.generatePersistent(ctx, sourceURL, outPath, bucket)
			if err == nil {
				return nil
			}
			if thumbnailWorkDeferred(err) {
				t.logf("persistent deferred after restart bucket=%d err=%v", bucket, err)
				return err
			}
			if errors.Is(err, errThumbnailSessionDead) {
				t.logf("persistent extractor disabled after restart bucket=%d err=%v", bucket, err)
				t.disablePersistent()
			} else {
				t.logf("persistent extractor fell back after restart bucket=%d err=%v", bucket, err)
			}
		} else if !errors.Is(err, ErrThumbnailUnavailable) {
			t.logf("persistent extractor fell back bucket=%d err=%v", bucket, err)
		}
	}
	return t.generator.GenerateVideoThumbnail(ctx, sourceURL, outPath, bucket)
}

func (t *videoThumbnailer) generatePersistent(ctx context.Context, sourceURL, outPath string, bucket int) error {
	session, err := t.thumbnailSession(sourceURL)
	if err != nil {
		return err
	}
	if session == nil {
		return ErrThumbnailUnavailable
	}
	if err := session.GenerateVideoThumbnail(ctx, outPath, bucket); err != nil {
		return err
	}
	t.logf("persistent bucket=%d", bucket)
	return nil
}

func (t *videoThumbnailer) thumbnailSession(sourceURL string) (VideoThumbnailSession, error) {
	if t.persistent != nil {
		return t.persistent, nil
	}
	stateful, ok := t.generator.(statefulVideoThumbnailGenerator)
	if !ok {
		return nil, nil
	}
	session, err := stateful.NewVideoThumbnailSession(sourceURL)
	if err != nil {
		return nil, err
	}
	t.persistent = session
	return session, nil
}

func (t *videoThumbnailer) resetPersistent() {
	if t.persistent != nil {
		t.persistent.Close()
		t.persistent = nil
	}
}

func (t *videoThumbnailer) disablePersistent() {
	t.resetPersistent()
	t.persistentOff = true
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

func (t *videoThumbnailer) markFailed(bucket int, err error, background bool) {
	if err == nil {
		err = ErrThumbnailUnavailable
	}
	if thumbnailWorkDeferred(err) {
		t.logf("deferred bucket=%d err=%v", bucket, err)
		return
	}
	if background {
		t.logf("precompute skipped bucket=%d err=%v", bucket, err)
		return
	}
	t.mu.Lock()
	t.failed[bucket] = time.Now()
	t.mu.Unlock()
	t.logf("failed bucket=%d err=%v", bucket, err)
}

func (t *videoThumbnailer) logf(format string, args ...any) {
	if t == nil || !t.instrumentLog {
		return
	}
	fmt.Fprintf(os.Stderr, "tdrive video thumbnail: "+format+"\n", args...)
}

func formatThroughputLog(bytesPerSecond int64) string {
	if bytesPerSecond < 1024 {
		return fmt.Sprintf("%dB/s", bytesPerSecond)
	}
	if bytesPerSecond < 1024*1024 {
		return fmt.Sprintf("%.1fKB/s", float64(bytesPerSecond)/1024)
	}
	return fmt.Sprintf("%.1fMB/s", float64(bytesPerSecond)/(1024*1024))
}

func thumbnailWorkDeferred(err error) bool {
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}

func (t *videoThumbnailer) cacheKey(bucket int) string {
	return t.cacheKeyPrefix + strconv.Itoa(bucket)
}

func videoThumbnailCacheKeyPrefix(file LogicalFile) string {
	return "video-thumb-v1-ch" + strconv.FormatInt(file.ChannelID, 10) +
		"-file" + strconv.FormatInt(file.FileID, 10) +
		"-size" + strconv.FormatInt(file.StoredSize, 10) +
		"-t"
}

func thumbnailBucket(seconds, duration float64) int {
	if seconds < 0 {
		return -1
	}
	interval := thumbnailInterval(duration)
	bucket := int((seconds + float64(interval)/2) / float64(interval))
	bucket *= interval
	if bucket < 0 {
		return 0
	}
	return bucket
}

func thumbnailInterval(duration float64) int {
	switch {
	case duration >= 2*60*60:
		return videoThumbVeryLongInterval
	case duration >= 30*60:
		return videoThumbLongInterval
	default:
		return videoThumbIntervalSeconds
	}
}

func isFiniteNonNegative(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0) && value >= 0
}

func resetTimer(timer *time.Timer, d time.Duration) {
	if !timer.Stop() {
		select {
		case <-timer.C:
		default:
		}
	}
	timer.Reset(d)
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
		filepath.Join(dir, "media", "mpv"),
		filepath.Join(dir, "media", "mpv.exe"),
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
