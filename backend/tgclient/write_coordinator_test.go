package tgclient

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"
)

type coordinatorTestClock struct {
	mu           sync.Mutex
	now          time.Time
	sleepStarted chan time.Duration
	sleepRelease chan struct{}
}

func newCoordinatorTestClock(now time.Time) *coordinatorTestClock {
	return &coordinatorTestClock{
		now:          now,
		sleepStarted: make(chan time.Duration, 8),
		sleepRelease: make(chan struct{}, 8),
	}
}

func (c *coordinatorTestClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *coordinatorTestClock) Set(now time.Time) {
	c.mu.Lock()
	c.now = now
	c.mu.Unlock()
}

func (c *coordinatorTestClock) Sleep(ctx context.Context, wait time.Duration) error {
	select {
	case c.sleepStarted <- wait:
	case <-ctx.Done():
		return ctx.Err()
	}
	select {
	case <-c.sleepRelease:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func TestWriteCoordinatorSharesLongestFloodWaitAcrossWriteClasses(t *testing.T) {
	start := time.Unix(1_700_000_000, 0)
	clock := newCoordinatorTestClock(start)
	coordinator := newWriteCoordinator(clock.Now, clock.Sleep)

	firstEntered := make(chan struct{})
	secondEntered := make(chan struct{})
	releaseFirst := make(chan struct{})
	releaseSecond := make(chan struct{})
	firstResult := make(chan error, 1)
	secondResult := make(chan error, 1)

	go func() {
		firstResult <- coordinator.Do(context.Background(), writeClassUploadPart, func() error {
			close(firstEntered)
			<-releaseFirst
			return NewFloodWaitError(2 * time.Second)
		})
	}()
	go func() {
		secondResult <- coordinator.Do(context.Background(), writeClassUploadPart, func() error {
			close(secondEntered)
			<-releaseSecond
			return NewFloodWaitError(5 * time.Second)
		})
	}()

	// Upload parts share backpressure state but must remain independently
	// runnable; serializing their RPCs would erase gotd's multipart parallelism.
	<-firstEntered
	<-secondEntered
	close(releaseFirst)
	if err := <-firstResult; !errors.Is(err, ErrFloodWait) {
		t.Fatalf("first upload-part error = %v, want FLOOD_WAIT", err)
	}
	close(releaseSecond)
	if err := <-secondResult; !errors.Is(err, ErrFloodWait) {
		t.Fatalf("second upload-part error = %v, want FLOOD_WAIT", err)
	}

	messageAction := make(chan struct{}, 1)
	uploadAction := make(chan struct{}, 1)
	messageResult := make(chan error, 1)
	uploadResult := make(chan error, 1)
	go func() {
		messageResult <- coordinator.Do(context.Background(), writeClassMessage, func() error {
			messageAction <- struct{}{}
			return nil
		})
	}()
	go func() {
		uploadResult <- coordinator.Do(context.Background(), writeClassUploadPart, func() error {
			uploadAction <- struct{}{}
			return nil
		})
	}()

	for i := 0; i < 2; i++ {
		if wait := <-clock.sleepStarted; wait != 5*time.Second {
			t.Fatalf("shared cooldown wait = %s, want 5s", wait)
		}
	}
	assertNoCoordinatorAction(t, messageAction, "message")
	assertNoCoordinatorAction(t, uploadAction, "upload part")

	clock.Set(start.Add(5 * time.Second))
	clock.sleepRelease <- struct{}{}
	clock.sleepRelease <- struct{}{}
	<-messageAction
	<-uploadAction
	if err := <-messageResult; err != nil {
		t.Fatalf("message after cooldown: %v", err)
	}
	if err := <-uploadResult; err != nil {
		t.Fatalf("upload part after cooldown: %v", err)
	}
}

func TestWriteCoordinatorSerializesMessagesAndRechecksCooldownAfterQueue(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		start := time.Unix(1_700_000_000, 0)
		clock := newCoordinatorTestClock(start)
		coordinator := newWriteCoordinator(clock.Now, clock.Sleep)

		firstEntered := make(chan struct{})
		releaseFirst := make(chan struct{})
		firstResult := make(chan error, 1)
		go func() {
			firstResult <- coordinator.Do(context.Background(), writeClassMessage, func() error {
				close(firstEntered)
				<-releaseFirst
				return nil
			})
		}()
		<-firstEntered

		secondAction := make(chan struct{}, 1)
		secondResult := make(chan error, 1)
		go func() {
			secondResult <- coordinator.Do(context.Background(), writeClassMessage, func() error {
				secondAction <- struct{}{}
				return nil
			})
		}()
		synctest.Wait()
		assertNoCoordinatorAction(t, secondAction, "second message")

		// Upload parts are not held behind the logical-message capacity. The
		// resulting FLOOD_WAIT arrives while message two is queued.
		err := coordinator.Do(context.Background(), writeClassUploadPart, func() error {
			return NewFloodWaitError(4 * time.Second)
		})
		if !errors.Is(err, ErrFloodWait) {
			t.Fatalf("upload-part error = %v, want FLOOD_WAIT", err)
		}

		close(releaseFirst)
		if err := <-firstResult; err != nil {
			t.Fatalf("first message: %v", err)
		}
		if wait := <-clock.sleepStarted; wait != 4*time.Second {
			t.Fatalf("queued message cooldown = %s, want 4s", wait)
		}
		assertNoCoordinatorAction(t, secondAction, "second message during cooldown")

		clock.Set(start.Add(4 * time.Second))
		clock.sleepRelease <- struct{}{}
		<-secondAction
		if err := <-secondResult; err != nil {
			t.Fatalf("second message: %v", err)
		}
	})
}

func TestWriteCoordinatorCancellationPreventsAction(t *testing.T) {
	clock := newCoordinatorTestClock(time.Unix(1_700_000_000, 0))
	coordinator := newWriteCoordinator(clock.Now, clock.Sleep)
	if err := coordinator.Do(context.Background(), writeClassUploadPart, func() error {
		return NewFloodWaitError(time.Minute)
	}); !errors.Is(err, ErrFloodWait) {
		t.Fatalf("seed cooldown error = %v, want FLOOD_WAIT", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var called atomic.Bool
	err := coordinator.Do(ctx, writeClassMessage, func() error {
		called.Store(true)
		return nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled call error = %v, want context canceled", err)
	}
	if called.Load() {
		t.Fatal("action ran after cancellation")
	}

	err = coordinator.Do(nil, writeClassMessage, func() error {
		called.Store(true)
		return nil
	})
	if err == nil {
		t.Fatal("nil context was accepted")
	}
	if called.Load() {
		t.Fatal("action ran with a nil context")
	}
}

func assertNoCoordinatorAction(t *testing.T, action <-chan struct{}, label string) {
	t.Helper()
	select {
	case <-action:
		t.Fatalf("%s action ran before the shared cooldown elapsed", label)
	default:
	}
}
