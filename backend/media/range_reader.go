package media

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strconv"
	"sync"
	"time"

	"TDrive/backend/tgclient"

	"golang.org/x/sync/singleflight"
)

const (
	defaultRangeCacheBytes        = 32 * 1024 * 1024
	defaultRangeConcurrency       = 4
	rangeUploadBoundary     int64 = int64(tgclient.RangeReadMaxBytes)

	// openingChunkBytes bounds the first foreground read of a file.
	//
	// A player reads a small header before it can show anything, but a block is
	// a megabyte because that is Telegram's per-request maximum. On a slow link
	// most of a video's startup delay is spent waiting for the rest of that
	// block. Serving a short prefix first cuts the wait, and the full block is
	// filled behind it so later reads still hit the cache. Stays 4 KB-aligned,
	// which upload.getFile requires.
	openingChunkBytes int64 = 256 * 1024
)

// defaultRangeRetryPolicy tunes the shared tgclient retry policy for playback:
// FLOOD_WAITs are honored in full up to a bound, and transient transport drops
// are retried quickly so a live stream does not stall behind long backoffs.
func defaultRangeRetryPolicy() tgclient.FloodWaitRetryPolicy {
	return tgclient.FloodWaitRetryPolicy{
		MaxRetries:          5,
		MaxWait:             2 * time.Minute,
		MaxTotalWait:        5 * time.Minute,
		MaxTransientRetries: 3,
		TransientBackoff:    250 * time.Millisecond,
		MaxTransientBackoff: 3 * time.Second,
		TransientJitter:     100 * time.Millisecond,
	}
}

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

	// Retry bounds per-block FLOOD_WAIT and transient transport retries. Nil
	// uses defaultRangeRetryPolicy.
	Retry *tgclient.FloodWaitRetryPolicy

	// OnFloodWait, when set, is called when a block read hits a Telegram
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
	ctx             context.Context
	cancel          context.CancelFunc
	client          tgclient.RangeClient
	cache           *blockCache
	meter           *throughputMeter
	sem             chan struct{}
	foregroundGroup singleflight.Group
	backgroundGroup singleflight.Group
	prefetchMu      sync.Mutex
	prefetching     map[string]struct{}
	prefetchBlocks  int
	background      bool
	retry           tgclient.FloodWaitRetryPolicy
	onFloodWait     func(time.Duration)
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
	retry := defaultRangeRetryPolicy()
	if cfg.Retry != nil {
		retry = *cfg.Retry
	}
	baseCtx := cfg.Context
	if baseCtx == nil {
		baseCtx = context.Background()
	}
	ctx, cancel := context.WithCancel(baseCtx)
	return &RangeReader{
		ctx:            ctx,
		cancel:         cancel,
		client:         cfg.Client,
		cache:          newBlockCache(maxCache),
		meter:          newThroughputMeter(),
		sem:            make(chan struct{}, concurrency),
		prefetching:    make(map[string]struct{}),
		prefetchBlocks: cfg.PrefetchBlocks,
		background:     cfg.Background,
		retry:          retry,
		onFloodWait:    cfg.OnFloodWait,
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
		block, blockOffset, err := r.span(ctx, ref, absolute, blockStart)
		if err != nil {
			if done > 0 {
				return done, err
			}
			return 0, err
		}
		inside := int(absolute - blockOffset)
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

// span returns bytes covering absolute together with the offset they start at.
// Everything but the opening of a file is served a block at a time.
func (r *RangeReader) span(ctx context.Context, ref tgclient.DocumentRef, absolute, blockStart int64) ([]byte, int64, error) {
	if r.wantsOpeningPrefix(ref, absolute, blockStart) {
		data, err := r.openingSpan(ctx, ref)
		if err == nil {
			return data, 0, nil
		}
		// Any failure falls through to the ordinary block read below.
	}
	data, err := r.block(ctx, ref, blockStart, r.background)
	return data, blockStart, err
}

// wantsOpeningPrefix reports whether this read is the latency-critical first
// touch of a file. Background readers are excluded: they are speculative, so a
// short read would only cost them an extra request.
func (r *RangeReader) wantsOpeningPrefix(ref tgclient.DocumentRef, absolute, blockStart int64) bool {
	if r.background || blockStart != 0 || absolute >= openingChunkBytes {
		return false
	}
	if ref.Size <= openingChunkBytes {
		return false
	}
	_, full := r.cache.get(blockKey(ref, 0))
	return !full
}

func (r *RangeReader) openingSpan(ctx context.Context, ref tgclient.DocumentRef) ([]byte, error) {
	data, err := r.coalescedSpan(ctx, ref, openingKey(ref), 0, int(openingChunkBytes), false)
	if err != nil {
		return nil, err
	}
	// The full block follows behind so the next read is warm. It re-transfers
	// the prefix, which is a deliberate trade: 256 KB of duplicate background
	// traffic against roughly a four-fold cut in how long a video takes to start.
	r.prefetchBlock(ref, 0)
	return data, nil
}

func (r *RangeReader) block(ctx context.Context, ref tgclient.DocumentRef, blockStart int64, background bool) ([]byte, error) {
	return r.coalescedSpan(ctx, ref, blockKey(ref, blockStart), blockStart, blockLimit(ref.Size, blockStart), background)
}

func (r *RangeReader) coalescedSpan(ctx context.Context, ref tgclient.DocumentRef, key string, start int64, limit int, background bool) ([]byte, error) {
	if limit <= 0 {
		return nil, io.EOF
	}
	if data, ok := r.cache.get(key); ok {
		return data, nil
	}
	group := &r.foregroundGroup
	if background {
		group = &r.backgroundGroup
	}
	// Foreground and background reads intentionally do not coalesce with each
	// other. A speculative thumbnail/prefetch fetch may duplicate one block of
	// network work, but it can never make live playback wait behind background
	// getFile slots. Both groups share the cache, so completed work is still reused.
	ch := group.DoChan(key, func() (any, error) {
		if data, ok := r.cache.get(key); ok {
			return data, nil
		}
		// The shared fetch is tied to the reader lifetime, not the first
		// caller's request context. Otherwise one aborted HTTP request could
		// poison coalesced waiters for the same block.
		data, err := r.fetchSpan(r.ctx, ref, start, limit, background)
		if err != nil {
			return nil, err
		}
		if r.cache != nil {
			r.cache.put(key, data)
		}
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

func (r *RangeReader) fetchSpan(ctx context.Context, ref tgclient.DocumentRef, start int64, limit int, background bool) ([]byte, error) {
	if limit <= 0 {
		return nil, io.EOF
	}

	// Foreground latency is what a viewer actually waits on when a stream opens
	// or seeks, so it is worth being able to see it in the log.
	started := time.Now()
	defer func() {
		if !background {
			slog.Debug("media: fetched block from telegram", "offset", start, "bytes", limit, "elapsed", time.Since(started))
		}
	}()

	buf := make([]byte, limit)
	err := r.retry.Do(ctx, func() error {
		n, err := r.fetchBlockOnce(ctx, ref, start, buf, background)
		if n > 0 && r.meter != nil {
			r.meter.Add(n)
		}
		if n == len(buf) && (err == nil || errors.Is(err, io.EOF)) {
			return nil
		}
		if err == nil {
			err = io.ErrUnexpectedEOF
		}
		if wait, ok := tgclient.FloodWaitDuration(err); ok {
			if r.meter != nil {
				r.meter.NoteFloodWait(wait)
			}
			if r.onFloodWait != nil {
				r.onFloodWait(wait)
			}
		}
		return err
	})
	if err != nil {
		return nil, err
	}
	return buf, nil
}

func (r *RangeReader) fetchBlockOnce(ctx context.Context, ref tgclient.DocumentRef, blockStart int64, buf []byte, background bool) (int, error) {
	if err := r.acquire(ctx); err != nil {
		return 0, err
	}
	releaseGetFile, err := acquireGetFileSlot(ctx, background)
	if err != nil {
		r.release()
		return 0, err
	}
	n, err := r.client.ReadDocumentRange(ctx, ref, blockStart, buf)
	releaseGetFile()
	r.release()
	return n, err
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

// openingKey names the short prefix fetched ahead of a file's first block.
func openingKey(ref tgclient.DocumentRef) string {
	return blockKey(ref, 0) + ":open"
}

func blockKey(ref tgclient.DocumentRef, blockStart int64) string {
	return strconv.FormatInt(ref.Peer.ChannelID, 10) + ":" +
		strconv.FormatInt(ref.MsgID, 10) + ":" +
		strconv.FormatInt(blockStart, 10)
}
