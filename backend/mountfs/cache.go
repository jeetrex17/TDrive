package mountfs

import (
	"container/list"
	"context"
	"fmt"
	"sync"
	"time"
)

const defaultSnapshotLoadTimeout = 30 * time.Second

type snapshotCache struct {
	mu          sync.Mutex
	entries     map[string]*list.Element
	recent      *list.List
	loads       map[string]*snapshotLoad
	loadSlots   chan struct{}
	capacity    int
	maxEntries  int
	entryCount  int
	ttl         time.Duration
	now         func() time.Time
	loadTimeout time.Duration
}

type cachedSnapshot struct {
	parentID  string
	snapshot  directorySnapshot
	expiresAt time.Time
}

type snapshotLoad struct {
	done     chan struct{}
	cancel   context.CancelFunc
	waiters  int
	snapshot directorySnapshot
	err      error
}

type directoryLoader func(ctx context.Context, parentID string) (directorySnapshot, error)

func newSnapshotCache(capacity, maxEntries, maxConcurrentLoads int, ttl time.Duration, now func() time.Time) *snapshotCache {
	return &snapshotCache{
		entries:     make(map[string]*list.Element, capacity),
		recent:      list.New(),
		loads:       make(map[string]*snapshotLoad),
		loadSlots:   make(chan struct{}, maxConcurrentLoads),
		capacity:    capacity,
		maxEntries:  maxEntries,
		ttl:         ttl,
		now:         now,
		loadTimeout: defaultSnapshotLoadTimeout,
	}
}

func (cache *snapshotCache) getOrLoad(
	ctx context.Context,
	parentID string,
	loader directoryLoader,
) (directorySnapshot, error) {
	if err := ctx.Err(); err != nil {
		return directorySnapshot{}, err
	}
	cache.mu.Lock()
	if snapshot, found := cache.lookupLocked(parentID); found {
		cache.mu.Unlock()
		return snapshot, nil
	}
	if load, found := cache.loads[parentID]; found {
		load.waiters++
		cache.mu.Unlock()
		return cache.waitForSnapshotLoad(ctx, parentID, load)
	}
	cache.mu.Unlock()

	select {
	case cache.loadSlots <- struct{}{}:
	case <-ctx.Done():
		return directorySnapshot{}, ctx.Err()
	}
	if err := ctx.Err(); err != nil {
		cache.releaseLoadSlot()
		return directorySnapshot{}, err
	}

	// State may have changed while this caller waited for global admission.
	cache.mu.Lock()
	if snapshot, found := cache.lookupLocked(parentID); found {
		cache.mu.Unlock()
		cache.releaseLoadSlot()
		return snapshot, nil
	}
	if load, found := cache.loads[parentID]; found {
		load.waiters++
		cache.mu.Unlock()
		cache.releaseLoadSlot()
		return cache.waitForSnapshotLoad(ctx, parentID, load)
	}
	loadContext, cancelLoad := context.WithCancel(context.WithoutCancel(ctx))
	load := &snapshotLoad{
		done:    make(chan struct{}),
		cancel:  cancelLoad,
		waiters: 1,
	}
	cache.loads[parentID] = load
	go cache.runLoad(loadContext, parentID, load, loader)
	cache.mu.Unlock()
	return cache.waitForSnapshotLoad(ctx, parentID, load)
}

func (cache *snapshotCache) waitForSnapshotLoad(
	ctx context.Context,
	parentID string,
	load *snapshotLoad,
) (directorySnapshot, error) {
	select {
	case <-ctx.Done():
		cache.removeWaiter(parentID, load)
		return directorySnapshot{}, ctx.Err()
	case <-load.done:
		return load.snapshot, load.err
	}
}

func (cache *snapshotCache) removeWaiter(parentID string, load *snapshotLoad) {
	cache.mu.Lock()
	defer cache.mu.Unlock()

	if cache.loads[parentID] != load {
		return
	}
	load.waiters--
	if load.waiters > 0 {
		return
	}
	delete(cache.loads, parentID)
	load.cancel()
}

func (cache *snapshotCache) runLoad(
	baseContext context.Context,
	parentID string,
	load *snapshotLoad,
	loader directoryLoader,
) {
	defer load.cancel()
	loadContext, cancel := context.WithTimeout(baseContext, cache.loadTimeout)
	defer cancel()
	snapshot, err := loader(loadContext, parentID)
	if loadContext.Err() == context.DeadlineExceeded {
		snapshot = directorySnapshot{}
		err = fmt.Errorf("%w: directory snapshot load timed out after %s", ErrContentUnavailable, cache.loadTimeout)
	}

	cache.mu.Lock()
	if cache.loads[parentID] == load {
		if err == nil {
			cache.insertLocked(parentID, snapshot)
		}
		delete(cache.loads, parentID)
	}
	load.snapshot = snapshot
	load.err = err
	cache.releaseLoadSlot()
	close(load.done)
	cache.mu.Unlock()
}

func (cache *snapshotCache) releaseLoadSlot() {
	<-cache.loadSlots
}

func (cache *snapshotCache) lookupLocked(parentID string) (directorySnapshot, bool) {
	element, found := cache.entries[parentID]
	if !found {
		return directorySnapshot{}, false
	}
	record := element.Value.(cachedSnapshot)
	if !cache.now().Before(record.expiresAt) {
		cache.removeLocked(element)
		return directorySnapshot{}, false
	}
	cache.recent.MoveToFront(element)
	return record.snapshot, true
}

func (cache *snapshotCache) insertLocked(parentID string, snapshot directorySnapshot) {
	weight := len(snapshot.entries)
	if weight > cache.maxEntries {
		if element, found := cache.entries[parentID]; found {
			cache.removeLocked(element)
		}
		return
	}
	expiresAt := cache.now().Add(cache.ttl)
	if element, found := cache.entries[parentID]; found {
		cache.removeLocked(element)
	}

	element := cache.recent.PushFront(cachedSnapshot{
		parentID:  parentID,
		snapshot:  snapshot,
		expiresAt: expiresAt,
	})
	cache.entries[parentID] = element
	cache.entryCount += weight
	for cache.recent.Len() > cache.capacity || cache.entryCount > cache.maxEntries {
		cache.removeLocked(cache.recent.Back())
	}
}

func (cache *snapshotCache) removeLocked(element *list.Element) {
	if element == nil {
		return
	}
	record := element.Value.(cachedSnapshot)
	delete(cache.entries, record.parentID)
	cache.entryCount -= len(record.snapshot.entries)
	cache.recent.Remove(element)
}

func (cache *snapshotCache) len() int {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	return cache.recent.Len()
}

func (cache *snapshotCache) entryLen() int {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	return cache.entryCount
}
