package mountfs

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"
)

func TestSnapshotCacheReusesDirectoryAcrossStatAndReadDir(t *testing.T) {
	t.Parallel()

	source := newCountingDirectorySource(map[string][]SourceEntry{
		RootID: {
			{ID: "f:readme", ParentID: RootID, Name: "README.md", Kind: KindFile, Size: 10},
		},
	})
	fs := mustNewFSWithOptions(t, 42, source, &fakeContentOpener{}, Options{
		SnapshotTTL:          time.Minute,
		MaxCachedDirectories: 4,
	})

	if _, err := fs.Stat(context.Background(), "/readme.md"); err != nil {
		t.Fatalf("Stat() error = %v", err)
	}
	if _, err := fs.ReadDir(context.Background(), "/"); err != nil {
		t.Fatalf("ReadDir() error = %v", err)
	}
	if _, err := fs.Stat(context.Background(), "/README.MD"); err != nil {
		t.Fatalf("second Stat() error = %v", err)
	}

	if got := source.callsFor(RootID); got != 1 {
		t.Fatalf("ListDirectory(root) calls = %d, want 1 cached load", got)
	}
}

func TestSnapshotCacheRefreshesExpiredDirectory(t *testing.T) {
	t.Parallel()

	clock := newFakeClock(time.Date(2026, time.August, 19, 12, 0, 0, 0, time.UTC))
	source := newCountingDirectorySource(map[string][]SourceEntry{
		RootID: {
			{ID: "f:one", ParentID: RootID, Name: "one.txt", Kind: KindFile},
		},
	})
	fs := mustNewFSWithClock(t, 42, source, &fakeContentOpener{}, Options{
		SnapshotTTL:          time.Second,
		MaxCachedDirectories: 4,
	}, clock.Now)

	if _, err := fs.Stat(context.Background(), "/one.txt"); err != nil {
		t.Fatalf("first Stat() error = %v", err)
	}
	clock.Advance(999 * time.Millisecond)
	if _, err := fs.Stat(context.Background(), "/one.txt"); err != nil {
		t.Fatalf("cached Stat() error = %v", err)
	}
	if got := source.callsFor(RootID); got != 1 {
		t.Fatalf("calls before expiration = %d, want 1", got)
	}

	clock.Advance(time.Millisecond)
	if _, err := fs.Stat(context.Background(), "/one.txt"); err != nil {
		t.Fatalf("expired Stat() error = %v", err)
	}
	if got := source.callsFor(RootID); got != 2 {
		t.Fatalf("calls after expiration = %d, want 2", got)
	}
}

func TestSnapshotCacheEnforcesLRUBound(t *testing.T) {
	t.Parallel()

	source := newCountingDirectorySource(map[string][]SourceEntry{
		RootID: {
			{ID: "d:a", ParentID: RootID, Name: "a", Kind: KindDirectory},
			{ID: "d:b", ParentID: RootID, Name: "b", Kind: KindDirectory},
		},
		"d:a": nil,
		"d:b": nil,
	})
	fs := mustNewFSWithOptions(t, 42, source, &fakeContentOpener{}, Options{
		SnapshotTTL:          time.Minute,
		MaxCachedDirectories: 2,
	})

	if _, err := fs.ReadDir(context.Background(), "/a"); err != nil {
		t.Fatalf("ReadDir(a) error = %v", err)
	}
	if _, err := fs.ReadDir(context.Background(), "/b"); err != nil {
		t.Fatalf("ReadDir(b) error = %v", err)
	}
	if _, err := fs.ReadDir(context.Background(), "/a"); err != nil {
		t.Fatalf("second ReadDir(a) error = %v", err)
	}

	if got := source.callsFor(RootID); got != 1 {
		t.Fatalf("root calls = %d, want root retained as MRU", got)
	}
	if got := source.callsFor("d:a"); got != 2 {
		t.Fatalf("directory a calls = %d, want 2 after eviction", got)
	}
	if got := source.callsFor("d:b"); got != 1 {
		t.Fatalf("directory b calls = %d, want 1", got)
	}
	if got := fs.cacheLen(); got > 2 {
		t.Fatalf("cache size = %d, configured maximum is 2", got)
	}
}

func TestSnapshotCacheEnforcesTotalEntryBudget(t *testing.T) {
	t.Parallel()

	clock := newFakeClock(time.Date(2026, time.August, 19, 12, 0, 0, 0, time.UTC))
	cache := newSnapshotCache(4, 3, 2, time.Minute, clock.Now)
	cache.entries.Put("first", snapshotWithEntries(2))
	cache.entries.Put("second", snapshotWithEntries(2))

	if got := cache.len(); got != 1 {
		t.Fatalf("cache size = %d, want 1 after weighted eviction", got)
	}
	if got := cache.entryLen(); got != 2 {
		t.Fatalf("cached entries = %d, want 2", got)
	}
}

func TestSnapshotCacheDoesNotRetainOversizedDirectory(t *testing.T) {
	t.Parallel()

	clock := newFakeClock(time.Date(2026, time.August, 19, 12, 0, 0, 0, time.UTC))
	cache := newSnapshotCache(4, 2, 2, time.Minute, clock.Now)
	loads := 0
	loader := func(context.Context, string) (directorySnapshot, error) {
		loads++
		return snapshotWithEntries(3), nil
	}

	for range 2 {
		snapshot, err := cache.getOrLoad(context.Background(), "large", loader)
		if err != nil {
			t.Fatalf("getOrLoad() error = %v", err)
		}
		if got := len(snapshot.entries); got != 3 {
			t.Fatalf("snapshot entries = %d, want 3", got)
		}
	}
	if loads != 2 {
		t.Fatalf("oversized snapshot loads = %d, want 2 because it is not cached", loads)
	}
	if got := cache.entryLen(); got != 0 {
		t.Fatalf("cached entries = %d, want 0", got)
	}
}

func TestSnapshotCacheCoalescesConcurrentLoads(t *testing.T) {
	t.Parallel()

	source := newCountingDirectorySource(map[string][]SourceEntry{
		RootID: {
			{ID: "f:shared", ParentID: RootID, Name: "shared.txt", Kind: KindFile},
		},
	})
	source.block = make(chan struct{})
	source.entered = make(chan struct{})
	fs := mustNewFSWithOptions(t, 42, source, &fakeContentOpener{}, Options{
		SnapshotTTL:          time.Minute,
		MaxCachedDirectories: 4,
	})

	const readers = 32
	start := make(chan struct{})
	errorsByReader := make(chan error, readers)
	for range readers {
		go func() {
			<-start
			_, err := fs.Stat(context.Background(), "/shared.txt")
			errorsByReader <- err
		}()
	}
	close(start)
	select {
	case <-source.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("directory load did not start")
	}
	close(source.block)

	for range readers {
		if err := <-errorsByReader; err != nil {
			t.Fatalf("concurrent Stat() error = %v", err)
		}
	}
	if got := source.callsFor(RootID); got != 1 {
		t.Fatalf("concurrent ListDirectory(root) calls = %d, want 1 coalesced load", got)
	}
}

func TestCancelledWaiterDoesNotPoisonSharedDirectoryLoad(t *testing.T) {
	t.Parallel()

	cache := newSnapshotCache(4, 10, 2, time.Minute, time.Now)
	loadStarted := make(chan struct{})
	inspectContext := make(chan struct{})
	contextObserved := make(chan error, 1)
	releaseLoad := make(chan struct{})
	loader := func(ctx context.Context, _ string) (directorySnapshot, error) {
		close(loadStarted)
		<-inspectContext
		contextObserved <- ctx.Err()
		select {
		case <-releaseLoad:
			return snapshotWithName("survivor"), nil
		case <-ctx.Done():
			return directorySnapshot{}, ctx.Err()
		}
	}

	cancelledContext, cancel := context.WithCancel(context.Background())
	firstResult := make(chan error, 1)
	go func() {
		_, err := cache.getOrLoad(cancelledContext, RootID, loader)
		firstResult <- err
	}()
	select {
	case <-loadStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("directory load did not start")
	}

	survivorWaiting := make(chan struct{})
	survivorContext := &waiterObservedContext{
		Context: context.Background(),
		waiting: survivorWaiting,
	}
	secondResult := make(chan error, 1)
	go func() {
		_, err := cache.getOrLoad(survivorContext, RootID, loader)
		secondResult <- err
	}()
	select {
	case <-survivorWaiting:
	case <-time.After(2 * time.Second):
		t.Fatal("surviving waiter did not join the shared load")
	}

	cancel()
	if err := <-firstResult; !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled waiter error = %v, want context.Canceled", err)
	}
	close(inspectContext)
	if err := <-contextObserved; err != nil {
		t.Fatalf("shared loader context = %v, want it alive for the remaining waiter", err)
	}
	close(releaseLoad)
	if err := <-secondResult; err != nil {
		t.Fatalf("surviving waiter inherited cancellation: %v", err)
	}
}

func TestSnapshotCacheCancelsAbandonedDistinctLoadsAndReusesSlots(t *testing.T) {
	t.Parallel()

	cache := newSnapshotCache(4, 10, 2, time.Minute, time.Now)
	loadStarted := make(chan string, 2)
	loadFinished := make(chan string, 2)
	loader := func(ctx context.Context, parentID string) (directorySnapshot, error) {
		if parentID == "reuse" {
			return snapshotWithName("reused"), nil
		}
		loadStarted <- parentID
		<-ctx.Done()
		loadFinished <- parentID
		return directorySnapshot{}, ctx.Err()
	}

	contexts := make(map[string]context.CancelFunc, 2)
	results := make(chan error, 2)
	for _, parentID := range []string{"first", "second"} {
		requestContext, cancel := context.WithCancel(context.Background())
		contexts[parentID] = cancel
		go func() {
			_, err := cache.getOrLoad(requestContext, parentID, loader)
			results <- err
		}()
	}
	started := make(map[string]struct{}, 2)
	for range 2 {
		select {
		case parentID := <-loadStarted:
			started[parentID] = struct{}{}
		case <-time.After(2 * time.Second):
			t.Fatal("distinct directory load did not start")
		}
	}
	if len(started) != 2 {
		t.Fatalf("started loads = %v, want both distinct keys", started)
	}

	contexts["first"]()
	contexts["second"]()
	for range 2 {
		if err := <-results; !errors.Is(err, context.Canceled) {
			t.Fatalf("abandoned waiter error = %v, want context.Canceled", err)
		}
	}
	for range 2 {
		select {
		case <-loadFinished:
		case <-time.After(2 * time.Second):
			t.Fatal("abandoned loader did not return after its final waiter cancelled")
		}
	}

	snapshot, err := cache.getOrLoad(context.Background(), "reuse", loader)
	if err != nil {
		t.Fatalf("load after abandoned-load cleanup error = %v", err)
	}
	if got := snapshot.entries[0].entry.Name; got != "reused" {
		t.Fatalf("reused-slot snapshot name = %q, want %q", got, "reused")
	}
	if got := cache.loads.Len(); got != 0 {
		t.Fatalf("abandoned-load bookkeeping leaked: loads=%d", got)
	}
}

func TestSnapshotCacheStaleCompletionCannotReplaceNewLoad(t *testing.T) {
	t.Parallel()

	cache := newSnapshotCache(4, 10, 2, time.Minute, time.Now)
	oldStarted := make(chan struct{})
	oldCancelled := make(chan struct{})
	releaseOld := make(chan struct{})
	oldReturned := make(chan struct{})
	newStarted := make(chan struct{})
	releaseNew := make(chan struct{})
	var attemptsMu sync.Mutex
	attempts := 0
	loader := func(ctx context.Context, _ string) (directorySnapshot, error) {
		attemptsMu.Lock()
		attempts++
		attempt := attempts
		attemptsMu.Unlock()
		switch attempt {
		case 1:
			close(oldStarted)
			<-ctx.Done()
			close(oldCancelled)
			<-releaseOld
			close(oldReturned)
			return snapshotWithName("stale"), nil
		case 2:
			close(newStarted)
			<-releaseNew
			return snapshotWithName("fresh"), nil
		default:
			return directorySnapshot{}, fmt.Errorf("unexpected loader attempt %d", attempt)
		}
	}

	oldContext, cancelOld := context.WithCancel(context.Background())
	oldResult := make(chan error, 1)
	go func() {
		_, err := cache.getOrLoad(oldContext, "shared", loader)
		oldResult <- err
	}()
	select {
	case <-oldStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("old load did not start")
	}
	if got := cache.loads.Waiters("shared"); got != 1 {
		t.Fatal("old load was not tracked")
	}

	cancelOld()
	if err := <-oldResult; !errors.Is(err, context.Canceled) {
		t.Fatalf("old waiter error = %v, want context.Canceled", err)
	}
	select {
	case <-oldCancelled:
	case <-time.After(2 * time.Second):
		t.Fatal("old loader context was not cancelled with its final waiter")
	}

	newResult := make(chan struct {
		snapshot directorySnapshot
		err      error
	}, 1)
	go func() {
		snapshot, err := cache.getOrLoad(context.Background(), "shared", loader)
		newResult <- struct {
			snapshot directorySnapshot
			err      error
		}{snapshot: snapshot, err: err}
	}()
	select {
	case <-newStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("replacement load did not start while the stale loader was returning")
	}
	if got := cache.loads.Waiters("shared"); got != 1 {
		t.Fatal("replacement load was not tracked independently")
	}

	close(releaseOld)
	select {
	case <-oldReturned:
	case <-time.After(2 * time.Second):
		t.Fatal("stale loader did not finish")
	}
	if got := cache.loads.Waiters("shared"); got != 1 {
		t.Fatal("stale completion removed the replacement load")
	}
	_, staleCached := cache.entries.Get("shared")
	if staleCached {
		t.Fatal("stale completion populated the snapshot cache")
	}

	close(releaseNew)
	result := <-newResult
	if result.err != nil {
		t.Fatalf("replacement load error = %v", result.err)
	}
	if got := result.snapshot.entries[0].entry.Name; got != "fresh" {
		t.Fatalf("replacement snapshot name = %q, want %q", got, "fresh")
	}
	cached, err := cache.getOrLoad(context.Background(), "shared", loader)
	if err != nil {
		t.Fatalf("cached replacement lookup error = %v", err)
	}
	if got := cached.entries[0].entry.Name; got != "fresh" {
		t.Fatalf("cached snapshot name = %q, want %q", got, "fresh")
	}
	attemptsMu.Lock()
	defer attemptsMu.Unlock()
	if attempts != 2 {
		t.Fatalf("loader attempts = %d, want 2", attempts)
	}
}

func TestSnapshotCacheBoundsDistinctConcurrentLoads(t *testing.T) {
	t.Parallel()

	const (
		maxLoads  = 3
		totalKeys = 24
	)
	cache := newSnapshotCache(32, 100, maxLoads, time.Minute, time.Now)
	entered := make(chan struct{}, totalKeys)
	release := make(chan struct{})
	var activeMu sync.Mutex
	active := 0
	maxActive := 0
	loader := func(ctx context.Context, parentID string) (directorySnapshot, error) {
		activeMu.Lock()
		active++
		if active > maxActive {
			maxActive = active
		}
		activeMu.Unlock()
		entered <- struct{}{}
		select {
		case <-release:
		case <-ctx.Done():
			return directorySnapshot{}, ctx.Err()
		}
		activeMu.Lock()
		active--
		activeMu.Unlock()
		return directorySnapshot{}, nil
	}

	results := make(chan error, totalKeys)
	for index := range totalKeys {
		go func() {
			_, err := cache.getOrLoad(context.Background(), fmt.Sprintf("d:%d", index), loader)
			results <- err
		}()
	}
	for range maxLoads {
		select {
		case <-entered:
		case <-time.After(2 * time.Second):
			t.Fatal("expected admitted directory load did not start")
		}
	}
	select {
	case <-entered:
		t.Fatalf("more than %d distinct loads entered concurrently", maxLoads)
	case <-time.After(25 * time.Millisecond):
	}
	close(release)

	for range totalKeys {
		if err := <-results; err != nil {
			t.Fatalf("getOrLoad() error = %v", err)
		}
	}
	activeMu.Lock()
	observedMax := maxActive
	activeMu.Unlock()
	if observedMax > maxLoads {
		t.Fatalf("maximum concurrent loads = %d, configured bound is %d", observedMax, maxLoads)
	}
	if got := cache.loads.Len(); got != 0 {
		t.Fatalf("load bookkeeping leaked: loads=%d", got)
	}
}

func TestSnapshotCacheTimeoutAndAdmissionCancellationCleanUp(t *testing.T) {
	t.Parallel()

	cache := newSnapshotCache(4, 10, 1, time.Minute, time.Now)
	cache.loadTimeout = 50 * time.Millisecond
	slowStarted := make(chan struct{})
	var slowOnce sync.Once
	var callsMu sync.Mutex
	calls := make(map[string]int)
	loader := func(ctx context.Context, parentID string) (directorySnapshot, error) {
		callsMu.Lock()
		calls[parentID]++
		callsMu.Unlock()
		if parentID == "slow" {
			slowOnce.Do(func() { close(slowStarted) })
			<-ctx.Done()
			return directorySnapshot{}, ctx.Err()
		}
		return directorySnapshot{}, nil
	}

	slowResult := make(chan error, 1)
	go func() {
		_, err := cache.getOrLoad(context.Background(), "slow", loader)
		slowResult <- err
	}()
	select {
	case <-slowStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("slow load did not start")
	}

	waitContext, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if _, err := cache.getOrLoad(waitContext, "waiting", loader); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("admission waiter error = %v, want its own context deadline", err)
	}
	if err := <-slowResult; !errors.Is(err, ErrContentUnavailable) {
		t.Fatalf("internal load timeout error = %v, want ErrContentUnavailable", err)
	} else if errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("internal load timeout leaked context.DeadlineExceeded: %v", err)
	}

	if _, err := cache.getOrLoad(context.Background(), "waiting", loader); err != nil {
		t.Fatalf("load after admission cleanup error = %v", err)
	}
	callsMu.Lock()
	waitingCalls := calls["waiting"]
	callsMu.Unlock()
	if waitingCalls != 1 {
		t.Fatalf("waiting loader calls = %d, want only the post-timeout admitted call", waitingCalls)
	}
	if got := cache.loads.Len(); got != 0 {
		t.Fatalf("timeout bookkeeping leaked: loads=%d", got)
	}
}

func TestSnapshotCacheCanBeDisabled(t *testing.T) {
	t.Parallel()

	source := newCountingDirectorySource(map[string][]SourceEntry{RootID: nil})
	fs := mustNewFSWithOptions(t, 42, source, &fakeContentOpener{}, Options{DisableSnapshotCache: true})

	for range 2 {
		if _, err := fs.ReadDir(context.Background(), "/"); err != nil {
			t.Fatalf("ReadDir() error = %v", err)
		}
	}
	if got := source.callsFor(RootID); got != 2 {
		t.Fatalf("ListDirectory(root) calls = %d, want 2 with cache disabled", got)
	}
}

func TestNewWithOptionsRejectsInvalidCacheLimits(t *testing.T) {
	t.Parallel()

	source := newCountingDirectorySource(nil)
	opener := &fakeContentOpener{}
	for name, options := range map[string]Options{
		"negative TTL":              {SnapshotTTL: -time.Second},
		"negative capacity":         {MaxCachedDirectories: -1},
		"negative entry budget":     {MaxCachedEntries: -1},
		"negative concurrent loads": {MaxConcurrentSnapshotLoads: -1},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			_, err := NewWithOptions(42, source, opener, options)
			if !errors.Is(err, ErrInvalidConfiguration) {
				t.Fatalf("NewWithOptions() error = %v, want ErrInvalidConfiguration", err)
			}
		})
	}
}

func snapshotWithEntries(count int) directorySnapshot {
	return directorySnapshot{entries: make([]snapshotEntry, count)}
}

func snapshotWithName(name string) directorySnapshot {
	return directorySnapshot{entries: []snapshotEntry{{entry: Entry{Name: name}}}}
}

func BenchmarkStatAndReadDir1000Entries(b *testing.B) {
	children := make([]SourceEntry, 1000)
	for index := range children {
		children[index] = SourceEntry{
			ID:       fmt.Sprintf("f:%04d", index),
			ParentID: RootID,
			Name:     fmt.Sprintf("document-%04d.txt", index),
			Kind:     KindFile,
			Size:     int64(index),
		}
	}
	source := newCountingDirectorySource(map[string][]SourceEntry{RootID: children})
	fs, err := NewWithOptions(42, source, &fakeContentOpener{}, Options{
		SnapshotTTL:          time.Hour,
		MaxCachedDirectories: 4,
	})
	if err != nil {
		b.Fatalf("NewWithOptions() error = %v", err)
	}
	if _, err := fs.ReadDir(context.Background(), "/"); err != nil {
		b.Fatalf("warm ReadDir() error = %v", err)
	}

	b.Run("Stat", func(b *testing.B) {
		b.ReportAllocs()
		for range b.N {
			if _, err := fs.Stat(context.Background(), "/document-0999.txt"); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("ReadDir", func(b *testing.B) {
		b.ReportAllocs()
		for range b.N {
			if _, err := fs.ReadDir(context.Background(), "/"); err != nil {
				b.Fatal(err)
			}
		}
	})
}

func mustNewFSWithOptions(t *testing.T, channelID int64, source DirectorySource, opener ContentOpener, options Options) *FS {
	t.Helper()

	fs, err := NewWithOptions(channelID, source, opener, options)
	if err != nil {
		t.Fatalf("NewWithOptions() error = %v", err)
	}
	return fs
}

func mustNewFSWithClock(
	t *testing.T,
	channelID int64,
	source DirectorySource,
	opener ContentOpener,
	options Options,
	now func() time.Time,
) *FS {
	t.Helper()

	fs, err := newFS(channelID, source, opener, options, now)
	if err != nil {
		t.Fatalf("newFS() error = %v", err)
	}
	return fs
}

type countingDirectorySource struct {
	mu      sync.Mutex
	entries map[string][]SourceEntry
	calls   map[string]int
	block   chan struct{}
	entered chan struct{}
	once    sync.Once
}

func newCountingDirectorySource(entries map[string][]SourceEntry) *countingDirectorySource {
	return &countingDirectorySource{
		entries: entries,
		calls:   make(map[string]int),
	}
}

func (source *countingDirectorySource) ListDirectory(ctx context.Context, channelID int64, parentID string) ([]SourceEntry, error) {
	source.mu.Lock()
	source.calls[parentID]++
	entries := append([]SourceEntry(nil), source.entries[parentID]...)
	block := source.block
	entered := source.entered
	source.mu.Unlock()

	if entered != nil {
		source.once.Do(func() { close(entered) })
	}
	if block != nil {
		select {
		case <-block:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return entries, nil
}

func (source *countingDirectorySource) callsFor(parentID string) int {
	source.mu.Lock()
	defer source.mu.Unlock()
	return source.calls[parentID]
}

type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

type waiterObservedContext struct {
	context.Context
	once    sync.Once
	waiting chan struct{}
}

func (ctx *waiterObservedContext) Done() <-chan struct{} {
	ctx.once.Do(func() { close(ctx.waiting) })
	return ctx.Context.Done()
}

func newFakeClock(now time.Time) *fakeClock {
	return &fakeClock{now: now}
}

func (clock *fakeClock) Now() time.Time {
	clock.mu.Lock()
	defer clock.mu.Unlock()
	return clock.now
}

func (clock *fakeClock) Advance(duration time.Duration) {
	clock.mu.Lock()
	defer clock.mu.Unlock()
	clock.now = clock.now.Add(duration)
}
