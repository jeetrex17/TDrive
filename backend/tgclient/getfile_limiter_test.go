package tgclient

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestBackgroundLimiterLeavesPlaybackHeadroom(t *testing.T) {
	ctx := context.Background()

	releaseA, err := AcquireBackgroundGetFileSlots(ctx, DefaultDownloadThreads)
	if err != nil {
		t.Fatalf("acquire first download: %v", err)
	}
	defer releaseA()

	releaseB, err := AcquireBackgroundGetFileSlots(ctx, DefaultDownloadThreads)
	if err != nil {
		t.Fatalf("acquire second download: %v", err)
	}
	defer releaseB()

	blockedCtx, cancelBlocked := context.WithCancel(ctx)
	blocked := make(chan error, 1)
	go func() {
		release, err := AcquireBackgroundGetFileSlots(blockedCtx, DefaultDownloadThreads)
		if err == nil {
			release()
		}
		blocked <- err
	}()

	select {
	case err := <-blocked:
		t.Fatalf("third download acquired despite full download pool: %v", err)
	case <-time.After(20 * time.Millisecond):
	}

	playbackCtx, cancelPlayback := context.WithTimeout(ctx, time.Second)
	defer cancelPlayback()
	releasePlayback, err := AcquireGetFileSlots(playbackCtx, 1)
	if err != nil {
		t.Fatalf("playback should acquire reserved getFile slot while downloads are full: %v", err)
	}
	releasePlayback()

	cancelBlocked()
	if err := <-blocked; !errors.Is(err, context.Canceled) {
		t.Fatalf("blocked download err = %v, want context.Canceled", err)
	}
}

func TestBackgroundLimiterDoesNotHeadOfLineBlockPlayback(t *testing.T) {
	ctx := context.Background()

	releaseDownload, err := AcquireBackgroundGetFileSlots(ctx, DefaultDownloadThreads)
	if err != nil {
		t.Fatalf("acquire download: %v", err)
	}
	defer releaseDownload()

	// Fill the remaining global slots with foreground reads. A second download
	// still has room in the download pool, but not enough global capacity; it must
	// wait outside the global semaphore so it cannot block later playback reads.
	var foreground []func()
	defer func() {
		for _, release := range foreground {
			release()
		}
	}()
	for i := 0; i < MaxConcurrentGetFile-DefaultDownloadThreads; i++ {
		release, err := AcquireGetFileSlots(ctx, 1)
		if err != nil {
			t.Fatalf("acquire foreground fill slot %d: %v", i, err)
		}
		foreground = append(foreground, release)
	}

	blockedCtx, cancelBlocked := context.WithCancel(ctx)
	blockedDownload := make(chan error, 1)
	go func() {
		release, err := AcquireBackgroundGetFileSlots(blockedCtx, DefaultDownloadThreads)
		if err == nil {
			release()
		}
		blockedDownload <- err
	}()

	select {
	case err := <-blockedDownload:
		t.Fatalf("second download acquired despite full global pool: %v", err)
	case <-time.After(20 * time.Millisecond):
	}

	playbackAcquired := make(chan error, 1)
	go func() {
		release, err := AcquireGetFileSlots(ctx, 1)
		if err == nil {
			release()
		}
		playbackAcquired <- err
	}()

	// Free exactly one global slot. A queued weight-3 download would block this
	// one-slot playback acquire; the non-queued download path leaves it available.
	foreground[0]()
	foreground = foreground[1:]
	select {
	case err := <-playbackAcquired:
		if err != nil {
			t.Fatalf("playback acquire: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("playback acquire was head-of-line blocked by download")
	}

	cancelBlocked()
	if err := <-blockedDownload; !errors.Is(err, context.Canceled) {
		t.Fatalf("blocked download err = %v, want context.Canceled", err)
	}
}
