package mountdav

import (
	"context"
	"io"
	"log/slog"
	"os"
	"sync"
	"time"
)

const (
	defaultPendingCreateGrace = 3 * time.Second
	defaultPendingCreatePoll  = 250 * time.Millisecond
)

// pendingCreateEntry tracks one deferred empty-file commit.
type pendingCreateEntry struct {
	armedAt    time.Time
	superseded bool
	commit     func(ctx context.Context)
}

// pendingCreateStore briefly defers committing an empty (zero-byte) PUT that
// creates a brand-new path, instead of committing it immediately.
//
// macOS's WebDAV client routinely creates such an empty placeholder before
// writing the real content as a follow-up PUT under a lock -- this is normal,
// harmless client behavior, not a bug (confirmed by direct reproduction: a
// real mount_webdav mount against a real writable, encrypted server correctly
// completes this exact two-step sequence, including under tens of seconds of
// added network latency per step). But if that follow-up write is ever lost
// for any reason -- a client hiccup, a transient remote failure -- the empty
// placeholder was already durably committed, and is indistinguishable from a
// genuinely complete, correct small upload.
//
// Deferring the commit closes that gap: if a write to the same path arrives
// before the grace period elapses (the normal case), the empty create is
// superseded and never committed at all -- only the real content ever
// becomes visible. Only a path that is genuinely never touched again gets
// its empty file committed, once the grace window elapses, preserving a
// deliberate empty-file creation (e.g. touch(1) through the mount).
type pendingCreateStore struct {
	mu      sync.Mutex
	entries map[string]*pendingCreateEntry
	grace   time.Duration
	now     func() time.Time

	stopOnce sync.Once
	stop     chan struct{}
	done     chan struct{}
}

// newPendingCreateStore starts the background reaper immediately, so now
// (the store's clock) must be supplied here rather than mutated afterward --
// there is no race-free way to swap it once the reaper goroutine is running.
// A nil now defaults to time.Now.
func newPendingCreateStore(grace, pollInterval time.Duration, now func() time.Time) *pendingCreateStore {
	if grace <= 0 {
		grace = defaultPendingCreateGrace
	}
	if pollInterval <= 0 {
		pollInterval = defaultPendingCreatePoll
	}
	if now == nil {
		now = time.Now
	}
	store := &pendingCreateStore{
		entries: make(map[string]*pendingCreateEntry),
		grace:   grace,
		now:     now,
		stop:    make(chan struct{}),
		done:    make(chan struct{}),
	}
	go store.run(pollInterval)
	return store
}

func (s *pendingCreateStore) run(pollInterval time.Duration) {
	defer close(s.done)
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-s.stop:
			return
		case <-ticker.C:
			s.reapDue(context.Background())
		}
	}
}

// arm defers commit until grace elapses unsuperseded. Any existing pending
// entry for path is superseded and replaced (a second empty PUT to the same
// still-uncommitted path simply extends the grace window).
func (s *pendingCreateStore) arm(path string, commit func(ctx context.Context)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if existing, ok := s.entries[path]; ok {
		existing.superseded = true
		slog.Debug("mountdav: pending empty create re-armed, extending grace window", "path", path)
	} else {
		slog.Debug("mountdav: pending empty create armed", "path", path, "grace", s.grace)
	}
	s.entries[path] = &pendingCreateEntry{armedAt: s.now(), commit: commit}
}

// supersede cancels any pending empty create for path so it is never
// committed. Callers use this before any other mutation of the same path
// proceeds -- a real write, a delete, or a directory create all represent
// fresher intent than a not-yet-committed empty placeholder.
func (s *pendingCreateStore) supersede(path string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if existing, ok := s.entries[path]; ok {
		existing.superseded = true
		slog.Debug("mountdav: pending empty create superseded, will never commit", "path", path)
	}
	delete(s.entries, path)
}

// reapDue commits every pending entry whose grace period has elapsed and
// that has not been superseded, then removes it. Exposed directly (not only
// reachable via the background ticker) so tests can drive it deterministically
// with an injected clock instead of waiting on a real timer.
func (s *pendingCreateStore) reapDue(ctx context.Context) {
	s.mu.Lock()
	now := s.now()
	type duePath struct {
		path  string
		entry *pendingCreateEntry
	}
	due := make([]duePath, 0)
	for path, entry := range s.entries {
		if entry.superseded {
			delete(s.entries, path)
			continue
		}
		if now.Sub(entry.armedAt) >= s.grace {
			due = append(due, duePath{path: path, entry: entry})
			delete(s.entries, path)
		}
	}
	s.mu.Unlock()
	for _, item := range due {
		slog.Info("mountdav: grace period elapsed with no follow-up write, committing empty file", "path", item.path)
		item.entry.commit(ctx)
	}
}

// Close stops the background reaper and waits for it to exit. Any entries
// still pending, not yet due, are discarded without committing -- matching
// process-restart behavior, since this in-memory state cannot survive one
// either way. Safe to call once, from Server.Stop.
func (s *pendingCreateStore) Close() {
	s.stopOnce.Do(func() { close(s.stop) })
	<-s.done
}

// tryDeferEmptyCreate briefly holds an empty (zero-byte) create for a
// currently-nonexistent path instead of committing it immediately. Reports
// whether it deferred the write. If the path already exists, this is an
// ordinary truncating overwrite, not a new-file placeholder, and must
// proceed immediately like any other PUT.
func (application *readApplication) tryDeferEmptyCreate(ctx context.Context, path string) bool {
	if application.pendingCreates == nil {
		return false
	}
	if _, err := application.fs.Stat(ctx, path); err == nil {
		return false
	} else if !os.IsNotExist(err) {
		// Stat failed for some other reason (e.g. context deadline): do not
		// risk masking a real error behind a deferred, best-effort write.
		return false
	}
	slog.Debug("mountdav: deferring empty create for a new path", "path", path)
	application.pendingCreates.arm(path, func(commitCtx context.Context) {
		application.commitDeferredEmptyCreate(commitCtx, path)
	})
	return true
}

// commitDeferredEmptyCreate performs the actual empty-file commit once the
// grace period has elapsed unsuperseded. It re-checks existence immediately
// before committing: a request this store has no visibility into (a
// concurrent write racing the original, unlocked empty PUT) could have
// created the path in the meantime, and this must never overwrite that with
// empty content.
func (application *readApplication) commitDeferredEmptyCreate(ctx context.Context, path string) {
	if _, err := application.fs.Stat(ctx, path); err == nil {
		slog.Debug("mountdav: deferred empty create skipped, path already exists from an out-of-band write", "path", path)
		return
	}
	operationID, err := randomOperationID()
	if err != nil {
		slog.Error("mountdav: deferred empty create failed to generate operation id", "path", path, "error", err)
		return
	}
	_, err = application.writer.Put(ctx, PutRequest{
		OperationID:   operationID,
		Path:          path,
		ContentLength: 0,
	}, io.LimitReader(nil, 0))
	if err != nil {
		slog.Warn("mountdav: deferred empty create commit failed", "path", path, "error", err)
		return
	}
	slog.Debug("mountdav: deferred empty create committed", "path", path)
}
