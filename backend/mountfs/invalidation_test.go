package mountfs

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"
)

func TestInvalidateDirectoriesRefreshesSourceAndDestinationOnly(t *testing.T) {
	t.Parallel()

	source := newMutableDirectorySource(map[string][]SourceEntry{
		RootID: {
			{ID: "d:source", ParentID: RootID, Name: "source", Kind: KindDirectory},
			{ID: "d:destination", ParentID: RootID, Name: "destination", Kind: KindDirectory},
			{ID: "d:unrelated", ParentID: RootID, Name: "unrelated", Kind: KindDirectory},
		},
		"d:source": {
			{ID: "f:moved", ParentID: "d:source", Name: "moved.txt", Kind: KindFile},
		},
		"d:destination": nil,
		"d:unrelated": {
			{ID: "f:kept", ParentID: "d:unrelated", Name: "kept.txt", Kind: KindFile},
		},
	})
	fs := mustNewFSWithOptions(t, 42, source, &fakeContentOpener{}, Options{
		SnapshotTTL:          time.Hour,
		MaxCachedDirectories: 8,
	})

	for _, path := range []string{"/source", "/destination", "/unrelated"} {
		if _, err := fs.ReadDir(context.Background(), path); err != nil {
			t.Fatalf("warm ReadDir(%q) error = %v", path, err)
		}
	}
	source.replace("d:source", nil)
	source.replace("d:destination", []SourceEntry{
		{ID: "f:moved", ParentID: "d:destination", Name: "moved.txt", Kind: KindFile},
	})

	fs.InvalidateDirectories("d:source", "d:destination", "d:source")

	sourceEntries, err := fs.ReadDir(context.Background(), "/source")
	if err != nil {
		t.Fatalf("refreshed source ReadDir() error = %v", err)
	}
	if len(sourceEntries) != 0 {
		t.Fatalf("refreshed source entries = %#v, want empty", sourceEntries)
	}
	destinationEntries, err := fs.ReadDir(context.Background(), "/destination")
	if err != nil {
		t.Fatalf("refreshed destination ReadDir() error = %v", err)
	}
	if len(destinationEntries) != 1 || destinationEntries[0].ID != "f:moved" {
		t.Fatalf("refreshed destination entries = %#v, want moved file", destinationEntries)
	}
	if _, err := fs.ReadDir(context.Background(), "/unrelated"); err != nil {
		t.Fatalf("retained unrelated ReadDir() error = %v", err)
	}

	if got := source.callsFor("d:source"); got != 2 {
		t.Fatalf("source loads = %d, want 2", got)
	}
	if got := source.callsFor("d:destination"); got != 2 {
		t.Fatalf("destination loads = %d, want 2", got)
	}
	if got := source.callsFor("d:unrelated"); got != 1 {
		t.Fatalf("unrelated loads = %d, want retained cache", got)
	}
}

func TestInvalidateSubtreeEvictsCachedDescendantsAndRetainsUnrelated(t *testing.T) {
	t.Parallel()

	source := newMutableDirectorySource(map[string][]SourceEntry{
		RootID: {
			{ID: "d:tree", ParentID: RootID, Name: "tree", Kind: KindDirectory},
			{ID: "d:other", ParentID: RootID, Name: "other", Kind: KindDirectory},
		},
		"d:tree": {
			{ID: "d:child", ParentID: "d:tree", Name: "child", Kind: KindDirectory},
		},
		"d:child": {
			{ID: "d:grandchild", ParentID: "d:child", Name: "grandchild", Kind: KindDirectory},
		},
		"d:grandchild": nil,
		"d:other":      nil,
	})
	fs := mustNewFSWithOptions(t, 42, source, &fakeContentOpener{}, Options{
		SnapshotTTL:          time.Hour,
		MaxCachedDirectories: 16,
	})

	if _, err := fs.ReadDir(context.Background(), "/tree/child/grandchild"); err != nil {
		t.Fatalf("warm descendant ReadDir() error = %v", err)
	}
	if _, err := fs.ReadDir(context.Background(), "/other"); err != nil {
		t.Fatalf("warm unrelated ReadDir() error = %v", err)
	}

	fs.InvalidateSubtree("d:tree")

	if _, err := fs.ReadDir(context.Background(), "/tree/child/grandchild"); err != nil {
		t.Fatalf("refreshed descendant ReadDir() error = %v", err)
	}
	if _, err := fs.ReadDir(context.Background(), "/other"); err != nil {
		t.Fatalf("retained unrelated ReadDir() error = %v", err)
	}

	for _, parentID := range []string{"d:tree", "d:child", "d:grandchild"} {
		if got := source.callsFor(parentID); got != 2 {
			t.Errorf("subtree loads for %q = %d, want 2", parentID, got)
		}
	}
	if got := source.callsFor(RootID); got != 1 {
		t.Fatalf("root loads = %d, want retained parent cache", got)
	}
	if got := source.callsFor("d:other"); got != 1 {
		t.Fatalf("unrelated loads = %d, want retained cache", got)
	}
}

func TestInvalidatedInFlightSnapshotRetriesAndCannotReinsertStaleData(t *testing.T) {
	t.Parallel()

	cache := newSnapshotCache(4, 10, 2, time.Hour, time.Now)
	staleStarted := make(chan struct{})
	staleCancelled := make(chan struct{})
	releaseStale := make(chan struct{})
	freshStarted := make(chan struct{})
	releaseFresh := make(chan struct{})
	var attemptsMu sync.Mutex
	attempts := 0
	loader := func(ctx context.Context, _ string) (directorySnapshot, error) {
		attemptsMu.Lock()
		attempts++
		attempt := attempts
		attemptsMu.Unlock()

		switch attempt {
		case 1:
			close(staleStarted)
			<-ctx.Done()
			close(staleCancelled)
			<-releaseStale
			// Deliberately ignore cancellation to model a slow source that returns
			// an already-captured projection snapshot.
			return snapshotWithName("stale"), nil
		case 2:
			close(freshStarted)
			<-releaseFresh
			return snapshotWithName("fresh"), nil
		default:
			return directorySnapshot{}, fmt.Errorf("unexpected loader attempt %d", attempt)
		}
	}

	result := make(chan struct {
		snapshot directorySnapshot
		err      error
	}, 1)
	go func() {
		snapshot, err := cache.getOrLoad(context.Background(), "d:shared", loader)
		result <- struct {
			snapshot directorySnapshot
			err      error
		}{snapshot: snapshot, err: err}
	}()
	select {
	case <-staleStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("stale load did not start")
	}

	cache.invalidateDirectories("d:shared")
	select {
	case <-staleCancelled:
	case <-time.After(2 * time.Second):
		t.Fatal("invalidation did not cancel the stale load")
	}
	select {
	case <-freshStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("invalidated waiter did not retry until the stale loader returned")
	}
	close(releaseStale)
	close(releaseFresh)

	loaded := <-result
	if loaded.err != nil {
		t.Fatalf("getOrLoad() error = %v", loaded.err)
	}
	if got := loaded.snapshot.entries[0].entry.Name; got != "fresh" {
		t.Fatalf("retried snapshot = %q, want fresh", got)
	}
	cached, err := cache.getOrLoad(context.Background(), "d:shared", loader)
	if err != nil {
		t.Fatalf("cached getOrLoad() error = %v", err)
	}
	if got := cached.entries[0].entry.Name; got != "fresh" {
		t.Fatalf("cached snapshot = %q, stale completion was reinserted", got)
	}
	attemptsMu.Lock()
	defer attemptsMu.Unlock()
	if attempts != 2 {
		t.Fatalf("loader attempts = %d, want 2", attempts)
	}
}

func TestInvalidateSubtreeInvalidatesInFlightCachedChild(t *testing.T) {
	t.Parallel()

	cache := newSnapshotCache(8, 20, 2, time.Hour, time.Now)
	cache.entries.Put("d:tree", directorySnapshot{entries: []snapshotEntry{
		{source: SourceEntry{ID: "d:child", ParentID: "d:tree", Kind: KindDirectory}},
	}})

	childStarted := make(chan struct{})
	childCancelled := make(chan struct{})
	var childStartedOnce sync.Once
	var childCancelledOnce sync.Once
	loader := func(ctx context.Context, _ string) (directorySnapshot, error) {
		childStartedOnce.Do(func() { close(childStarted) })
		<-ctx.Done()
		childCancelledOnce.Do(func() { close(childCancelled) })
		return directorySnapshot{}, ctx.Err()
	}
	requestContext, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan error, 1)
	go func() {
		_, err := cache.getOrLoad(requestContext, "d:child", loader)
		result <- err
	}()
	select {
	case <-childStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("child load did not start")
	}

	cache.invalidateSubtree("d:tree")
	select {
	case <-childCancelled:
	case <-time.After(2 * time.Second):
		t.Fatal("subtree invalidation did not cancel the child load")
	}
	cancel()
	select {
	case <-result:
	case <-time.After(2 * time.Second):
		t.Fatal("child waiter did not exit after cancellation")
	}
}

func TestInvalidationIsSafeWithDisabledCacheAndNilFilesystem(t *testing.T) {
	t.Parallel()

	var nilFS *FS
	nilFS.InvalidateDirectories(RootID, "d:any")
	nilFS.InvalidateSubtree(RootID)

	fs := mustNewFSWithOptions(t, 42, newMutableDirectorySource(nil), &fakeContentOpener{}, Options{
		DisableSnapshotCache: true,
	})
	fs.InvalidateDirectories(RootID, "d:any")
	fs.InvalidateSubtree(RootID)
}

type mutableDirectorySource struct {
	mu      sync.Mutex
	entries map[string][]SourceEntry
	calls   map[string]int
}

func newMutableDirectorySource(entries map[string][]SourceEntry) *mutableDirectorySource {
	return &mutableDirectorySource{
		entries: entries,
		calls:   make(map[string]int),
	}
}

func (source *mutableDirectorySource) ListDirectory(_ context.Context, _ int64, parentID string) ([]SourceEntry, error) {
	source.mu.Lock()
	defer source.mu.Unlock()
	source.calls[parentID]++
	return append([]SourceEntry(nil), source.entries[parentID]...), nil
}

func (source *mutableDirectorySource) replace(parentID string, entries []SourceEntry) {
	source.mu.Lock()
	defer source.mu.Unlock()
	source.entries[parentID] = append([]SourceEntry(nil), entries...)
}

func (source *mutableDirectorySource) callsFor(parentID string) int {
	source.mu.Lock()
	defer source.mu.Unlock()
	return source.calls[parentID]
}
