package mountwrite

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

func TestKeyedLockerSerializesSameKeyAndAllowsDifferentKeys(t *testing.T) {
	t.Parallel()

	locker := NewKeyedLocker()
	releaseA, err := locker.Lock(context.Background(), "drive/object-a")
	if err != nil {
		t.Fatalf("lock a: %v", err)
	}

	differentDone := make(chan struct{})
	go func() {
		release, lockErr := locker.Lock(context.Background(), "drive/object-b")
		if lockErr == nil {
			release()
		}
		close(differentDone)
	}()
	select {
	case <-differentDone:
	case <-time.After(time.Second):
		t.Fatal("different key was unnecessarily blocked")
	}

	sameAcquired := make(chan struct{})
	go func() {
		release, lockErr := locker.Lock(context.Background(), "drive/object-a")
		if lockErr == nil {
			close(sameAcquired)
			release()
		}
	}()
	select {
	case <-sameAcquired:
		t.Fatal("same key acquired before release")
	case <-time.After(30 * time.Millisecond):
	}
	releaseA()
	select {
	case <-sameAcquired:
	case <-time.After(time.Second):
		t.Fatal("same key did not acquire after release")
	}
}

func TestKeyedLockerSortsDeduplicatesAndCleansEntries(t *testing.T) {
	t.Parallel()

	locker := NewKeyedLocker()
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			release, err := locker.Lock(context.Background(), "b", "a", "a")
			if err == nil {
				release()
			}
		}()
		go func() {
			defer wg.Done()
			release, err := locker.Lock(context.Background(), "a", "b")
			if err == nil {
				release()
			}
		}()
	}

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("sorted multi-key locks deadlocked")
	}
	if got := locker.Len(); got != 0 {
		t.Fatalf("retained lock entries = %d, want 0", got)
	}
}

func TestKeyedLockerHonorsCanceledWait(t *testing.T) {
	t.Parallel()

	locker := NewKeyedLocker()
	release, err := locker.Lock(context.Background(), "busy")
	if err != nil {
		t.Fatalf("first lock: %v", err)
	}
	defer release()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := locker.Lock(ctx, "busy"); !errors.Is(err, ErrCanceled) {
		t.Fatalf("canceled lock error = %v, want ErrCanceled", err)
	}
	if got := locker.Len(); got != 1 {
		t.Fatalf("lock entries after canceled waiter = %d, want 1 held entry", got)
	}
}
