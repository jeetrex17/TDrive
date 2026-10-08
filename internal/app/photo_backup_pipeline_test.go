package app

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"TDrive/backend/photobackup"
)

func TestPhotoBackupQueueOverlapsAndJoinsWorkers(t *testing.T) {
	entered := make(chan struct{}, 2)
	release := make(chan struct{})
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	var claimed atomic.Int64
	finished := make(chan int, 1)
	go func() {
		n, _ := runPhotoBackupQueue(ctx, 2, func() bool { return true }, func(ctx context.Context) (photobackup.RunResult, error) {
			if claimed.Add(1) > 2 {
				return photobackup.RunResult{}, nil
			}
			entered <- struct{}{}
			select {
			case <-release:
				return photobackup.RunResult{Claimed: true, Completed: true}, nil
			case <-ctx.Done():
				return photobackup.RunResult{Claimed: true}, ctx.Err()
			}
		})
		finished <- n
	}()
	for range 2 {
		select {
		case <-entered:
		case <-time.After(5 * time.Second):
			t.Fatal("workers did not overlap")
		}
	}
	select {
	case <-finished:
		t.Fatal("returned without joining active workers")
	default:
	}
	close(release)
	if n := <-finished; n != 2 {
		t.Fatalf("completed %d, want 2", n)
	}
}

func TestPhotoBackupQueueContinuesAfterFailedItem(t *testing.T) {
	calls := 0
	n, err := runPhotoBackupQueue(t.Context(), 1, func() bool { return true }, func(context.Context) (photobackup.RunResult, error) {
		calls++
		return photobackup.RunResult{Claimed: calls < 3, Completed: calls == 2}, nil
	})
	if err != nil || n != 1 || calls != 3 {
		t.Fatalf("completed=%d calls=%d err=%v", n, calls, err)
	}
}

func TestPhotoBackupRunCoalescesDiscoveryDuringPreviewDrain(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	a := &App{photoBackupRunID: 1, photoBackupCancel: cancel, photoBackupDone: make(chan struct{}), photoBackupRerun: true}
	if !a.continuePhotoBackupRun(ctx, 1) || a.photoBackupCancel == nil || a.photoBackupRerun {
		t.Fatal("lost discovery wake or retired active owner")
	}
	if a.continuePhotoBackupRun(ctx, 1) || a.photoBackupCancel != nil || a.photoBackupDone != nil {
		t.Fatal("idle run did not retire atomically")
	}
}

func TestPhotoBackupRunDoesNotRestartAfterStopOrClobberNewOwner(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	a := &App{photoBackupRunID: 2, photoBackupCancel: cancel, photoBackupDone: make(chan struct{}), photoBackupRerun: true}
	if a.continuePhotoBackupRun(ctx, 1) || a.photoBackupCancel == nil || !a.photoBackupRerun {
		t.Fatal("stale run changed current owner")
	}
	cancel()
	if a.continuePhotoBackupRun(ctx, 2) || a.photoBackupCancel != nil || a.photoBackupRerun {
		t.Fatal("canceled run restarted")
	}
}

func TestPhotoBackupRunBackgroundRetiresCoalescedWake(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	a := &App{photoBackupRunID: 1, photoBackupCancel: cancel, photoBackupDone: make(chan struct{}), photoBackupRerun: true}
	a.photoBackupBackground.background = true
	if a.continuePhotoBackupRun(ctx, 1) || a.photoBackupCancel != nil {
		t.Fatal("background discovery wake started a new cycle")
	}
}

func TestPhotoBackupQueueStopsOnDeferredAndBackground(t *testing.T) {
	for _, foreground := range []bool{false, true} {
		t.Run(fmt.Sprint(foreground), func(t *testing.T) {
			calls := 0
			n, err := runPhotoBackupQueue(t.Context(), 1, func() bool { return foreground }, func(context.Context) (photobackup.RunResult, error) {
				calls++
				return photobackup.RunResult{Claimed: true, Deferred: true}, nil
			})
			want := 0
			if foreground {
				want = 1
			}
			if n != 0 || err != nil || calls != want {
				t.Fatalf("completed=%d calls=%d err=%v", n, calls, err)
			}
		})
	}
}

func TestPhotoBackupQueueFatalErrorCancelsSibling(t *testing.T) {
	boom := errors.New("ledger unavailable")
	entered := make(chan struct{})
	var calls atomic.Int64
	_, err := runPhotoBackupQueue(t.Context(), 2, func() bool { return true }, func(ctx context.Context) (photobackup.RunResult, error) {
		if calls.Add(1) == 1 {
			close(entered)
			<-ctx.Done()
			return photobackup.RunResult{}, ctx.Err()
		}
		<-entered
		return photobackup.RunResult{}, boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("error=%v", err)
	}
}

func TestPhotoBackupReservationBoundsAndCancellation(t *testing.T) {
	budget := newPhotoBackupBudget()
	release, err := budget.acquire(t.Context(), maxPhotoBackupResourceBytes)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := budget.acquire(ctx, 1); !errors.Is(err, context.Canceled) {
		t.Fatalf("wait error=%v", err)
	}
	release()
	release, err = budget.acquire(t.Context(), 0) // unknown size reserves the full budget
	if err != nil {
		t.Fatal(err)
	}
	release()
	if _, err := budget.acquire(t.Context(), maxPhotoBackupResourceBytes+1); err == nil {
		t.Fatal("accepted oversized resource")
	}
}

func TestPhotoBackupTimingBucketsAreBoundedAndConcurrent(t *testing.T) {
	timings := &photoBackupTimings{}
	finished := make(chan struct{}, 2)
	for range 2 {
		go func() {
			for range 50 {
				timings.observeUpload("transfer", 3*time.Millisecond)
			}
			finished <- struct{}{}
		}()
	}
	for range 2 {
		<-finished
	}
	timings.observeUpload("unrecognized-stage", time.Hour)
	p := timings.phases[backupTransfer]
	if p.count != 100 || p.total != 300*time.Millisecond || p.quantileUpper(50) != 4*time.Millisecond || p.quantileUpper(95) != 4*time.Millisecond {
		t.Fatalf("timings=%+v", p)
	}
	timings.observe(backupOriginal, 24*time.Hour)
	if got := timings.phases[backupOriginal].quantileUpper(95); got != 24*time.Hour {
		t.Fatalf("overflow quantile=%v", got)
	}
}

// This benchmark isolates scheduling with simulated latency, not Telegram
// throughput. Every case performs the same 12 jobs and fixed per-job waits.
func BenchmarkPhotoBackupQueueLatency(b *testing.B) {
	for _, workers := range []int{1, 2, 3} {
		b.Run(fmt.Sprintf("workers=%d", workers), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				var jobs atomic.Int64
				_, err := runPhotoBackupQueue(b.Context(), workers, func() bool { return true }, func(context.Context) (photobackup.RunResult, error) {
					if jobs.Add(1) > 12 {
						return photobackup.RunResult{}, nil
					}
					time.Sleep(time.Millisecond)     // simulated preparation
					time.Sleep(2 * time.Millisecond) // simulated transport wait
					return photobackup.RunResult{Claimed: true, Completed: true}, nil
				})
				if err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
