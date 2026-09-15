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
	// Three requests per pooled connection keep every socket busy without
	// queueing deep inside any one of them.
	MaxConcurrentGetFile = 3 * MediaPoolSize

	// PlaybackGetFileReserve is the number of global slots that background work can
	// never consume, so foreground media playback always has headroom even while
	// downloads or thumbnail generation are saturating everything else. One per
	// pooled connection lets a seek land on every socket at once.
	PlaybackGetFileReserve = MediaPoolSize

	// MaxConcurrentBackgroundGetFile is the budget shared by all background getFile
	// work: disk downloads, seek-thumbnail generation, and playback read-ahead.
	// The remaining global slots stay reserved for foreground playback reads.
	MaxConcurrentBackgroundGetFile = MaxConcurrentGetFile - PlaybackGetFileReserve

	// DefaultDownloadThreads is the per-download random-access thread budget:
	// one block in flight per pooled connection. Two downloads fill the
	// background pool between them while playback keeps its reserve.
	DefaultDownloadThreads = MediaPoolSize

	backgroundGlobalRetry = 10 * time.Millisecond
)

var (
	getFileSlots           = semaphore.NewWeighted(MaxConcurrentGetFile)
	backgroundGetFileSlots = semaphore.NewWeighted(MaxConcurrentBackgroundGetFile)

	// uploadPartSlots is one budget of parts in flight shared by every upload.
	// Three files each keeping UploadThreads parts in flight saturate the link
	// just as well as eight parts in total do, but a small file then waits
	// behind the big ones' queue and takes seconds to finish. Waiters are
	// served in order, so the files take turns part by part.
	uploadPartSlots = semaphore.NewWeighted(UploadThreads)
)

// acquireUploadPartSlot reserves room for one upload part request.
func acquireUploadPartSlot(ctx context.Context) (func(), error) {
	if err := uploadPartSlots.Acquire(ctx, 1); err != nil {
		return nil, err
	}
	return func() { uploadPartSlots.Release(1) }, nil
}

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
