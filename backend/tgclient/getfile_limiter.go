package tgclient

import (
	"context"
	"time"

	"golang.org/x/sync/semaphore"
)

const (
	// MaxConcurrentGetFile caps low-level Telegram upload.getFile pressure across
	// every caller. Keeping it process-wide avoids multiplicative fan-out such as
	// multipart parts * per-file threads, which would otherwise trip FLOOD_WAIT.
	MaxConcurrentGetFile = 8

	// PlaybackGetFileReserve is the number of global slots that background work can
	// never consume, so foreground media playback always has headroom even while
	// downloads or thumbnail generation are saturating everything else.
	PlaybackGetFileReserve = 2

	// MaxConcurrentBackgroundGetFile is the budget shared by all background getFile
	// work: disk downloads, seek-thumbnail generation, and playback read-ahead.
	// The remaining global slots stay reserved for foreground playback reads.
	MaxConcurrentBackgroundGetFile = MaxConcurrentGetFile - PlaybackGetFileReserve

	// DefaultDownloadThreads is the per-download random-access thread budget. With
	// two multipart parts in flight this fills the background pool while still
	// preserving playback headroom under MaxConcurrentGetFile.
	DefaultDownloadThreads = 3

	backgroundGlobalRetry = 10 * time.Millisecond
)

var (
	getFileSlots           = semaphore.NewWeighted(MaxConcurrentGetFile)
	backgroundGetFileSlots = semaphore.NewWeighted(MaxConcurrentBackgroundGetFile)
)

// AcquireGetFileSlots reserves n global getFile slots for foreground media
// playback reads. Background work (downloads, thumbnails, read-ahead) must use
// AcquireBackgroundGetFileSlots so playback keeps its reserved headroom.
func AcquireGetFileSlots(ctx context.Context, n int) (func(), error) {
	weight := int64(clampGetFileWeight(n, MaxConcurrentGetFile))
	if err := getFileSlots.Acquire(ctx, weight); err != nil {
		return nil, err
	}
	return func() {
		getFileSlots.Release(weight)
	}, nil
}

// AcquireBackgroundGetFileSlots reserves n getFile slots for background work:
// disk downloads, seek-thumbnail generation, and playback read-ahead. It gates
// through a smaller background pool first, then enters the global pool with a
// non-queued TryAcquire. That second detail matters: a queued multi-slot
// background acquire must never sit at the head of the global semaphore and block
// a one-slot foreground playback read from using its reserved capacity.
func AcquireBackgroundGetFileSlots(ctx context.Context, n int) (func(), error) {
	weight := int64(clampGetFileWeight(n, MaxConcurrentBackgroundGetFile))
	if err := backgroundGetFileSlots.Acquire(ctx, weight); err != nil {
		return nil, err
	}

	releaseGlobal, err := acquireGlobalGetFileSlotsNoQueue(ctx, weight)
	if err != nil {
		backgroundGetFileSlots.Release(weight)
		return nil, err
	}
	return func() {
		releaseGlobal()
		backgroundGetFileSlots.Release(weight)
	}, nil
}

func acquireGlobalGetFileSlotsNoQueue(ctx context.Context, weight int64) (func(), error) {
	weight = int64(clampGetFileWeight(int(weight), MaxConcurrentGetFile))
	timer := time.NewTimer(backgroundGlobalRetry)
	defer timer.Stop()
	first := true
	for {
		if getFileSlots.TryAcquire(weight) {
			return func() {
				getFileSlots.Release(weight)
			}, nil
		}
		if first {
			first = false
		} else {
			timer.Reset(backgroundGlobalRetry)
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
}

func clampGetFileWeight(n, max int) int {
	if n < 1 {
		return 1
	}
	if n > max {
		return max
	}
	return n
}
