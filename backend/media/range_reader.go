package media

import (
	"context"
	"fmt"
	"io"
	"strconv"
	"sync"
	"time"

	"TDrive/backend/tgclient"

	"golang.org/x/sync/singleflight"
)

const (
	defaultRangeCacheBytes         = 32 * 1024 * 1024
	defaultRangeConcurrency        = 4
	defaultFloodWaitRetries        = 5
	defaultFloodWaitMaxSleep       = 60 * time.Second
	rangeUploadBoundary      int64 = int64(tgclient.RangeReadMaxBytes)
)

type RangeReaderConfig struct {
	Client tgclient.RangeClient

	// Context controls shared background block fetches. Per-call contexts still
	// cancel that caller's wait immediately; this context cancels in-flight
	// shared work for shutdown. Nil uses context.Background().
	Context context.Context

	// MaxCacheBytes bounds the in-memory block cache. 0 uses a conservative
	// default; negative disables caching.
	MaxCacheBytes int64

	// MaxConcurrency bounds simultaneous low-level range calls. 0 uses the
	// default. Duplicate block reads are still coalesced before they hit this.
	MaxConcurrency int

	// FloodWaitRetries bounds per-block FLOOD_WAIT retries. 0 uses the default.
	FloodWaitRetries int

	// FloodWaitMaxSleep caps a single FLOOD_WAIT sleep. 0 uses the default.
	FloodWaitMaxSleep time.Duration

	// OnFloodWait, when set, is called before sleeping for a Telegram
	// FLOOD_WAIT. It is a logging/progress hook; nil is fine.
	OnFloodWait func(wait time.Duration)

	// PrefetchBlocks asynchronously warms this many sequential 1 MiB blocks
	// after a foreground read. 0 disables prefetching.
	PrefetchBlocks int

	// Background routes this reader's fetches through the shared background getFile
	// pool instead of the foreground playback reserve. Set it for non-playback
	// readers (such as the thumbnail reader) so their reads yield to live
	// playback. Prefetch fetches are always treated as background regardless.
	Background bool
}

// RangeReader turns arbitrary app byte reads into Telegram-compatible block
// reads: 1 MiB boundary splitting, 4 KiB alignment, request coalescing, an LRU
// block cache, bounded concurrency, and FLOOD_WAIT backoff.
type RangeReader struct {
	ctx              context.Context
	cancel           context.CancelFunc
	client           tgclient.RangeClient
	cache            *blockCache
	meter            *throughputMeter
	sem              chan struct{}
	group            singleflight.Group
	prefetchMu       sync.Mutex
	prefetching      map[string]struct{}
	prefetchBlocks   int
	background       bool
	floodWaitRetries int
	floodWaitMax     time.Duration
	onFloodWait      func(time.Duration)
}

func NewRangeReader(cfg RangeReaderConfig) *RangeReader {
	maxCache := cfg.MaxCacheBytes
	if maxCache == 0 {
		maxCache = defaultRangeCacheBytes
	}
	concurrency := cfg.MaxConcurrency
	if concurrency <= 0 {
		concurrency = defaultRangeConcurrency
	}
	retries := cfg.FloodWaitRetries
	if retries <= 0 {
		retries = defaultFloodWaitRetries
	}
	maxSleep := cfg.FloodWaitMaxSleep
	if maxSleep <= 0 {
		maxSleep = defaultFloodWaitMaxSleep
	}
	baseCtx := cfg.Context
	if baseCtx == nil {
		baseCtx = context.Background()
	}
	ctx, cancel := context.WithCancel(baseCtx)
	return &RangeReader{
		ctx:              ctx,
		cancel:           cancel,
		client:           cfg.Client,
		cache:            newBlockCache(maxCache),
		meter:            newThroughputMeter(),
		sem:              make(chan struct{}, concurrency),
		prefetching:      make(map[string]struct{}),
		prefetchBlocks:   cfg.PrefetchBlocks,
		background:       cfg.Background,
		floodWaitRetries: retries,
		floodWaitMax:     maxSleep,
		onFloodWait:      cfg.OnFloodWait,
	}
}

func (r *RangeReader) Close() {
	if r != nil && r.cancel != nil {
		r.cancel()
	}
}

func (r *RangeReader) Throughput() ThroughputStats {
	if r == nil || r.meter == nil {
		return ThroughputStats{}
	}
	return r.meter.Stats()
}

// ReadStoredAt reads stored Telegram bytes from ref into p. It follows
// io.ReaderAt EOF semantics: a short read caused by reaching the end of the
// document returns io.EOF with the bytes that were available.
func (r *RangeReader) ReadStoredAt(ctx context.Context, ref tgclient.DocumentRef, p []byte, off int64) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if r == nil || r.client == nil {
		return 0, fmt.Errorf("media: range client not ready")
	}
	if off < 0 {
		return 0, fmt.Errorf("media: negative range offset")
	}
	if ref.Size <= 0 || off >= ref.Size {
		return 0, io.EOF
	}

	want := len(p)
	available := ref.Size - off
	if int64(want) > available {
		want = int(available)
	}

	done := 0
	for done < want {
		if err := ctx.Err(); err != nil {
			return done, err
		}
		absolute := off + int64(done)
		blockStart := blockStartFor(absolute)
		block, err := r.block(ctx, ref, blockStart, r.background)
		if err != nil {
			if done > 0 {
				return done, err
			}
			return 0, err
		}
		inside := int(absolute - blockStart)
		if inside >= len(block) {
			if done > 0 {
				return done, io.EOF
			}
			return 0, io.EOF
		}
		n := copy(p[done:want], block[inside:])
		done += n
	}
	if done < len(p) {
		r.prefetchAfter(ref, off+int64(done))
		return done, io.EOF
	}
	r.prefetchAfter(ref, off+int64(done))
	return done, nil
}

func (r *RangeReader) prefetchAfter(ref tgclient.DocumentRef, nextOffset int64) {
	if r == nil || r.prefetchBlocks <= 0 || nextOffset <= 0 || nextOffset >= ref.Size {
		return
	}
	nextBlock := blockStartFor(nextOffset)
	if nextBlock < nextOffset {
		nextBlock += rangeUploadBoundary
	}
	for i := 0; i < r.prefetchBlocks && nextBlock < ref.Size; i++ {
		r.prefetchBlock(ref, nextBlock)
		nextBlock += rangeUploadBoundary
	}
}

func (r *RangeReader) prefetchBlock(ref tgclient.DocumentRef, blockStart int64) {
	key := blockKey(ref, blockStart)
	if _, ok := r.cache.get(key); ok {
		return
	}
	r.prefetchMu.Lock()
	if _, ok := r.prefetching[key]; ok {
		r.prefetchMu.Unlock()
		return
	}
	r.prefetching[key] = struct{}{}
	r.prefetchMu.Unlock()

	go func() {
		defer func() {
			r.prefetchMu.Lock()
			delete(r.prefetching, key)
			r.prefetchMu.Unlock()
		}()
		// Prefetch is speculative read-ahead, so it always yields to live playback.
		_, _ = r.block(r.ctx, ref, blockStart, true)
	}()
}

func (r *RangeReader) block(ctx context.Context, ref tgclient.DocumentRef, blockStart int64, background bool) ([]byte, error) {
	key := blockKey(ref, blockStart)
	if data, ok := r.cache.get(key); ok {
		return data, nil
	}

	ch := r.group.DoChan(key, func() (any, error) {
		if data, ok := r.cache.get(key); ok {
			return data, nil
		}
		// The shared fetch is tied to the reader lifetime, not the first
		// caller's request context. Otherwise one aborted HTTP request could
		// poison coalesced waiters for the same block.
		data, err := r.fetchBlock(r.ctx, ref, blockStart, background)
		if err != nil {
			return nil, err
		}
		r.cache.put(key, data)
		return data, nil
	})

	select {
	case res := <-ch:
		if res.Err != nil {
			return nil, res.Err
		}
		data, ok := res.Val.([]byte)
		if !ok {
			return nil, fmt.Errorf("media: invalid range cache value")
		}
		return data, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (r *RangeReader) fetchBlock(ctx context.Context, ref tgclient.DocumentRef, blockStart int64, background bool) ([]byte, error) {
	limit := blockLimit(ref.Size, blockStart)
	if limit <= 0 {
		return nil, io.EOF
	}

	buf := make([]byte, limit)
	for attempt := 0; ; attempt++ {
		if err := r.acquire(ctx); err != nil {
			return nil, err
		}
		releaseGetFile, err := acquireGetFileSlot(ctx, background)
		if err != nil {
			r.release()
			return nil, err
		}
		n, err := r.client.ReadDocumentRange(ctx, ref, blockStart, buf)
		releaseGetFile()
		r.release()
		if n > 0 && r.meter != nil {
			r.meter.Add(n)
		}
		if err == nil {
			if n != len(buf) {
				return nil, io.ErrUnexpectedEOF
			}
			return buf, nil
		}
		wait, ok := tgclient.FloodWaitDuration(err)
		if !ok || attempt >= r.floodWaitRetries {
			return nil, err
		}
		if wait > r.floodWaitMax {
			wait = r.floodWaitMax
		}
		if r.meter != nil {
			r.meter.NoteFloodWait(wait)
		}
		if r.onFloodWait != nil {
			r.onFloodWait(wait)
		}
		select {
		case <-time.After(wait):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
}

func (r *RangeReader) acquire(ctx context.Context) error {
	select {
	case r.sem <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (r *RangeReader) release() {
	<-r.sem
}

// acquireGetFileSlot reserves one global getFile slot. Background reads (the
// thumbnail reader and all prefetch) go through the background pool so they yield
// to foreground playback, which keeps its reserved headroom.
func acquireGetFileSlot(ctx context.Context, background bool) (func(), error) {
	if background {
		return tgclient.AcquireBackgroundGetFileSlots(ctx, 1)
	}
	return tgclient.AcquireGetFileSlots(ctx, 1)
}

func blockStartFor(off int64) int64 {
	return (off / rangeUploadBoundary) * rangeUploadBoundary
}

func blockLimit(size, blockStart int64) int {
	if blockStart >= size {
		return 0
	}
	remaining := size - blockStart
	if remaining > rangeUploadBoundary {
		return tgclient.RangeReadMaxBytes
	}
	return int(remaining)
}

func blockKey(ref tgclient.DocumentRef, blockStart int64) string {
	return strconv.FormatInt(ref.Peer.ChannelID, 10) + ":" +
		strconv.FormatInt(ref.MsgID, 10) + ":" +
		strconv.FormatInt(blockStart, 10)
}
