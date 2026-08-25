package mountdav

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// blockingReader signals started once Read is first called, then blocks
// until release is closed, letting a test pin a goroutine's io.Copy exactly
// mid-chunk so a second, conflicting claim can race it deterministically.
type blockingReader struct {
	data     []byte
	started  chan struct{}
	release  chan struct{}
	signaled bool
}

func (r *blockingReader) Read(buffer []byte) (int, error) {
	if !r.signaled {
		r.signaled = true
		close(r.started)
		<-r.release
	}
	if len(r.data) == 0 {
		return 0, io.EOF
	}
	n := copy(buffer, r.data)
	r.data = r.data[n:]
	return n, nil
}

type sliceReader struct{ data []byte }

func (r *sliceReader) Read(buffer []byte) (int, error) {
	if len(r.data) == 0 {
		return 0, io.EOF
	}
	n := copy(buffer, r.data)
	r.data = r.data[n:]
	return n, nil
}

func noSeedOpenCurrent() (io.ReadCloser, int64, error) { return nil, 0, nil }

// TestResumeStoreConflictingTotalCannotCorruptARacingReplacementEntry proves
// the fix for a logic race in claim/discardLocked: an entry can be discarded
// by a conflicting-total claim for the same path while its own chunk body is
// still being copied outside the store lock (by design, so slow network I/O
// never blocks unrelated paths). When that discarded entry's copy later
// fails and it tries to clean itself up, it must not blindly delete whatever
// now occupies its path's map slot -- a fresh, unrelated, live entry a
// different request legitimately created in the meantime -- or corrupt the
// aggregate quota counter by double-subtracting.
func TestResumeStoreConflictingTotalCannotCorruptARacingReplacementEntry(t *testing.T) {
	store := newTestResumeStore(t)
	path := "/Docs/photo.png"
	tokens := []string{"opaquelocktoken:test"}

	// G1 claims path for a 1000-byte sequence and starts copying its first
	// chunk (100 of 1000 bytes), then blocks mid-Read so the store lock is
	// free and the entry is left busy=true while "in flight."
	g1Reader := &blockingReader{data: make([]byte, 100), started: make(chan struct{}), release: make(chan struct{})}
	type g1Result struct {
		complete bool
		err      error
	}
	resultCh := make(chan g1Result, 1)
	go func() {
		_, complete, err := store.appendResumeChunk(
			path,
			contentRange{start: 0, end: 99, total: 1000},
			tokens,
			noSeedOpenCurrent,
			g1Reader,
		)
		resultCh <- g1Result{complete: complete, err: err}
	}()
	<-g1Reader.started

	// While G1 is still blocked mid-copy, G2 sends a conflicting sequence for
	// the SAME path with a DIFFERENT total (50, not 1000) and completes it
	// in one shot (a single, final, 50-byte chunk of a 50-byte total). This
	// must discard G1's stale entry and install its own live one.
	g2File, g2Complete, g2Err := store.appendResumeChunk(
		path,
		contentRange{start: 0, end: 49, total: 50},
		tokens,
		noSeedOpenCurrent,
		&sliceReader{data: make([]byte, 50)},
	)
	if g2Err != nil {
		t.Fatalf("G2 appendResumeChunk error = %v", g2Err)
	}
	if !g2Complete {
		t.Fatalf("G2 should complete in one shot (its single chunk is the whole 50-byte total)")
	}
	closeAndRemoveResumeFile(g2File)

	// Release G1: its Read now delivers the remaining bytes, io.Copy writes
	// them to entry.file -- but G2's claim already closed and removed that
	// file, so the write must fail and G1 must observe an error.
	close(g1Reader.release)
	g1 := <-resultCh
	if g1.err == nil {
		t.Fatalf("G1 should fail once its file was closed out from under it by G2's conflicting claim, got complete=%v err=nil", g1.complete)
	}

	// The bug this test guards against: G1's cleanup of its own (now-stale)
	// entry must not have touched the store's bookkeeping at all, since by
	// the time G1 got back to the lock, path no longer belonged to it.
	store.mu.Lock()
	_, stillTracked := store.entries[path]
	usedBytes := store.scratch.UsedBytes()
	store.mu.Unlock()
	if stillTracked {
		t.Fatalf("store still tracks %q after G2's sequence completed and was removed on completion; G1's stale cleanup must not have resurrected or left a dangling entry", path)
	}
	if usedBytes != 0 {
		t.Fatalf("usedBytes = %d, want 0: G1's stale discard must not double-subtract quota that G2's own (already-completed-and-removed) entry already accounted for", usedBytes)
	}
}

// TestResumeStoreConflictingTotalDuringFinalChunkFailsCleanly covers the
// same race at the other call site: the entry being finalized (its last
// chunk just finished copying) was itself superseded by a conflicting-total
// claim while that copy was in flight. Completion must fail rather than
// silently deleting whatever now occupies the map slot.
func TestResumeStoreConflictingTotalDuringFinalChunkFailsCleanly(t *testing.T) {
	store := newTestResumeStore(t)
	path := "/Docs/photo.png"
	tokens := []string{"opaquelocktoken:test"}

	g1Reader := &blockingReader{data: make([]byte, 100), started: make(chan struct{}), release: make(chan struct{})}
	type g1Result struct {
		complete bool
		err      error
	}
	resultCh := make(chan g1Result, 1)
	go func() {
		// G1's only chunk is also its final one (end+1 == total == 100).
		_, complete, err := store.appendResumeChunk(
			path,
			contentRange{start: 0, end: 99, total: 100},
			tokens,
			noSeedOpenCurrent,
			g1Reader,
		)
		resultCh <- g1Result{complete: complete, err: err}
	}()
	<-g1Reader.started

	g2File, g2Complete, g2Err := store.appendResumeChunk(
		path,
		contentRange{start: 0, end: 9, total: 500},
		tokens,
		noSeedOpenCurrent,
		&sliceReader{data: make([]byte, 10)},
	)
	if g2Err != nil {
		t.Fatalf("G2 appendResumeChunk error = %v", g2Err)
	}
	if g2Complete {
		t.Fatalf("G2's own sequence (500 total, 10 bytes sent) should not be complete yet")
	}
	if g2File != nil {
		t.Fatalf("an incomplete sequence must not return an assembled file")
	}

	close(g1Reader.release)
	g1 := <-resultCh
	if g1.err == nil {
		t.Fatalf("G1 should fail: its file was closed out from under it by G2's conflicting claim")
	}

	store.mu.Lock()
	live, exists := store.entries[path]
	usedBytes := store.scratch.UsedBytes()
	store.mu.Unlock()
	if !exists {
		t.Fatalf("G2's live, in-progress entry for %q must survive G1's stale cleanup", path)
	}
	if live.total != 500 || live.accumulated != 10 {
		t.Fatalf("live entry = {total:%d accumulated:%d}, want {500 10}: G1's stale cleanup must not have mutated G2's entry", live.total, live.accumulated)
	}
	if usedBytes != 500 {
		t.Fatalf("usedBytes = %d, want 500 (only G2's reservation): G1's stale discard must not double-subtract", usedBytes)
	}

	store.mu.Lock()
	store.discardLocked(path, live)
	store.mu.Unlock()
}

func TestResumeStoreMapsAggregateQuotaAndReleasesCompletedReservation(t *testing.T) {
	store, err := newResumeStore(t.TempDir(), time.Minute, 100, 10)
	if err != nil {
		t.Fatalf("new resume store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	assembled, complete, err := store.appendResumeChunk(
		"/first.bin",
		contentRange{start: 0, end: 0, total: 8},
		nil,
		noSeedOpenCurrent,
		&sliceReader{data: []byte("a")},
	)
	if err != nil || complete || assembled != nil {
		t.Fatalf("first chunk = file %v, complete %v, error %v", assembled, complete, err)
	}
	if got := store.scratch.UsedBytes(); got != 8 {
		t.Fatalf("used bytes = %d, want 8", got)
	}

	_, _, err = store.appendResumeChunk(
		"/second.bin",
		contentRange{start: 0, end: 0, total: 3},
		nil,
		noSeedOpenCurrent,
		&sliceReader{data: []byte("b")},
	)
	if !errors.Is(err, ErrResumeTooLarge) {
		t.Fatalf("second sequence error = %v, want ErrResumeTooLarge", err)
	}
	if got := store.scratch.UsedBytes(); got != 8 {
		t.Fatalf("failed reservation changed used bytes to %d, want 8", got)
	}

	assembled, complete, err = store.appendResumeChunk(
		"/first.bin",
		contentRange{start: 1, end: 7, total: 8},
		nil,
		noSeedOpenCurrent,
		&sliceReader{data: []byte("bcdefgh")},
	)
	if err != nil || !complete || assembled == nil {
		t.Fatalf("final chunk = file %v, complete %v, error %v", assembled, complete, err)
	}
	closeAndRemoveResumeFile(assembled)
	if got := store.scratch.UsedBytes(); got != 0 {
		t.Fatalf("completed sequence retained %d bytes", got)
	}
}

func TestResumeStoreRollsBackQuotaWhenExclusiveCreateFails(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "1.resume"), []byte("collision"), 0o600); err != nil {
		t.Fatalf("seed colliding file: %v", err)
	}
	store, err := newResumeStore(root, time.Minute, 100, 10)
	if err != nil {
		t.Fatalf("new resume store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	_, _, err = store.appendResumeChunk(
		"/file.bin",
		contentRange{start: 0, end: 0, total: 8},
		nil,
		noSeedOpenCurrent,
		&sliceReader{data: []byte("a")},
	)
	if !errors.Is(err, os.ErrExist) {
		t.Fatalf("create collision error = %v, want os.ErrExist", err)
	}
	if got := store.scratch.UsedBytes(); got != 0 {
		t.Fatalf("create failure retained %d reserved bytes", got)
	}
}
