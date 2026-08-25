package mountcache

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

func TestCoalescerRejectsNilContext(t *testing.T) {
	loads := NewCoalescer[string, int](0)
	_, err := loads.Load(nil, context.Background(), "key", nil, func(context.Context) (int, error) {
		return 1, nil
	}, nil)
	if !errors.Is(err, ErrNilContext) {
		t.Fatalf("Load(nil) error = %v, want ErrNilContext", err)
	}
}

func TestCoalescerSharesLoadAndKeepsItAliveForRemainingWaiters(t *testing.T) {
	cache := NewLRU[string, int](LRUConfig[int]{Capacity: 2})
	loads := NewCoalescer[string, int](0)
	started := make(chan struct{})
	release := make(chan struct{})
	var calls atomic.Int32
	loader := func(ctx context.Context) (int, error) {
		calls.Add(1)
		close(started)
		select {
		case <-release:
			return 42, nil
		case <-ctx.Done():
			return 0, ctx.Err()
		}
	}
	load := func(ctx context.Context) (int, error) {
		return loads.Load(ctx, context.Background(), "shared", func() (int, bool) {
			return cache.Get("shared")
		}, loader, func(value int) {
			cache.Put("shared", value)
		})
	}

	cancelledContext, cancel := context.WithCancel(context.Background())
	first := make(chan loadResult[int], 1)
	go func() {
		value, err := load(cancelledContext)
		first <- loadResult[int]{value: value, err: err}
	}()
	waitSignal(t, started, "shared load")

	second := make(chan loadResult[int], 1)
	go func() {
		value, err := load(context.Background())
		second <- loadResult[int]{value: value, err: err}
	}()
	waitForWaiters(t, loads, "shared", 2)
	cancel()
	if result := waitResult(t, first); !errors.Is(result.err, context.Canceled) {
		t.Fatalf("canceled waiter error = %v, want context.Canceled", result.err)
	}
	close(release)
	if result := waitResult(t, second); result.err != nil || result.value != 42 {
		t.Fatalf("remaining waiter = %+v, want value 42", result)
	}

	value, err := load(context.Background())
	if err != nil || value != 42 {
		t.Fatalf("cached Load() = %d, %v, want 42, nil", value, err)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("loader calls = %d, want 1", got)
	}
}

func TestCoalescerInvalidationDetachesStaleLoadAndEvictsAtomically(t *testing.T) {
	errInvalidated := errors.New("invalidated")
	cache := NewLRU[string, string](LRUConfig[string]{Capacity: 2})
	loads := NewCoalescer[string, string](2)
	staleStarted := make(chan struct{})
	staleCanceled := make(chan struct{})
	releaseStale := make(chan struct{})
	staleReturned := make(chan struct{})
	var calls atomic.Int32
	loader := func(ctx context.Context) (string, error) {
		call := calls.Add(1)
		if call == 1 {
			close(staleStarted)
			<-ctx.Done()
			close(staleCanceled)
			<-releaseStale
			close(staleReturned)
			return "stale", nil
		}
		return "fresh", nil
	}
	load := func(ctx context.Context) (string, error) {
		return loads.Load(ctx, context.Background(), "key", func() (string, bool) {
			return cache.Get("key")
		}, loader, func(value string) {
			cache.Put("key", value)
		})
	}

	stale := make(chan loadResult[string], 1)
	go func() {
		value, err := load(context.Background())
		stale <- loadResult[string]{value: value, err: err}
	}()
	waitSignal(t, staleStarted, "stale load")
	loads.Invalidate([]string{"key"}, errInvalidated, func(string) []string {
		cache.Delete("key")
		return nil
	})
	if result := waitResult(t, stale); !errors.Is(result.err, errInvalidated) {
		t.Fatalf("invalidated load error = %v, want sentinel", result.err)
	}
	waitSignal(t, staleCanceled, "stale load cancellation")

	fresh, err := load(context.Background())
	if err != nil || fresh != "fresh" {
		t.Fatalf("replacement Load() = %q, %v, want fresh, nil", fresh, err)
	}
	close(releaseStale)
	waitSignal(t, staleReturned, "stale load return")
	if got, ok := cache.Get("key"); !ok || got != "fresh" {
		t.Fatalf("cached value after stale completion = %q, %v, want fresh, true", got, ok)
	}
}

func TestCoalescerCancelsAbandonedLoadAndDoesNotCacheLateSuccess(t *testing.T) {
	cache := NewLRU[string, int](LRUConfig[int]{Capacity: 1})
	loads := NewCoalescer[string, int](1)
	started := make(chan struct{})
	canceled := make(chan struct{})
	release := make(chan struct{})
	returned := make(chan struct{})
	loader := func(ctx context.Context) (int, error) {
		close(started)
		<-ctx.Done()
		close(canceled)
		<-release
		close(returned)
		return 1, nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan loadResult[int], 1)
	go func() {
		value, err := loads.Load(ctx, context.Background(), "key", func() (int, bool) {
			return cache.Get("key")
		}, loader, func(value int) {
			cache.Put("key", value)
		})
		result <- loadResult[int]{value: value, err: err}
	}()
	waitSignal(t, started, "abandoned load")
	cancel()
	if got := waitResult(t, result); !errors.Is(got.err, context.Canceled) {
		t.Fatalf("abandoned waiter error = %v, want context.Canceled", got.err)
	}
	waitSignal(t, canceled, "loader cancellation")
	close(release)
	waitSignal(t, returned, "abandoned load return")
	if _, ok := cache.Get("key"); ok {
		t.Fatal("late success from an abandoned load was cached")
	}
}

func TestCoalescerCloseCancelsActiveLoadAndRejectsNewLoads(t *testing.T) {
	errStopped := errors.New("stopped")
	loads := NewCoalescer[string, int](1)
	started := make(chan struct{})
	canceled := make(chan struct{})
	release := make(chan struct{})
	result := make(chan loadResult[int], 1)
	go func() {
		value, err := loads.Load(
			context.Background(),
			context.Background(),
			"active",
			nil,
			func(ctx context.Context) (int, error) {
				close(started)
				<-ctx.Done()
				close(canceled)
				<-release
				return 1, nil
			},
			nil,
		)
		result <- loadResult[int]{value: value, err: err}
	}()
	waitSignal(t, started, "active load")

	loads.Close(errStopped)
	if got := waitResult(t, result); !errors.Is(got.err, errStopped) {
		t.Fatalf("closed load error = %v, want stopped sentinel", got.err)
	}
	waitSignal(t, canceled, "closed loader cancellation")
	close(release)
	if got := loads.Len(); got != 0 {
		t.Fatalf("Len() after Close = %d, want 0", got)
	}
	if got := loads.Waiters("active"); got != 0 {
		t.Fatalf("Waiters(active) after Close = %d, want 0", got)
	}

	_, err := loads.Load(
		context.Background(),
		context.Background(),
		"new",
		nil,
		func(context.Context) (int, error) { return 2, nil },
		nil,
	)
	if !errors.Is(err, errStopped) {
		t.Fatalf("Load() after Close error = %v, want stopped sentinel", err)
	}
	loads.Close(errors.New("ignored second close"))
}

func TestCoalescerInvalidationWalksDependentKeysOnce(t *testing.T) {
	loads := NewCoalescer[string, int](0)
	visited := make(map[string]int)
	loads.Invalidate([]string{"root", "root"}, errors.New("refresh"), func(key string) []string {
		visited[key]++
		switch key {
		case "root":
			return []string{"child", "root"}
		case "child":
			return []string{"root"}
		default:
			return nil
		}
	})
	if visited["root"] != 1 || visited["child"] != 1 || len(visited) != 2 {
		t.Fatalf("invalidation visits = %v, want root and child once", visited)
	}

	var nilLoads *Coalescer[string, int]
	nilVisited := false
	nilLoads.Invalidate([]string{"key"}, nil, func(string) []string {
		nilVisited = true
		return nil
	})
	if !nilVisited {
		t.Fatal("nil coalescer skipped its eviction callback")
	}
}

type loadResult[V any] struct {
	value V
	err   error
}

func waitResult[V any](t *testing.T, results <-chan loadResult[V]) loadResult[V] {
	t.Helper()
	select {
	case result := <-results:
		return result
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for load result")
		var zero loadResult[V]
		return zero
	}
}

func waitSignal(t *testing.T, signal <-chan struct{}, description string) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for %s", description)
	}
}

func waitForWaiters[K comparable, V any](
	t *testing.T,
	loads *Coalescer[K, V],
	key K,
	want int,
) {
	t.Helper()
	eventually(t, func() bool { return loads.Waiters(key) == want }, "coalesced waiters")
}

func eventually(t *testing.T, condition func() bool, description string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for !condition() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", description)
		}
		time.Sleep(time.Millisecond)
	}
}
