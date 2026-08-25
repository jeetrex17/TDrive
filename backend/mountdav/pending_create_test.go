package mountdav

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// newTestPendingCreateStore builds a pendingCreateStore whose background
// ticker is deliberately too slow to ever fire during a test (an hour), so
// only the test's own direct reapDue calls -- driven by an injected,
// test-controlled clock -- exercise the reap logic. This keeps the tests
// fast and deterministic instead of waiting on real timers.
func newTestPendingCreateStore(t *testing.T, grace time.Duration, now func() time.Time) *pendingCreateStore {
	t.Helper()
	store := newPendingCreateStore(grace, time.Hour, now)
	t.Cleanup(store.Close)
	return store
}

func newWritableTestHandlerWithPendingCreates(t *testing.T, writer WriteCoordinator, pendingCreates *pendingCreateStore) http.Handler {
	t.Helper()
	locks := newBoundedLockSystem(defaultMaxActiveLocks)
	application := &readApplication{
		capabilityPath: testCapability,
		fs:             testFS(t, nil),
		lockSystem:     locks,
		writer:         writer,
		authority:      "127.0.0.1:7331",
		resume:         newTestResumeStore(t),
		pendingCreates: pendingCreates,
	}
	return newProtectedHandler(protectionConfig{
		capabilityPath:     testCapability,
		authority:          "127.0.0.1:7331",
		maxConcurrent:      8,
		maxConcurrentWrite: 2,
		writable:           true,
	}, application)
}

func putEmpty(t *testing.T, handler http.Handler, target string) int {
	t.Helper()
	request := trustedRequest(http.MethodPut, testCapability+target, nil)
	request.ContentLength = 0
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	return recorder.Code
}

// TestServePUTDefersEmptyCreateOfNewPath is the core behavior: an empty PUT
// creating a brand-new path is accepted (matching what the client expects --
// see TestPUTResumeContinuationCannotUndoAnEarlierAcceptedPUT's sibling
// finding that macOS routinely does exactly this before writing real
// content) but not yet committed to the coordinator.
func TestServePUTDefersEmptyCreateOfNewPath(t *testing.T) {
	writer := &recordingWriteCoordinator{putResult: MutationResult{Created: true}}
	pendingCreates := newTestPendingCreateStore(t, time.Minute, time.Now)
	handler := newWritableTestHandlerWithPendingCreates(t, writer, pendingCreates)

	if code := putEmpty(t, handler, "/Docs/photo.png"); code != http.StatusCreated {
		t.Fatalf("empty PUT status = %d, want 201", code)
	}
	if writer.putRequest.Path != "" {
		t.Fatalf("coordinator was invoked immediately for a deferred empty create: %q", writer.putRequest.Path)
	}
}

// TestServePUTRealContentSupersedesDeferredEmptyCreate proves the actual
// fix: when real content follows the empty placeholder before the grace
// period elapses -- the normal, working case this session already
// reproduced against a real macOS mount -- only the real content is ever
// committed. The empty placeholder never becomes visible at all.
func TestServePUTRealContentSupersedesDeferredEmptyCreate(t *testing.T) {
	writer := &recordingWriteCoordinator{putResult: MutationResult{Created: true}}
	pendingCreates := newTestPendingCreateStore(t, time.Minute, time.Now)
	handler := newWritableTestHandlerWithPendingCreates(t, writer, pendingCreates)

	if code := putEmpty(t, handler, "/Docs/photo.png"); code != http.StatusCreated {
		t.Fatalf("empty PUT status = %d, want 201", code)
	}

	request := trustedRequest(http.MethodPut, testCapability+"/Docs/photo.png", strings.NewReader("real content"))
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusCreated {
		t.Fatalf("real PUT status = %d, want 201", recorder.Code)
	}
	if string(writer.putBody) != "real content" {
		t.Fatalf("coordinator received %q, want the real content", writer.putBody)
	}

	// Even if the grace period elapses later, the superseded empty create
	// must never separately commit and stomp the real content.
	pendingCreates.reapDue(context.Background())
	if string(writer.putBody) != "real content" {
		t.Fatalf("coordinator body after reap = %q, want the real content to still stand unchanged", writer.putBody)
	}
	if writer.putRequest.ContentLength != int64(len("real content")) {
		t.Fatalf("coordinator putRequest.ContentLength = %d, want %d: the empty create must not have re-committed after being superseded", writer.putRequest.ContentLength, len("real content"))
	}
}

// TestServePUTCommitsDeferredEmptyCreateWhenNothingFollows preserves a
// deliberate empty-file creation (e.g. touch(1) through the mount, or a
// genuinely abandoned macOS placeholder): once the grace period elapses
// with nothing superseding it, the empty file commits for real.
func TestServePUTCommitsDeferredEmptyCreateWhenNothingFollows(t *testing.T) {
	writer := &recordingWriteCoordinator{putResult: MutationResult{Created: true}}
	now := time.Now()
	clock := func() time.Time { return now }
	pendingCreates := newTestPendingCreateStore(t, 3*time.Second, clock)
	handler := newWritableTestHandlerWithPendingCreates(t, writer, pendingCreates)

	if code := putEmpty(t, handler, "/Docs/photo.png"); code != http.StatusCreated {
		t.Fatalf("empty PUT status = %d, want 201", code)
	}
	if writer.putRequest.Path != "" {
		t.Fatalf("coordinator invoked before grace elapsed: %q", writer.putRequest.Path)
	}

	pendingCreates.reapDue(context.Background()) // before grace: still nothing
	if writer.putRequest.Path != "" {
		t.Fatalf("coordinator invoked before grace elapsed on early reap: %q", writer.putRequest.Path)
	}

	now = now.Add(3 * time.Second)
	pendingCreates.reapDue(context.Background())
	if writer.putRequest.Path != "/Docs/photo.png" || writer.putRequest.ContentLength != 0 {
		t.Fatalf("after grace elapsed, coordinator putRequest = %+v, want the empty create committed", writer.putRequest)
	}
}

// TestServePUTDoesNotDeferEmptyOverwriteOfExistingFile ensures the deferral
// is scoped to brand-new paths only: an empty PUT to a path that already
// exists is an ordinary truncating overwrite and must proceed immediately,
// exactly as it always has.
func TestServePUTDoesNotDeferEmptyOverwriteOfExistingFile(t *testing.T) {
	writer := &recordingWriteCoordinator{putResult: MutationResult{Created: false}}
	pendingCreates := newTestPendingCreateStore(t, time.Minute, time.Now)
	fs := testFSWithSeeds(t, []byte("original"), "existing.txt")
	locks := newBoundedLockSystem(defaultMaxActiveLocks)
	application := &readApplication{
		capabilityPath: testCapability,
		fs:             fs,
		lockSystem:     locks,
		writer:         writer,
		authority:      "127.0.0.1:7331",
		resume:         newTestResumeStore(t),
		pendingCreates: pendingCreates,
	}
	handler := newProtectedHandler(protectionConfig{
		capabilityPath:     testCapability,
		authority:          "127.0.0.1:7331",
		maxConcurrent:      8,
		maxConcurrentWrite: 2,
		writable:           true,
	}, application)

	if code := putEmpty(t, handler, "/Docs/existing.txt"); code != http.StatusNoContent {
		t.Fatalf("empty overwrite status = %d, want 204", code)
	}
	if writer.putRequest.Path != "/Docs/existing.txt" {
		t.Fatalf("coordinator was not invoked immediately for an existing-file overwrite: %+v", writer.putRequest)
	}
}

// TestServePUTDeferredEmptyCreateSkipsIfSupersededByOutOfBandCreate covers
// the residual safety net: if the path somehow already exists by the time
// the deferred commit actually runs (a concurrent, unlocked write this store
// had no direct signal about), the deferred commit must re-check and skip
// rather than stomp whatever is there with empty content.
func TestServePUTDeferredEmptyCreateSkipsIfSupersededByOutOfBandCreate(t *testing.T) {
	writer := &recordingWriteCoordinator{putResult: MutationResult{Created: true}}
	now := time.Now()
	clock := func() time.Time { return now }
	pendingCreates := newTestPendingCreateStore(t, 3*time.Second, clock)
	fs := testFS(t, nil)
	locks := newBoundedLockSystem(defaultMaxActiveLocks)
	application := &readApplication{
		capabilityPath: testCapability,
		fs:             fs,
		lockSystem:     locks,
		writer:         writer,
		authority:      "127.0.0.1:7331",
		resume:         newTestResumeStore(t),
		pendingCreates: pendingCreates,
	}
	handler := newProtectedHandler(protectionConfig{
		capabilityPath:     testCapability,
		authority:          "127.0.0.1:7331",
		maxConcurrent:      8,
		maxConcurrentWrite: 2,
		writable:           true,
	}, application)

	if code := putEmpty(t, handler, "/Docs/photo.png"); code != http.StatusCreated {
		t.Fatalf("empty PUT status = %d, want 201", code)
	}

	// Swap in a filesystem that now reports the path as existing, simulating
	// an out-of-band create this store was never told about superseding it.
	application.fs = testFSWithSeeds(t, []byte("real"), "photo.png")

	now = now.Add(3 * time.Second)
	pendingCreates.reapDue(context.Background())
	if writer.putRequest.Path != "" {
		t.Fatalf("coordinator was invoked despite the path already existing: %+v", writer.putRequest)
	}
}

// TestServePUTDeleteSupersedesDeferredEmptyCreate ensures a DELETE of the
// same path cancels a still-pending empty create so it cannot resurrect the
// file the client just explicitly removed.
func TestServePUTDeleteSupersedesDeferredEmptyCreate(t *testing.T) {
	writer := &recordingWriteCoordinator{}
	pendingCreates := newTestPendingCreateStore(t, time.Minute, time.Now)
	handler := newWritableTestHandlerWithPendingCreates(t, writer, pendingCreates)

	if code := putEmpty(t, handler, "/Docs/photo.png"); code != http.StatusCreated {
		t.Fatalf("empty PUT status = %d, want 201", code)
	}

	request := trustedRequest(http.MethodDelete, testCapability+"/Docs/photo.png", nil)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("DELETE status = %d, want 204 (the fake coordinator's default success)", recorder.Code)
	}

	pendingCreates.reapDue(context.Background())
	if writer.putRequest.Path != "" {
		t.Fatalf("deferred empty create resurrected after DELETE superseded it: %+v", writer.putRequest)
	}
}
