package tgclient

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func blockingScope(ready func()) func(context.Context, func()) error {
	return func(ctx context.Context, r func()) error {
		if ready != nil {
			ready()
		}
		r()
		<-ctx.Done()
		return ctx.Err()
	}
}

func TestLiveConnAcquireReady(t *testing.T) {
	lc := newLiveConn(blockingScope(nil))
	defer lc.Close()
	if err := lc.acquire(context.Background()); err != nil {
		t.Fatalf("acquire: %v", err)
	}
}

func TestLiveConnStartsOnceUnderConcurrency(t *testing.T) {
	var starts int32
	lc := newLiveConn(func(ctx context.Context, ready func()) error {
		atomic.AddInt32(&starts, 1)
		ready()
		<-ctx.Done()
		return ctx.Err()
	})
	defer lc.Close()

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := lc.acquire(context.Background()); err != nil {
				t.Errorf("acquire: %v", err)
			}
		}()
	}
	wg.Wait()

	if got := atomic.LoadInt32(&starts); got != 1 {
		t.Fatalf("scope started %d times, want 1", got)
	}
}

func TestLiveConnScopeFailsBeforeReady(t *testing.T) {
	wantErr := errors.New("dial failed")
	lc := newLiveConn(func(ctx context.Context, ready func()) error {
		return wantErr // never signals ready
	})
	defer lc.Close()

	if err := lc.acquire(context.Background()); !errors.Is(err, wantErr) {
		t.Fatalf("acquire err = %v, want %v", err, wantErr)
	}
}

func TestLiveConnAcquireRespectsCtx(t *testing.T) {
	block := make(chan struct{})
	lc := newLiveConn(func(ctx context.Context, ready func()) error {
		<-block // never becomes ready
		return nil
	})
	defer func() {
		close(block)
		lc.Close()
	}()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := lc.acquire(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("acquire err = %v, want context.Canceled", err)
	}
}

func TestLiveConnCloseStopsScope(t *testing.T) {
	stopped := make(chan struct{})
	lc := newLiveConn(func(ctx context.Context, ready func()) error {
		ready()
		<-ctx.Done()
		close(stopped)
		return ctx.Err()
	})

	if err := lc.acquire(context.Background()); err != nil {
		t.Fatalf("acquire: %v", err)
	}
	lc.Close()

	select {
	case <-stopped:
	case <-time.After(2 * time.Second):
		t.Fatal("scope did not stop after Close")
	}
	if err := lc.acquire(context.Background()); !errors.Is(err, errScopeClosed) {
		t.Fatalf("acquire after Close = %v, want errScopeClosed", err)
	}
}

func TestLiveConnRestartsAfterFailure(t *testing.T) {
	var attempt int32
	lc := newLiveConn(func(ctx context.Context, ready func()) error {
		if atomic.AddInt32(&attempt, 1) == 1 {
			return errors.New("first attempt fails")
		}
		ready()
		<-ctx.Done()
		return ctx.Err()
	})
	defer lc.Close()

	if err := lc.acquire(context.Background()); err == nil {
		t.Fatal("first acquire should fail")
	}
	if err := lc.acquire(context.Background()); err != nil {
		t.Fatalf("second acquire should succeed, got %v", err)
	}
	if got := atomic.LoadInt32(&attempt); got != 2 {
		t.Fatalf("attempts = %d, want 2", got)
	}
}

func TestLiveConnRestartsAfterReadyScopeDies(t *testing.T) {
	var attempt int32
	dropFirst := make(chan struct{})
	lc := newLiveConn(func(ctx context.Context, ready func()) error {
		n := atomic.AddInt32(&attempt, 1)
		ready()
		if n == 1 {
			<-dropFirst
			return errors.New("connection dropped")
		}
		<-ctx.Done()
		return ctx.Err()
	})
	defer lc.Close()

	if err := lc.acquire(context.Background()); err != nil {
		t.Fatalf("first acquire: %v", err)
	}
	close(dropFirst)

	deadline := time.After(2 * time.Second)
	for {
		lc.mu.Lock()
		dead := lc.scope != nil && lc.scope.done
		lc.mu.Unlock()
		if dead {
			break
		}
		select {
		case <-deadline:
			t.Fatal("first scope did not exit")
		default:
			time.Sleep(time.Millisecond)
		}
	}

	if err := lc.acquire(context.Background()); err != nil {
		t.Fatalf("second acquire should restart and succeed, got %v", err)
	}
	if got := atomic.LoadInt32(&attempt); got != 2 {
		t.Fatalf("attempts = %d, want 2", got)
	}
}

func TestLiveConnCloseWithoutStart(t *testing.T) {
	lc := newLiveConn(blockingScope(nil))
	lc.Close() // must not panic or block
}
