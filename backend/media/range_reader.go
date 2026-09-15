package media

import (
	"bytes"
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"slices"
	"strconv"
	"sync"
	"time"

	"TDrive/backend/tgclient"

	"golang.org/x/sync/singleflight"
)

const (
	defaultRangeCacheBytes       = 64 * 1024 * 1024
	rangeUploadBoundary    int64 = int64(tgclient.RangeReadMaxBytes)

	// defaultBackgroundSlots bounds speculative fetches for a reader that asks
	// for no read-ahead window of its own, such as the thumbnail and mount
	// readers whose every fetch is background work.
	defaultBackgroundSlots = 4

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

	// Context controls shared block fetches. Per-call contexts still cancel
	// that caller's wait immediately; this context cancels in-flight shared
	// work for shutdown. Nil uses context.Background().
	Context context.Context

	// Cache is a block cache shared with another reader of the same bytes, so
	// the two never fetch a block twice. Nil builds a private cache sized by
	// MaxCacheBytes.
	Cache *blockCache

	// MaxCacheBytes bounds a private block cache. 0 uses the default; negative
	// disables caching. Ignored when Cache is set.
	MaxCacheBytes int64

	// Retry bounds per-block FLOOD_WAIT and transient transport retries. Nil
	// uses defaultRangeRetryPolicy.
	Retry *tgclient.FloodWaitRetryPolicy

	// OnFloodWait, when set, is called when a block read hits a Telegram
	// FLOOD_WAIT. It is a logging/progress hook; nil is fine.
	OnFloodWait func(wait time.Duration)

	// ReadAhead is how many whole blocks the reader keeps in flight beyond the
	// block a foreground read just consumed, and so how many speculative
	// fetches may run at once. 0 disables read-ahead.
	ReadAhead int

	// Background routes this reader's fetches through the shared background
	// getFile pool instead of the foreground playback reserve. Set it for
	// non-playback readers (such as the thumbnail reader) so their reads yield
	// to live playback. Read-ahead and warm-up fetches are always background.
	Background bool
}

// RangeReader turns arbitrary app byte reads into Telegram-compatible block
// reads: 1 MiB boundary splitting, 4 KiB alignment, request coalescing, an LRU
// block cache, a read-ahead window, bounded concurrency, and FLOOD_WAIT backoff.
//
// Every block in flight is one flight, shared by everyone who needs it. A
// player read that catches up with read-ahead joins the flight already
// running for that block, promoting it to playback priority if it is still
// waiting for a slot, instead of transferring the block a second time. When a
// seek needs more lane slots than are free, unclaimed read-ahead for the
// position the player left is cancelled to make room.
type RangeReader struct {
	ctx         context.Context
	cancel      context.CancelFunc
	client      tgclient.RangeClient
	cache       *blockCache
	meter       *throughputMeter
	retry       tgclient.FloodWaitRetryPolicy
	onFloodWait func(time.Duration)
	background  bool
	readAhead   int

	// bgSlots is the reader's lane for background fetches. Foreground reads
	// skip it: they are bounded by the global playback reserve and by the
	// sequential HTTP handler, and must never queue behind speculation.
	bgSlots chan struct{}

	mu      sync.Mutex
	flights map[string]*flight

	// refs holds the freshest reference seen per document. Telegram expires
	// file references; re-resolving once and remembering the result keeps
	// every later block at one request instead of a rejected one, a resolve,
	// and a retry.
	refMu        sync.Mutex
	refs         map[string]tgclient.DocumentRef
	refreshGroup singleflight.Group
}

// flight is one in-progress block fetch. It carries the priority its creator
// had (foreground for a player read, background for read-ahead and warm-ups)
// and whether it is speculative, meaning a seek may cancel it.
type flight struct {
	ctx    context.Context
	cancel context.CancelFunc
	done   chan struct{}
	data   []byte
	err    error

	doc         string
	start       int64
	background  bool
	speculative bool

	// claimed closes once a reader waits on the block; a seek then leaves the
	// flight alone. promoted closes when that reader is foreground, which
	// also moves the fetch to the playback reserve if it is still queued.
	claimed  chan struct{}
	promoted chan struct{}
	once     [2]sync.Once
}

func newFlight(parent context.Context, doc string, start int64, background, speculative bool) *flight {
	ctx, cancel := context.WithCancel(parent)
	return &flight{
		ctx:         ctx,
		cancel:      cancel,
		done:        make(chan struct{}),
		doc:         doc,
		start:       start,
		background:  background,
		speculative: speculative,
		claimed:     make(chan struct{}),
		promoted:    make(chan struct{}),
	}
}

func (f *flight) claim(foreground bool) {
	f.once[0].Do(func() { close(f.claimed) })
	if foreground {
		f.once[1].Do(func() { close(f.promoted) })
	}
}

func (f *flight) isClaimed() bool {
	select {
	case <-f.claimed:
		return true
	default:
		return false
	}
}

func (f *flight) isPromoted() bool {
	select {
	case <-f.promoted:
		return true
	default:
		return false
	}
}

func NewRangeReader(cfg RangeReaderConfig) *RangeReader {
	cache := cfg.Cache
	if cache == nil {
		maxCache := cfg.MaxCacheBytes
		if maxCache == 0 {
			maxCache = defaultRangeCacheBytes
		}
		cache = newBlockCache(maxCache)
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
		ctx:         ctx,
		cancel:      cancel,
		client:      cfg.Client,
		cache:       cache,
		meter:       newThroughputMeter(),
		retry:       retry,
		onFloodWait: cfg.OnFloodWait,
		background:  cfg.Background,
		readAhead:   max(cfg.ReadAhead, 0),
		bgSlots:     make(chan struct{}, max(cfg.ReadAhead, defaultBackgroundSlots)),
		flights:     make(map[string]*flight),
		refs:        make(map[string]tgclient.DocumentRef),
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
	r.readAheadAfter(ref, off+int64(done))
	if done < len(p) {
		return done, io.EOF
	}
	return done, nil
}

// readAheadAfter keeps the window of blocks beyond nextOffset in flight.
func (r *RangeReader) readAheadAfter(ref tgclient.DocumentRef, nextOffset int64) {
	if r.readAhead <= 0 || nextOffset <= 0 || nextOffset >= ref.Size {
		return
	}
	next := blockStartFor(nextOffset)
	if next < nextOffset {
		next += rangeUploadBoundary
	}
	r.readAheadFrom(ref, next)
}

// readAheadFrom makes [next, next+window) the read-ahead window and starts
// whatever blocks of it are neither cached nor in flight. When those need
// more lane slots than are free, speculative flights the player has left
// behind are cancelled to make room.
func (r *RangeReader) readAheadFrom(ref tgclient.DocumentRef, next int64) {
	if r.readAhead <= 0 || next >= ref.Size {
		return
	}
	end := min(next+int64(r.readAhead)*rangeUploadBoundary, ref.Size)

	r.mu.Lock()
	var missing []int64
	for start := next; start < end; start += rangeUploadBoundary {
		key := blockKey(ref, start)
		if _, ok := r.flights[key]; ok {
			continue
		}
		if _, ok := r.cache.get(key); ok {
			continue
		}
		missing = append(missing, start)
	}
	if free := cap(r.bgSlots) - len(r.bgSlots); len(missing) > free {
		r.evictStaleLocked(docKey(ref), next, end, len(missing)-free)
	}
	r.mu.Unlock()

	for _, start := range missing {
		r.prefetch(ref, start, true)
	}
}

// evictStaleLocked cancels up to n speculative flights of doc that lie outside
// [next, end) and that no reader has claimed, farthest from the window first.
// Eviction is driven by demand rather than by every position change, so a
// player that reads the index at the end of a file right after its head does
// not throw away the blocks it is about to come back for.
func (r *RangeReader) evictStaleLocked(doc string, next, end int64, n int) {
	var stale []*flight
	for _, f := range r.flights {
		if f.speculative && f.doc == doc && !f.isClaimed() && (f.start < next || f.start >= end) {
			stale = append(stale, f)
		}
	}
	distance := func(f *flight) int64 {
		if f.start < next {
			return next - f.start
		}
		return f.start - end
	}
	slices.SortFunc(stale, func(a, b *flight) int {
		return cmp.Compare(distance(b), distance(a))
	})
	for _, f := range stale[:min(n, len(stale))] {
		f.cancel()
	}
}

// prefetchBlock warms one block in the background, for example the container
// index at the end of a file. Unlike read-ahead it is not tied to the player's
// position, so a seek never cancels it.
func (r *RangeReader) prefetchBlock(ref tgclient.DocumentRef, blockStart int64) {
	r.prefetch(ref, blockStart, false)
}

func (r *RangeReader) prefetch(ref tgclient.DocumentRef, blockStart int64, speculative bool) {
	if r == nil || blockStart < 0 || blockStart >= ref.Size {
		return
	}
	key := blockKey(ref, blockStart)
	if _, ok := r.cache.get(key); ok {
		return
	}
	f, created := r.flightFor(key, ref, blockStart, true, speculative)
	if created {
		go r.run(f, ref, key, blockLimit(ref.Size, blockStart))
	}
}

// span returns bytes covering absolute together with the offset they start at.
// Everything but the opening of a file is served a block at a time.
func (r *RangeReader) span(ctx context.Context, ref tgclient.DocumentRef, absolute, blockStart int64) ([]byte, int64, error) {
	if r.wantsOpeningPrefix(ref, absolute, blockStart) {
		data, err := r.fetch(ctx, ref, openingKey(ref), 0, int(openingChunkBytes))
		if err == nil {
			// The full block follows behind so the next read is warm. It
			// re-transfers the prefix, which is a deliberate trade: 256 KB of
			// duplicate background traffic against roughly a four-fold cut in
			// how long a video takes to start.
			r.prefetch(ref, 0, false)
			return data, 0, nil
		}
		// Any failure falls through to the ordinary block read below.
	}
	data, err := r.block(ctx, ref, blockStart)
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

// block returns one whole block for a reader that needs it now.
func (r *RangeReader) block(ctx context.Context, ref tgclient.DocumentRef, blockStart int64) ([]byte, error) {
	return r.fetch(ctx, ref, blockKey(ref, blockStart), blockStart, blockLimit(ref.Size, blockStart))
}

// fetch returns the bytes behind key, joining the flight already fetching them
// or starting one. The caller waits under its own ctx; the fetch itself runs
// on the reader's context, so an aborted HTTP request never fails a second
// waiter for the same block.
func (r *RangeReader) fetch(ctx context.Context, ref tgclient.DocumentRef, key string, start int64, limit int) ([]byte, error) {
	if limit <= 0 {
		return nil, io.EOF
	}
	// A seek can cancel a speculative flight in the instant before this
	// reader claims it. The reader then starts its own, unconditional flight,
	// so one retry is all it ever takes.
	for attempt := 0; attempt < 2; attempt++ {
		if data, ok := r.cache.get(key); ok {
			return data, nil
		}
		if err := r.ctx.Err(); err != nil {
			return nil, err
		}
		f, created := r.flightFor(key, ref, start, r.background, false)
		if created {
			go r.run(f, ref, key, limit)
		}
		select {
		case <-f.done:
			if f.err == nil {
				return f.data, nil
			}
			if !errors.Is(f.err, context.Canceled) || ctx.Err() != nil {
				return nil, f.err
			}
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return nil, context.Canceled
}

// flightFor returns the flight for key, creating it when none is running. A
// reader joining an existing flight claims it, and a foreground reader also
// promotes it. created reports whether the caller must run the flight.
func (r *RangeReader) flightFor(key string, ref tgclient.DocumentRef, start int64, background, speculative bool) (*flight, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if f, ok := r.flights[key]; ok {
		if !speculative {
			f.claim(!background)
		}
		return f, false
	}
	f := newFlight(r.ctx, docKey(ref), start, background, speculative)
	r.flights[key] = f
	return f, true
}

// run fetches a flight's bytes and publishes them. The block enters the cache
// before the flight leaves the map, so a reader arriving in between finds one
// or the other and never starts a duplicate fetch.
func (r *RangeReader) run(f *flight, ref tgclient.DocumentRef, key string, limit int) {
	data, err := r.fetchSpan(f, ref, limit)
	if err == nil {
		r.cache.put(key, data)
	}
	r.mu.Lock()
	if r.flights[key] == f {
		delete(r.flights, key)
	}
	r.mu.Unlock()
	f.data, f.err = data, err
	close(f.done)
	f.cancel()
}

func (r *RangeReader) fetchSpan(f *flight, ref tgclient.DocumentRef, limit int) ([]byte, error) {
	// Foreground latency is what a viewer actually waits on when a stream opens
	// or seeks, so it is worth being able to see it in the log.
	started := time.Now()
	defer func() {
		if !f.background {
			slog.Debug("media: fetched block from telegram", "offset", f.start, "bytes", limit, "elapsed", time.Since(started))
		}
	}()

	buf := make([]byte, limit)
	err := r.retry.Do(f.ctx, func() error {
		n, err := r.fetchBlockOnce(f, ref, buf)
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

// fetchBlockOnce is one attempt at a flight's block. Slots are held only for
// the request itself, never across a retry backoff. A rejected file reference
// is refreshed and the request repeated once within the same attempt.
func (r *RangeReader) fetchBlockOnce(f *flight, ref tgclient.DocumentRef, buf []byte) (int, error) {
	release, err := r.acquireSlot(f)
	if err != nil {
		return 0, err
	}
	defer release()
	current := r.currentRef(ref)
	n, err := r.client.ReadDocumentRange(f.ctx, current, f.start, buf)
	if !tgclient.IsFileReferenceError(err) {
		return n, err
	}
	fresh, refreshErr := r.refreshRef(f.ctx, current)
	if refreshErr != nil {
		return 0, fmt.Errorf("media: refresh file reference after %v: %w", err, refreshErr)
	}
	return r.client.ReadDocumentRange(f.ctx, fresh, f.start, buf)
}

// currentRef returns the freshest reference known for ref's document.
func (r *RangeReader) currentRef(ref tgclient.DocumentRef) tgclient.DocumentRef {
	r.refMu.Lock()
	defer r.refMu.Unlock()
	if fresh, ok := r.refs[docKey(ref)]; ok {
		return fresh
	}
	return ref
}

// refreshRef re-resolves a document whose file reference Telegram rejected.
// Concurrent block reads share one resolve, and a reference that another
// read already replaced is handed back without a request.
func (r *RangeReader) refreshRef(ctx context.Context, stale tgclient.DocumentRef) (tgclient.DocumentRef, error) {
	key := docKey(stale)
	result, err, _ := r.refreshGroup.Do(key, func() (any, error) {
		if fresh := r.currentRef(stale); !bytes.Equal(fresh.FileReference, stale.FileReference) {
			return fresh, nil
		}
		fresh, err := r.client.ResolveDocument(ctx, stale.Peer, stale.MsgID)
		if err != nil {
			return nil, err
		}
		r.refMu.Lock()
		r.refs[key] = fresh
		r.refMu.Unlock()
		return fresh, nil
	})
	if err != nil {
		return tgclient.DocumentRef{}, err
	}
	return result.(tgclient.DocumentRef), nil
}

// acquireSlot reserves a getFile slot for one attempt of f under its current
// priority. A background flight waits in the reader's lane and then in the
// shared background pool; promotion at any point before the request starts
// moves it to the playback reserve instead, so a player never waits behind
// the background queue for a block it asked for.
func (r *RangeReader) acquireSlot(f *flight) (func(), error) {
	if !f.background || f.isPromoted() {
		return tgclient.AcquireGetFileSlots(f.ctx, 1)
	}
	select {
	case r.bgSlots <- struct{}{}:
	case <-f.promoted:
		return tgclient.AcquireGetFileSlots(f.ctx, 1)
	case <-f.ctx.Done():
		return nil, f.ctx.Err()
	}

	// The pool wait is interruptible by promotion through a derived context.
	waitCtx, stop := context.WithCancel(f.ctx)
	go func() {
		select {
		case <-f.promoted:
			stop()
		case <-waitCtx.Done():
		}
	}()
	release, err := tgclient.AcquireBackgroundGetFileSlots(waitCtx, 1)
	stop()
	if err != nil {
		<-r.bgSlots
		if f.isPromoted() && f.ctx.Err() == nil {
			return tgclient.AcquireGetFileSlots(f.ctx, 1)
		}
		return nil, err
	}
	return func() {
		release()
		<-r.bgSlots
	}, nil
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

// docKey identifies one stored document across every block key.
func docKey(ref tgclient.DocumentRef) string {
	return strconv.FormatInt(ref.Peer.ChannelID, 10) + ":" + strconv.FormatInt(ref.MsgID, 10)
}

func blockKey(ref tgclient.DocumentRef, blockStart int64) string {
	return docKey(ref) + ":" + strconv.FormatInt(blockStart, 10)
}
