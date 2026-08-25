package mountcache

import (
	"context"
	"errors"
	"sync"
)

var errClosed = errors.New("mount cache: coalescer is closed")

// ErrNilContext reports a missing caller context.
var ErrNilContext = errors.New("mount cache: context is required")

// Coalescer shares one load per key while keeping each caller's cancellation
// independent. A successful result is cached only while its load is still the
// current, owned load for that key.
type Coalescer[K comparable, V any] struct {
	mu       sync.Mutex
	loads    map[K]*coalescedLoad[V]
	slots    chan struct{}
	closed   bool
	closeErr error
	closedCh chan struct{}
}

type coalescedLoad[V any] struct {
	ctx       context.Context
	cancel    context.CancelFunc
	done      chan struct{}
	waiters   int
	value     V
	err       error
	completed bool
	abandoned bool
	ownsSlot  bool
}

// NewCoalescer constructs a load group. A non-positive concurrency limit
// leaves distinct-key concurrency unbounded.
func NewCoalescer[K comparable, V any](maxConcurrent int) *Coalescer[K, V] {
	var slots chan struct{}
	if maxConcurrent > 0 {
		slots = make(chan struct{}, maxConcurrent)
	}
	return &Coalescer[K, V]{
		loads:    make(map[K]*coalescedLoad[V]),
		slots:    slots,
		closedCh: make(chan struct{}),
	}
}

// Load returns a cached value, joins an in-flight load, or starts loader.
// lookup and store run while the load-group lock is held so Invalidate can
// atomically detach a load and evict the value it might otherwise publish.
func (loads *Coalescer[K, V]) Load(
	ctx context.Context,
	baseContext context.Context,
	key K,
	lookup func() (V, bool),
	loader func(context.Context) (V, error),
	store func(V),
) (V, error) {
	var zero V
	if ctx == nil {
		return zero, ErrNilContext
	}
	if err := ctx.Err(); err != nil {
		return zero, err
	}
	if baseContext == nil {
		baseContext = context.Background()
	}

	loads.mu.Lock()
	value, load, cached, err := loads.lookupOrJoinLocked(key, lookup)
	if err != nil || cached || load != nil {
		loads.mu.Unlock()
		switch {
		case err != nil:
			return zero, err
		case cached:
			return value, nil
		default:
			return loads.wait(ctx, key, load)
		}
	}
	if loads.slots == nil {
		load = loads.startLocked(baseContext, key, false)
		loads.mu.Unlock()
		go loads.run(key, load, loader, store)
		return loads.wait(ctx, key, load)
	}
	loads.mu.Unlock()

	select {
	case loads.slots <- struct{}{}:
	case <-ctx.Done():
		return zero, ctx.Err()
	case <-loads.closedCh:
		return zero, loads.closedError()
	}
	if err := ctx.Err(); err != nil {
		loads.releaseSlot()
		return zero, err
	}

	loads.mu.Lock()
	value, load, cached, err = loads.lookupOrJoinLocked(key, lookup)
	if err != nil || cached || load != nil {
		loads.mu.Unlock()
		loads.releaseSlot()
		switch {
		case err != nil:
			return zero, err
		case cached:
			return value, nil
		default:
			return loads.wait(ctx, key, load)
		}
	}
	load = loads.startLocked(baseContext, key, true)
	loads.mu.Unlock()
	go loads.run(key, load, loader, store)
	return loads.wait(ctx, key, load)
}

func (loads *Coalescer[K, V]) lookupOrJoinLocked(
	key K,
	lookup func() (V, bool),
) (V, *coalescedLoad[V], bool, error) {
	var zero V
	if loads.closed {
		return zero, nil, false, loads.closeErr
	}
	if lookup != nil {
		if value, found := lookup(); found {
			return value, nil, true, nil
		}
	}
	if load := loads.loads[key]; load != nil {
		load.waiters++
		return zero, load, false, nil
	}
	return zero, nil, false, nil
}

func (loads *Coalescer[K, V]) startLocked(
	baseContext context.Context,
	key K,
	ownsSlot bool,
) *coalescedLoad[V] {
	loadContext, cancel := context.WithCancel(baseContext)
	load := &coalescedLoad[V]{
		ctx:      loadContext,
		cancel:   cancel,
		done:     make(chan struct{}),
		waiters:  1,
		ownsSlot: ownsSlot,
	}
	loads.loads[key] = load
	return load
}

func (loads *Coalescer[K, V]) run(
	key K,
	load *coalescedLoad[V],
	loader func(context.Context) (V, error),
	store func(V),
) {
	value, err := loader(load.ctx)

	loads.mu.Lock()
	if !load.completed {
		if current := loads.loads[key]; current == load {
			delete(loads.loads, key)
			if err == nil && load.ctx.Err() == nil && !load.abandoned && !loads.closed && store != nil {
				store(value)
			}
		}
		loads.completeLocked(load, value, err)
	}
	loads.mu.Unlock()

	load.cancel()
	if load.ownsSlot {
		loads.releaseSlot()
	}
}

func (loads *Coalescer[K, V]) wait(
	ctx context.Context,
	key K,
	load *coalescedLoad[V],
) (V, error) {
	var zero V
	select {
	case <-load.done:
		return load.value, load.err
	case <-ctx.Done():
		loads.releaseWaiter(key, load)
		return zero, ctx.Err()
	}
}

func (loads *Coalescer[K, V]) releaseWaiter(key K, load *coalescedLoad[V]) {
	loads.mu.Lock()
	if current := loads.loads[key]; current == load && !load.completed {
		load.waiters--
		if load.waiters == 0 {
			load.abandoned = true
			delete(loads.loads, key)
			load.cancel()
		}
	}
	loads.mu.Unlock()
}

// Invalidate atomically walks keys and any dependent keys returned by evict.
// Each current load is detached, canceled, and completed with terminalErr
// before another load for that key can begin.
func (loads *Coalescer[K, V]) Invalidate(
	keys []K,
	terminalErr error,
	evict func(K) []K,
) {
	if len(keys) == 0 {
		return
	}
	if terminalErr == nil {
		terminalErr = context.Canceled
	}
	if loads != nil {
		loads.mu.Lock()
		defer loads.mu.Unlock()
	}

	queue := append([]K(nil), keys...)
	seen := make(map[K]struct{}, len(queue))
	for len(queue) > 0 {
		key := queue[0]
		queue = queue[1:]
		if _, duplicate := seen[key]; duplicate {
			continue
		}
		seen[key] = struct{}{}
		if loads != nil {
			loads.invalidateLocked(key, terminalErr)
		}
		if evict != nil {
			queue = append(queue, evict(key)...)
		}
	}
}

// Close permanently rejects new loads and cancels all active loads.
func (loads *Coalescer[K, V]) Close(terminalErr error) {
	if loads == nil {
		return
	}
	if terminalErr == nil {
		terminalErr = errClosed
	}
	loads.mu.Lock()
	if loads.closed {
		loads.mu.Unlock()
		return
	}
	loads.closed = true
	loads.closeErr = terminalErr
	close(loads.closedCh)
	for key, load := range loads.loads {
		delete(loads.loads, key)
		load.abandoned = true
		load.cancel()
		var zero V
		loads.completeLocked(load, zero, terminalErr)
	}
	loads.mu.Unlock()
}

// Waiters returns the number of callers currently sharing key.
func (loads *Coalescer[K, V]) Waiters(key K) int {
	if loads == nil {
		return 0
	}
	loads.mu.Lock()
	defer loads.mu.Unlock()
	if load := loads.loads[key]; load != nil {
		return load.waiters
	}
	return 0
}

// Len returns the number of current distinct-key loads.
func (loads *Coalescer[K, V]) Len() int {
	if loads == nil {
		return 0
	}
	loads.mu.Lock()
	defer loads.mu.Unlock()
	return len(loads.loads)
}

func (loads *Coalescer[K, V]) completeLocked(load *coalescedLoad[V], value V, err error) {
	if load.completed {
		return
	}
	load.value = value
	load.err = err
	load.completed = true
	close(load.done)
}

func (loads *Coalescer[K, V]) invalidateLocked(key K, terminalErr error) {
	load := loads.loads[key]
	if load == nil {
		return
	}
	delete(loads.loads, key)
	load.abandoned = true
	load.cancel()
	var zero V
	loads.completeLocked(load, zero, terminalErr)
}

func (loads *Coalescer[K, V]) releaseSlot() {
	<-loads.slots
}

func (loads *Coalescer[K, V]) closedError() error {
	loads.mu.Lock()
	defer loads.mu.Unlock()
	if loads.closeErr != nil {
		return loads.closeErr
	}
	return errClosed
}
