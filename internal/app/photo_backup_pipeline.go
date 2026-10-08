package app

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"TDrive/backend/photobackup"
	"golang.org/x/sync/semaphore"
)

const photoBackupConcurrency = 2

// Discovery can enqueue after originals drain while optional previews are still
// finishing. Consume its coalesced wake or retire the owner under the same lock
// used by startPhotoBackup, so no start request is lost between those steps.
// All original and preview workers must be joined before calling this method.
func (a *App) continuePhotoBackupRun(ctx context.Context, runID uint64) bool {
	a.photoBackupMu.Lock()
	defer a.photoBackupMu.Unlock()
	if a.photoBackupRunID != runID {
		return false
	}
	if a.photoBackupRerun && ctx.Err() == nil && a.photoBackupMayStartNextJob() {
		a.photoBackupRerun = false
		return true
	}
	a.photoBackupRerun = false
	a.photoBackupCancel = nil
	a.photoBackupDone = nil
	return false
}

// Workers overlap staging/encryption with another original's transfer. The file
// service and Telegram transport retain their shared concurrency limits.
func runPhotoBackupQueue(ctx context.Context, workers int, mayStart func() bool, next func(context.Context) (photobackup.RunResult, error)) (int, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var wg sync.WaitGroup
	var mu sync.Mutex
	completed := 0
	var firstErr error
	for range max(1, min(workers, 3)) {
		wg.Go(func() {
			for ctx.Err() == nil && mayStart() {
				result, err := next(ctx)
				mu.Lock()
				if result.Completed {
					completed++
				}
				if err != nil && firstErr == nil {
					firstErr = err
					cancel()
				}
				mu.Unlock()
				if err != nil || !result.Claimed || result.Deferred {
					return
				}
			}
		})
	}
	wg.Wait()
	if firstErr == nil {
		firstErr = ctx.Err()
	}
	return completed, firstErr
}

type photoBackupBudget struct {
	bytes        *semaphore.Weighted
	mu           sync.Mutex
	active, peak int64
}

func newPhotoBackupBudget() *photoBackupBudget {
	return &photoBackupBudget{bytes: semaphore.NewWeighted(maxPhotoBackupResourceBytes)}
}

// A reservation covers the original until its private stage has been removed.
// Unknown-size resources take the entire budget. Ciphertext scratch is bounded
// separately by the uploader to one part per worker (or the smaller whole file).
func (b *photoBackupBudget) acquire(ctx context.Context, size int64) (func(), error) {
	if size > maxPhotoBackupResourceBytes || size < 0 {
		return nil, fmt.Errorf("photo backup: invalid resource size or exceeds the 4 GiB limit")
	}
	if size == 0 {
		size = maxPhotoBackupResourceBytes
	}
	if err := b.bytes.Acquire(ctx, size); err != nil {
		return nil, err
	}
	b.mu.Lock()
	b.active += size
	b.peak = max(b.peak, b.active)
	b.mu.Unlock()
	return func() {
		b.mu.Lock()
		b.active -= size
		b.mu.Unlock()
		b.bytes.Release(size)
	}, nil
}

type photoBackupPhase int

const (
	backupBudgetWait photoBackupPhase = iota
	backupMaterialize
	backupDestination
	backupOriginal
	backupLedger
	backupEncrypt
	backupTransfer
	backupManifest
	backupProjection
	backupPreviewPrepare
	backupPreviewSend
	backupPreviewAdmission
	backupPhaseCount
)

// Fixed logarithmic histograms avoid retaining per-file names, paths or samples.
// Quantiles are bucket upper bounds, not exact percentiles. Durations can overlap
// across workers and must not be added to infer wall-clock throughput.
type photoBackupTimings struct {
	mu             sync.Mutex
	phases         [backupPhaseCount]photoBackupTiming
	completedBytes int64
}

func (t *photoBackupTimings) observeUpload(stage string, elapsed time.Duration) {
	var phase photoBackupPhase
	switch stage {
	case "encrypt":
		phase = backupEncrypt
	case "transfer":
		phase = backupTransfer
	case "manifest_commit":
		phase = backupManifest
	case "projection":
		phase = backupProjection
	case "preview_prepare":
		phase = backupPreviewPrepare
	case "preview_send":
		phase = backupPreviewSend
	case "preview_admission":
		phase = backupPreviewAdmission
	default:
		return
	}
	t.observe(phase, elapsed)
}

type photoBackupTiming struct {
	count   int64
	total   time.Duration
	max     time.Duration
	buckets [24]int64
}

func (t *photoBackupTimings) observe(phase photoBackupPhase, duration time.Duration) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	p := &t.phases[phase]
	p.count++
	p.total += duration
	p.max = max(p.max, duration)
	bucket, limit := 0, time.Millisecond
	for duration > limit && bucket < len(p.buckets)-1 {
		bucket++
		limit *= 2
	}
	p.buckets[bucket]++
}

func (p photoBackupTiming) quantileUpper(percent int64) time.Duration {
	target := (p.count*percent + 99) / 100
	var count int64
	for i, n := range p.buckets {
		count += n
		if count >= target {
			return max(time.Millisecond<<i, p.maxIfOverflow(i))
		}
	}
	return 0
}

func (p photoBackupTiming) maxIfOverflow(i int) time.Duration {
	if i == len(p.buckets)-1 {
		return p.max
	}
	return 0
}

func (t *photoBackupTimings) log(elapsed time.Duration, completed int) {
	t.mu.Lock()
	phases := t.phases
	bytes := t.completedBytes
	t.mu.Unlock()
	slog.Info("photo backup: pipeline summary", "workers", photoBackupConcurrency, "completed", completed, "known_original_bytes", bytes, "originals_elapsed_ms", elapsed.Milliseconds())
	for i, name := range [...]string{"budget_wait", "materialize", "destination", "original_upload_and_projection", "ledger", "encrypt", "transfer", "manifest_commit", "projection", "preview_prepare", "preview_send", "preview_admission"} {
		p := phases[i]
		if p.count == 0 {
			continue
		}
		slog.Info("photo backup: stage timing", "stage", name, "samples", p.count, "total_ms", p.total.Milliseconds(), "max_ms", p.max.Milliseconds(), "p50_upper_ms", p.quantileUpper(50).Milliseconds(), "p95_upper_ms", p.quantileUpper(95).Milliseconds())
	}
}
