package file

import (
	"bytes"
	"context"
	"fmt"
	"sync"
	"time"

	"TDrive/backend/projection"
)

const backupRenditionSnapshotBytes = 30 << 20

type backupRenditionJob struct {
	source   projection.File
	snapshot []byte
}

// BackupRenditionWorker belongs to one backup run and one authenticated drive.
// Two fixed goroutines separate local preparation from potentially slow sends.
// At most two snapshots, together at most 30 MiB, are reserved across uploading
// originals and retained preview jobs. The uploader's own immutable source copy
// and decoder working memory are separate bounds. Keys are acquired only during
// preparation, never retained in queued jobs.
type BackupRenditionWorker struct {
	channelID int64
	ctx       context.Context
	cancel    context.CancelFunc
	mu        sync.Mutex
	closed    bool
	bytes     int
	count     int
	changed   chan struct{}
	pending   chan backupRenditionJob
	wake      chan struct{}
	prepared  chan struct{}
	wg        sync.WaitGroup
}

// NewBackupRenditionWorker resumes already-encrypted outbox entries without
// delaying original uploads. The owner must call Close before releasing the
// account, drive or vault key, including on pause, lock and logout.
func (s *Service) NewBackupRenditionWorker(ctx context.Context, channelID int64) *BackupRenditionWorker {
	return newBackupRenditionWorker(ctx, channelID, func(ctx context.Context, source projection.File, snapshot []byte) error {
		started := time.Now()
		defer func() { reportBackupTiming(ctx, "preview_prepare", started) }()
		err := s.prepareRenditions(ctx, source, bytes.NewReader(snapshot), false)
		if err != nil && ctx.Err() == nil {
			s.warnf("Backup preview preparation failed for file %d: %v\n", source.MsgID, err)
		}
		return err
	}, func(ctx context.Context) error {
		started := time.Now()
		defer func() { reportBackupTiming(ctx, "preview_send", started) }()
		// Derivatives share the same process-wide file budget as originals.
		release, err := s.acquireUploadSlot(ctx)
		if err == nil {
			defer release()
			err = s.ResumeRenditionUploads(ctx, channelID, 128)
		}
		if err != nil && ctx.Err() == nil {
			s.warnf("Backup preview send deferred: %v\n", err)
		}
		return err
	})
}

func newBackupRenditionWorker(ctx context.Context, channelID int64, prepare func(context.Context, projection.File, []byte) error, send func(context.Context) error) *BackupRenditionWorker {
	ctx, cancel := context.WithCancel(ctx)
	w := &BackupRenditionWorker{channelID: channelID, ctx: ctx, cancel: cancel, changed: make(chan struct{}), pending: make(chan backupRenditionJob, 2), wake: make(chan struct{}, 1), prepared: make(chan struct{})}
	w.wg.Go(func() {
		defer close(w.prepared)
		for job := range w.pending {
			if ctx.Err() == nil {
				_ = prepare(ctx, job.source, job.snapshot)
				// Preparation can persist one kind before an error; wake the
				// sender even then so that durable partial work is not stranded.
				select {
				case w.wake <- struct{}{}:
				default:
				}
			}
			clear(job.snapshot)
			w.mu.Lock()
			w.bytes -= len(job.snapshot)
			w.count--
			if !w.closed {
				close(w.changed)
				w.changed = make(chan struct{})
			}
			w.mu.Unlock()
		}
	})
	w.wg.Go(func() {
		_ = send(ctx)
		for {
			select {
			case <-ctx.Done():
				return
			case <-w.prepared:
				if ctx.Err() == nil {
					_ = send(ctx)
				}
				return
			case <-w.wake:
				if ctx.Err() == nil {
					_ = send(ctx)
				}
			}
		}
	})
	return w
}

// enqueue transfers ownership only on success. The producer must clear rejected
// snapshots; accepted snapshots are cleared before their budget is released.
func (w *BackupRenditionWorker) enqueue(source projection.File, snapshot []byte) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.enqueueLocked(source, snapshot)
}

func (w *BackupRenditionWorker) enqueueLocked(source projection.File, snapshot []byte) bool {
	if w.closed || w.ctx.Err() != nil || source.ChannelID != w.channelID || source.MsgID <= 0 || len(snapshot) == 0 || w.count >= 2 || len(snapshot) > backupRenditionSnapshotBytes-w.bytes {
		return false
	}
	w.bytes += len(snapshot)
	w.count++
	w.pending <- backupRenditionJob{source: source, snapshot: snapshot}
	return true
}

type backupRenditionReservation struct {
	worker   *BackupRenditionWorker
	bytes    int
	released bool // guarded by worker.mu
}

// reserve applies preparation backpressure before any original upload begins.
// The receipt path only performs a nonblocking ownership handoff, so preview
// congestion cannot delay the caller's durable original receipt checkpoint.
func (w *BackupRenditionWorker) reserve(ctx context.Context, channelID int64, size int) (*backupRenditionReservation, error) {
	for {
		w.mu.Lock()
		if err := ctx.Err(); err != nil {
			w.mu.Unlock()
			return nil, err
		}
		if w.closed || w.ctx.Err() != nil {
			w.mu.Unlock()
			return nil, context.Canceled
		}
		if channelID != w.channelID || size <= 0 || size > backupRenditionSnapshotBytes {
			w.mu.Unlock()
			return nil, fmt.Errorf("invalid backup preview reservation")
		}
		if w.count < 2 && size <= backupRenditionSnapshotBytes-w.bytes {
			w.count++
			w.bytes += size
			w.mu.Unlock()
			return &backupRenditionReservation{worker: w, bytes: size}, nil
		}
		changed := w.changed
		w.mu.Unlock()
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-w.ctx.Done():
			return nil, w.ctx.Err()
		case <-changed:
		}
	}
}

func (r *backupRenditionReservation) handoff(source projection.File, snapshot []byte) bool {
	w := r.worker
	w.mu.Lock()
	defer w.mu.Unlock()
	if r.released || w.closed || w.ctx.Err() != nil || source.ChannelID != w.channelID || source.MsgID <= 0 || len(snapshot) != r.bytes {
		return false
	}
	w.pending <- backupRenditionJob{source: source, snapshot: snapshot}
	r.released = true // the queued job now owns the reservation and plaintext
	return true
}

func (r *backupRenditionReservation) release() {
	w := r.worker
	w.mu.Lock()
	defer w.mu.Unlock()
	if r.released {
		return
	}
	r.released = true
	w.bytes -= r.bytes
	w.count--
	if !w.closed {
		close(w.changed)
		w.changed = make(chan struct{})
	}
}

// Wait stops admission and drains prepared work on natural completion. Parent
// cancellation interrupts sends and skips/clears remaining plaintext jobs.
func (w *BackupRenditionWorker) Wait() {
	w.mu.Lock()
	if !w.closed {
		w.closed = true
		close(w.pending)
		close(w.changed)
	}
	w.mu.Unlock()
	w.wg.Wait()
}

// Close cancels and joins both workers. Durable ciphertext remains in the
// outbox for the next run; no source file or native staging lease is retained.
func (w *BackupRenditionWorker) Close() {
	w.cancel()
	w.Wait()
}
