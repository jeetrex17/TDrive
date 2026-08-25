package mountfs

import (
	"context"
	"errors"
	"fmt"
	"time"

	"TDrive/backend/mountcache"
)

const defaultSnapshotLoadTimeout = 30 * time.Second

var errSnapshotInvalidated = errors.New("mountfs: directory snapshot invalidated")

type snapshotCache struct {
	entries     *mountcache.LRU[string, directorySnapshot]
	loads       *mountcache.Coalescer[string, directorySnapshot]
	loadTimeout time.Duration
}

type directoryLoader func(ctx context.Context, parentID string) (directorySnapshot, error)

func newSnapshotCache(
	capacity int,
	maxEntries int,
	maxConcurrentLoads int,
	ttl time.Duration,
	now func() time.Time,
) *snapshotCache {
	return &snapshotCache{
		entries: mountcache.NewLRU[string, directorySnapshot](
			mountcache.LRUConfig[directorySnapshot]{
				Capacity:  capacity,
				MaxWeight: maxEntries,
				TTL:       ttl,
				Now:       now,
				Weight: func(snapshot directorySnapshot) int {
					return len(snapshot.entries)
				},
			},
		),
		loads:       mountcache.NewCoalescer[string, directorySnapshot](maxConcurrentLoads),
		loadTimeout: defaultSnapshotLoadTimeout,
	}
}

func (cache *snapshotCache) getOrLoad(
	ctx context.Context,
	parentID string,
	loader directoryLoader,
) (directorySnapshot, error) {
	for {
		snapshot, err := cache.getOrLoadOnce(ctx, parentID, loader)
		if !errors.Is(err, errSnapshotInvalidated) {
			return snapshot, err
		}
		if err := ctx.Err(); err != nil {
			return directorySnapshot{}, err
		}
	}
}

func (cache *snapshotCache) getOrLoadOnce(
	ctx context.Context,
	parentID string,
	loader directoryLoader,
) (directorySnapshot, error) {
	return cache.loads.Load(
		ctx,
		context.WithoutCancel(ctx),
		parentID,
		func() (directorySnapshot, bool) {
			return cache.entries.Get(parentID)
		},
		func(loadContext context.Context) (directorySnapshot, error) {
			timedContext, cancel := context.WithTimeout(loadContext, cache.loadTimeout)
			defer cancel()
			snapshot, err := loader(timedContext, parentID)
			if timedContext.Err() == context.DeadlineExceeded {
				return directorySnapshot{}, fmt.Errorf(
					"%w: directory snapshot load timed out after %s",
					ErrContentUnavailable,
					cache.loadTimeout,
				)
			}
			return snapshot, err
		},
		func(snapshot directorySnapshot) {
			cache.entries.Put(parentID, snapshot)
		},
	)
}

// invalidateDirectories evicts only the named directory snapshots. Active
// loads are detached and cancelled so callers retry against the new source
// generation instead of observing or caching a stale completion.
func (cache *snapshotCache) invalidateDirectories(parentIDs ...string) {
	if cache == nil || len(parentIDs) == 0 {
		return
	}
	cache.loads.Invalidate(parentIDs, errSnapshotInvalidated, func(parentID string) []string {
		cache.entries.Delete(parentID)
		return nil
	})
}

// invalidateSubtree evicts the root snapshot and descendants discoverable
// from immutable snapshots currently retained by the cache. Uncached nodes do
// not require invalidation and unrelated snapshots remain available.
func (cache *snapshotCache) invalidateSubtree(rootID string) {
	if cache == nil {
		return
	}
	cache.loads.Invalidate([]string{rootID}, errSnapshotInvalidated, func(parentID string) []string {
		snapshot, found := cache.entries.Delete(parentID)
		if !found {
			return nil
		}
		children := make([]string, 0)
		for _, child := range snapshot.entries {
			if child.source.Kind == KindDirectory {
				children = append(children, child.source.ID)
			}
		}
		return children
	})
}

func (cache *snapshotCache) len() int {
	if cache == nil {
		return 0
	}
	return cache.entries.Len()
}

func (cache *snapshotCache) entryLen() int {
	if cache == nil {
		return 0
	}
	return cache.entries.Weight()
}
