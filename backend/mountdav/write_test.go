package mountdav

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"TDrive/backend/mountfs"
)

type recordingWriteCoordinator struct {
	mu sync.Mutex

	putRequest    PutRequest
	putBody       []byte
	mkdirRequest  MkdirRequest
	moveRequest   MoveRequest
	deleteRequest DeleteRequest

	putResult    MutationResult
	mkdirResult  MutationResult
	moveResult   MutationResult
	deleteResult MutationResult
	err          error
}

func (writer *recordingWriteCoordinator) Put(ctx context.Context, request PutRequest, body io.Reader) (MutationResult, error) {
	data, err := io.ReadAll(body)
	if err != nil {
		return MutationResult{}, err
	}
	writer.mu.Lock()
	defer writer.mu.Unlock()
	writer.putRequest = request
	writer.putBody = bytes.Clone(data)
	return writer.putResult, writer.err
}

func (writer *recordingWriteCoordinator) Mkdir(_ context.Context, request MkdirRequest) (MutationResult, error) {
	writer.mu.Lock()
	defer writer.mu.Unlock()
	writer.mkdirRequest = request
	return writer.mkdirResult, writer.err
}

func (writer *recordingWriteCoordinator) Move(_ context.Context, request MoveRequest) (MutationResult, error) {
	writer.mu.Lock()
	defer writer.mu.Unlock()
	writer.moveRequest = request
	return writer.moveResult, writer.err
}

func (writer *recordingWriteCoordinator) Delete(_ context.Context, request DeleteRequest) (MutationResult, error) {
	writer.mu.Lock()
	defer writer.mu.Unlock()
	writer.deleteRequest = request
	return writer.deleteResult, writer.err
}

func newWritableTestHandler(t *testing.T, writer WriteCoordinator) http.Handler {
	t.Helper()
	return newWritableTestHandlerWithResume(t, writer, newTestResumeStore(t))
}

func newWritableTestHandlerWithResume(t *testing.T, writer WriteCoordinator, resume *resumeStore) http.Handler {
	t.Helper()
	return newWritableTestHandlerFull(t, writer, resume, testFS(t, nil))
}

func newWritableTestHandlerFull(t *testing.T, writer WriteCoordinator, resume *resumeStore, fs *FileSystem) http.Handler {
	t.Helper()
	locks := newBoundedLockSystem(defaultMaxActiveLocks)
	application := &readApplication{
		capabilityPath: testCapability,
		fs:             fs,
		lockSystem:     locks,
		writer:         writer,
		authority:      "127.0.0.1:7331",
		resume:         resume,
	}
	return newProtectedHandler(protectionConfig{
		capabilityPath:     testCapability,
		authority:          "127.0.0.1:7331",
		maxConcurrent:      8,
		maxConcurrentWrite: 2,
		writable:           true,
	}, application)
}

// testFSWithSeeds builds a *FileSystem whose /Docs directory contains one
// file per name in names, all sharing content. Resume tests use this to make
// application.fs agree with what the test's fake recordingWriteCoordinator
// claims an earlier plain PUT already committed -- recordingWriteCoordinator
// only records requests, it never actually writes through to fs, so fs must
// be seeded to match by hand.
func testFSWithSeeds(t *testing.T, content []byte, names ...string) *FileSystem {
	t.Helper()
	modTime := time.Unix(1700000000, 0)
	docs := make([]mountfs.SourceEntry, len(names))
	for index, name := range names {
		docs[index] = mountfs.SourceEntry{
			ID:         "f:seed:" + name,
			ParentID:   "d:docs",
			Name:       name,
			Kind:       mountfs.KindFile,
			Size:       int64(len(content)),
			ModTime:    modTime,
			ContentRef: "seed",
		}
	}
	mfs, err := mountfs.New(42, memorySource{
		mountfs.RootID: {
			{ID: "d:docs", ParentID: mountfs.RootID, Name: "Docs", Kind: mountfs.KindDirectory, ModTime: modTime},
		},
		"d:docs": docs,
	}, &threadSafeSeedOpener{data: content})
	if err != nil {
		t.Fatalf("mountfs.New: %v", err)
	}
	return NewFileSystem(mfs)
}

// threadSafeSeedOpener backs testFSWithSeeds. It's a separate type from
// filesystem_test.go's memoryOpener, rather than a reuse of it, specifically
// because that opener's plain int call counter (read by other tests'
// assertions) is not safe for the concurrent OpenFile calls this file's
// resume-sequence concurrency test performs against one shared *FileSystem;
// this keeps that fix scoped to resume tests instead of touching a helper
// several unrelated test files depend on. memoryContent is read-only once
// constructed, so sharing it across goroutines here is race-free.
type threadSafeSeedOpener struct {
	data []byte
}

func (o *threadSafeSeedOpener) OpenContent(context.Context, int64, mountfs.SourceEntry) (mountfs.RandomAccessContent, error) {
	return &memoryContent{data: o.data}, nil
}

// newTestResumeStore creates a resumeStore rooted in t.TempDir, closed
// automatically via t.Cleanup. now defaults to a fixed, injectable clock
// (see newTestResumeStoreWithClock) so idle-timeout tests do not need to
// sleep out a production-length timeout.
func newTestResumeStore(t *testing.T) *resumeStore {
	t.Helper()
	store, err := newResumeStore(t.TempDir(), defaultResumeIdleTimeout, defaultMaxResumeObjectBytes, defaultMaxResumeAggregateBytes)
	if err != nil {
		t.Fatalf("newResumeStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func newTestResumeStoreWithClock(t *testing.T, idleTimeout time.Duration, now func() time.Time) *resumeStore {
	t.Helper()
	store, err := newResumeStore(t.TempDir(), idleTimeout, defaultMaxResumeObjectBytes, defaultMaxResumeAggregateBytes)
	if err != nil {
		t.Fatalf("newResumeStore: %v", err)
	}
	store.now = now
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func TestWritableProtectedHandlerAdvertisesExplicitSurface(t *testing.T) {
	writer := &recordingWriteCoordinator{}
	handler := newWritableTestHandler(t, writer)

	request := trustedRequest(http.MethodOptions, testCapability+"/", nil)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("OPTIONS status = %d, want 200", recorder.Code)
	}
	if got := recorder.Header().Get("Allow"); got != writableMethodsHeader {
		t.Fatalf("Allow = %q, want %q", got, writableMethodsHeader)
	}
	if got := recorder.Header().Get("DAV"); got != "1, 2" {
		t.Fatalf("DAV = %q, want class 1 and 2", got)
	}
	if strings.Contains(recorder.Header().Get("Allow"), "COPY") {
		t.Fatalf("COPY was advertised before it is implemented: %#v", recorder.Header())
	}
	if !strings.Contains(recorder.Header().Get("Allow"), "PROPPATCH") {
		t.Fatalf("PROPPATCH is required for Windows Explorer uploads: %#v", recorder.Header())
	}
}

func TestWindowsMiniRedirectorAcceptsMetadataPROPPATCH(t *testing.T) {
	writer := &recordingWriteCoordinator{}
	handler := newWritableTestHandler(t, writer)
	lockBody := `<D:lockinfo xmlns:D="DAV:"><D:lockscope><D:exclusive/></D:lockscope><D:locktype><D:write/></D:locktype></D:lockinfo>`
	lock := trustedRequest("LOCK", testCapability+"/Docs/note.txt", strings.NewReader(lockBody))
	lock.Header.Set("Depth", "0")
	lock.Header.Set("User-Agent", "Microsoft-WebDAV-MiniRedir/10.0.26100")
	lockRecorder := httptest.NewRecorder()
	handler.ServeHTTP(lockRecorder, lock)
	if lockRecorder.Code != http.StatusOK {
		t.Fatalf("LOCK status = %d, body=%q", lockRecorder.Code, lockRecorder.Body.String())
	}
	token := lockRecorder.Header().Get("Lock-Token")
	if token == "" {
		t.Fatal("LOCK omitted token")
	}

	body := `<?xml version="1.0" encoding="utf-8"?>
<D:propertyupdate xmlns:D="DAV:" xmlns:Z="urn:schemas-microsoft-com:">
  <D:set><D:prop>
    <Z:Win32CreationTime>Sun, 14 Sep 2018 12:50:00 GMT</Z:Win32CreationTime>
    <Z:Win32LastModifiedTime>Sun, 14 Sep 2018 12:50:00 GMT</Z:Win32LastModifiedTime>
    <Z:Win32FileAttributes>00000020</Z:Win32FileAttributes>
  </D:prop></D:set>
</D:propertyupdate>`
	request := trustedRequest("PROPPATCH", testCapability+"/Docs/note.txt", strings.NewReader(body))
	request.Header.Set("Content-Type", "text/xml; charset=utf-8")
	request.Header.Set("User-Agent", "Microsoft-WebDAV-MiniRedir/10.0.26100")
	request.Header.Set("If", "("+token+")")
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusMultiStatus {
		t.Fatalf("PROPPATCH status = %d, body=%q, want 207", recorder.Code, recorder.Body.String())
	}
	if !strings.Contains(recorder.Header().Get("Content-Type"), "application/xml") {
		t.Fatalf("PROPPATCH Content-Type = %q", recorder.Header().Get("Content-Type"))
	}
	for _, property := range []string{"Win32CreationTime", "Win32LastModifiedTime", "Win32FileAttributes", "HTTP/1.1 200 OK"} {
		if !strings.Contains(recorder.Body.String(), property) {
			t.Fatalf("PROPPATCH response omitted %q: %s", property, recorder.Body.String())
		}
	}
	if writer.putRequest.Path != "" || writer.deleteRequest.Path != "" {
		t.Fatalf("metadata-only PROPPATCH reached content writer: %+v", writer)
	}
	unlock := trustedRequest("UNLOCK", testCapability+"/Docs/note.txt", nil)
	unlock.Header.Set("Lock-Token", token)
	unlockRecorder := httptest.NewRecorder()
	handler.ServeHTTP(unlockRecorder, unlock)
	if unlockRecorder.Code != http.StatusNoContent {
		t.Fatalf("UNLOCK status = %d, body=%q", unlockRecorder.Code, unlockRecorder.Body.String())
	}
}

func TestWindowsMiniRedirectorAcceptsLockNullMetadataBeforePUT(t *testing.T) {
	writer := &recordingWriteCoordinator{putResult: MutationResult{Created: true, ETag: `"file-7-r1"`}}
	handler := newWritableTestHandler(t, writer)
	lockBody := `<D:lockinfo xmlns:D="DAV:"><D:lockscope><D:exclusive/></D:lockscope><D:locktype><D:write/></D:locktype></D:lockinfo>`
	lock := trustedRequest("LOCK", testCapability+"/Docs/new-upload.jpg", strings.NewReader(lockBody))
	lock.Header.Set("Depth", "0")
	lock.Header.Set("User-Agent", "Microsoft-WebDAV-MiniRedir/10.0.26100")
	lockRecorder := httptest.NewRecorder()
	handler.ServeHTTP(lockRecorder, lock)
	if lockRecorder.Code != http.StatusOK {
		t.Fatalf("LOCK status = %d, body=%q", lockRecorder.Code, lockRecorder.Body.String())
	}
	token := lockRecorder.Header().Get("Lock-Token")
	if token == "" {
		t.Fatal("LOCK omitted token")
	}

	patchBody := `<D:propertyupdate xmlns:D="DAV:" xmlns:Z="urn:schemas-microsoft-com:"><D:set><D:prop><Z:Win32LastModifiedTime>Sun, 14 Sep 2018 12:50:00 GMT</Z:Win32LastModifiedTime></D:prop></D:set></D:propertyupdate>`
	patch := trustedRequest("PROPPATCH", testCapability+"/Docs/new-upload.jpg", strings.NewReader(patchBody))
	patch.Header.Set("Content-Type", "text/xml; charset=utf-8")
	patch.Header.Set("User-Agent", "Microsoft-WebDAV-MiniRedir/10.0.26100")
	patch.Header.Set("If", "("+token+")")
	patchRecorder := httptest.NewRecorder()
	handler.ServeHTTP(patchRecorder, patch)
	if patchRecorder.Code != http.StatusMultiStatus {
		t.Fatalf("lock-null PROPPATCH status = %d, body=%q", patchRecorder.Code, patchRecorder.Body.String())
	}
	if writer.putRequest.Path != "" || writer.deleteRequest.Path != "" {
		t.Fatalf("lock-null PROPPATCH reached content writer: %+v", writer)
	}

	put := trustedRequest(http.MethodPut, testCapability+"/Docs/new-upload.jpg", strings.NewReader("jpeg"))
	put.Header.Set("Content-Type", "image/jpeg")
	put.Header.Set("If", "("+token+")")
	putRecorder := httptest.NewRecorder()
	handler.ServeHTTP(putRecorder, put)
	if putRecorder.Code != http.StatusCreated || writer.putRequest.Path != "/Docs/new-upload.jpg" || string(writer.putBody) != "jpeg" {
		t.Fatalf("PUT after lock-null PROPPATCH = %d, request %+v, body %q", putRecorder.Code, writer.putRequest, writer.putBody)
	}
}

type applicationAwareReader struct {
	applicationEntered *bool
	remaining          int
}

func (reader *applicationAwareReader) Read(buffer []byte) (int, error) {
	if !*reader.applicationEntered {
		return 0, errors.New("body was consumed by protection middleware")
	}
	if reader.remaining == 0 {
		return 0, io.EOF
	}
	n := min(len(buffer), reader.remaining)
	for index := 0; index < n; index++ {
		buffer[index] = 'x'
	}
	reader.remaining -= n
	return n, nil
}

func TestWritableProtectionStreamsPUTButBoundsXML(t *testing.T) {
	entered := false
	reader := &applicationAwareReader{applicationEntered: &entered, remaining: int(maxRequestBodyBytes) + 1}
	next := http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		entered = true
		body, err := io.ReadAll(request.Body)
		if err != nil {
			t.Fatalf("read streamed PUT: %v", err)
		}
		if len(body) != int(maxRequestBodyBytes)+1 {
			t.Fatalf("streamed PUT length = %d", len(body))
		}
		response.WriteHeader(http.StatusCreated)
	})
	handler := newProtectedHandler(protectionConfig{
		capabilityPath:     testCapability,
		authority:          "127.0.0.1:7331",
		writable:           true,
		maxConcurrent:      2,
		maxConcurrentWrite: 1,
	}, next)
	request := trustedRequest(http.MethodPut, testCapability+"/large.bin", reader)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusCreated {
		t.Fatalf("streaming PUT status = %d, body=%q", recorder.Code, recorder.Body.String())
	}

	request = trustedRequest("LOCK", testCapability+"/large.bin", bytes.NewReader(make([]byte, maxRequestBodyBytes+1)))
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized LOCK status = %d, want 413", recorder.Code)
	}
}

func TestWritablePUTPassesValidatedConditionsAndReturnsCommitStatus(t *testing.T) {
	writer := &recordingWriteCoordinator{putResult: MutationResult{Created: true, ETag: `"file-7-r1"`}}
	handler := newWritableTestHandler(t, writer)
	request := trustedRequest(http.MethodPut, testCapability+"/Docs/new.txt", strings.NewReader("payload"))
	request.ContentLength = 7
	request.Header.Set("Content-Type", "text/plain")
	request.Header.Set("If-None-Match", "*")
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusCreated || recorder.Header().Get("ETag") != `"file-7-r1"` {
		t.Fatalf("PUT response = %d/%#v/%q", recorder.Code, recorder.Header(), recorder.Body.String())
	}
	if writer.putRequest.Path != "/Docs/new.txt" || writer.putRequest.ContentLength != 7 || writer.putRequest.ContentType != "text/plain" {
		t.Fatalf("PUT request = %+v", writer.putRequest)
	}
	if !writer.putRequest.Conditions.IfNoneMatch.Present || !writer.putRequest.Conditions.IfNoneMatch.Any {
		t.Fatalf("If-None-Match was not parsed: %+v", writer.putRequest.Conditions)
	}
	if string(writer.putBody) != "payload" {
		t.Fatalf("PUT body = %q", writer.putBody)
	}

	writer.putResult = MutationResult{Created: false, ETag: `"file-7-r2"`}
	request = trustedRequest(http.MethodPut, testCapability+"/Docs/new.txt", strings.NewReader("next"))
	request.Header.Set("If-Match", `"file-7-r1"`)
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusNoContent || recorder.Header().Get("ETag") != `"file-7-r2"` {
		t.Fatalf("replace PUT response = %d/%#v", recorder.Code, recorder.Header())
	}
	if got := writer.putRequest.Conditions.IfMatch.Tags; len(got) != 1 || got[0].Opaque != "file-7-r1" || got[0].Weak {
		t.Fatalf("If-Match tags = %+v", got)
	}
}

func TestWritablePUTRejectsInvalidPreconditionsBeforeWriter(t *testing.T) {
	writer := &recordingWriteCoordinator{}
	handler := newWritableTestHandler(t, writer)
	request := trustedRequest(http.MethodPut, testCapability+"/Docs/new.txt", strings.NewReader("payload"))
	request.Header.Set("If-Match", `not-an-etag`)
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("invalid If-Match status = %d, want 400", recorder.Code)
	}
	if writer.putRequest.Path != "" {
		t.Fatalf("invalid request reached writer: %+v", writer.putRequest)
	}
}

func TestWritableMKCOLMOVEDELETERequests(t *testing.T) {
	writer := &recordingWriteCoordinator{
		mkdirResult:  MutationResult{Created: true},
		moveResult:   MutationResult{Created: false, ETag: `"moved-r2"`},
		deleteResult: MutationResult{},
	}
	handler := newWritableTestHandler(t, writer)

	mkdir := trustedRequest("MKCOL", testCapability+"/Docs/New", nil)
	mkdirRecorder := httptest.NewRecorder()
	handler.ServeHTTP(mkdirRecorder, mkdir)
	if mkdirRecorder.Code != http.StatusCreated || writer.mkdirRequest.Path != "/Docs/New" {
		t.Fatalf("MKCOL = %d, request %+v", mkdirRecorder.Code, writer.mkdirRequest)
	}

	move := trustedRequest("MOVE", testCapability+"/Docs/new.txt", nil)
	move.Header.Set("Destination", "http://127.0.0.1:7331"+testCapability+"/Docs/renamed.txt")
	move.Header.Set("Overwrite", "T")
	move.Header.Set("Depth", "infinity")
	moveRecorder := httptest.NewRecorder()
	handler.ServeHTTP(moveRecorder, move)
	if moveRecorder.Code != http.StatusNoContent || moveRecorder.Header().Get("ETag") != `"moved-r2"` {
		t.Fatalf("MOVE response = %d/%#v/%q", moveRecorder.Code, moveRecorder.Header(), moveRecorder.Body.String())
	}
	if writer.moveRequest.SourcePath != "/Docs/new.txt" || writer.moveRequest.DestinationPath != "/Docs/renamed.txt" || !writer.moveRequest.Overwrite {
		t.Fatalf("MOVE request = %+v", writer.moveRequest)
	}

	remove := trustedRequest(http.MethodDelete, testCapability+"/Docs/renamed.txt", nil)
	remove.Header.Set("Depth", "infinity")
	deleteRecorder := httptest.NewRecorder()
	handler.ServeHTTP(deleteRecorder, remove)
	if deleteRecorder.Code != http.StatusNoContent || writer.deleteRequest.Path != "/Docs/renamed.txt" {
		t.Fatalf("DELETE = %d, request %+v", deleteRecorder.Code, writer.deleteRequest)
	}
}

func TestWindowsMiniRedirectorDeleteIgnoresDepthForFiles(t *testing.T) {
	for _, depth := range []string{"0", "infinity,noroot"} {
		t.Run(depth, func(t *testing.T) {
			writer := &recordingWriteCoordinator{}
			handler := newWritableTestHandler(t, writer)
			request := trustedRequest(http.MethodDelete, testCapability+"/Docs/note.txt", nil)
			request.Header.Set("Depth", depth)
			request.Header.Set("User-Agent", "Microsoft-WebDAV-MiniRedir/10.0.26100")
			recorder := httptest.NewRecorder()

			handler.ServeHTTP(recorder, request)

			if recorder.Code != http.StatusNoContent || writer.deleteRequest.Path != "/Docs/note.txt" {
				t.Fatalf("DELETE Depth %q = %d, request %+v, body=%q", depth, recorder.Code, writer.deleteRequest, recorder.Body.String())
			}
		})
	}
}

func TestWritableMOVEStrictlyValidatesDestination(t *testing.T) {
	for _, test := range []struct {
		name        string
		destination string
		overwrite   string
		status      int
	}{
		{name: "missing", status: http.StatusBadRequest},
		{name: "wrong host", destination: "http://localhost:7331" + testCapability + "/x", status: http.StatusBadGateway},
		{name: "wrong capability", destination: "http://127.0.0.1:7331/wrong/x", status: http.StatusBadGateway},
		{name: "https escalation", destination: "https://127.0.0.1:7331" + testCapability + "/x", status: http.StatusBadGateway},
		{name: "encoded separator", destination: "http://127.0.0.1:7331" + testCapability + "/Docs%2Fx", status: http.StatusBadRequest},
		{name: "bidi override", destination: "http://127.0.0.1:7331" + testCapability + "/Docs/report%E2%80%AEfdp.exe", status: http.StatusBadRequest},
		{name: "invalid overwrite", destination: "http://127.0.0.1:7331" + testCapability + "/x", overwrite: "yes", status: http.StatusBadRequest},
	} {
		t.Run(test.name, func(t *testing.T) {
			writer := &recordingWriteCoordinator{}
			handler := newWritableTestHandler(t, writer)
			request := trustedRequest("MOVE", testCapability+"/Docs/note.txt", nil)
			if test.destination != "" {
				request.Header.Set("Destination", test.destination)
			}
			if test.overwrite != "" {
				request.Header.Set("Overwrite", test.overwrite)
			}
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, request)
			if recorder.Code != test.status {
				t.Fatalf("MOVE status = %d, want %d; body=%q", recorder.Code, test.status, recorder.Body.String())
			}
			if writer.moveRequest.SourcePath != "" {
				t.Fatalf("invalid MOVE reached writer: %+v", writer.moveRequest)
			}
		})
	}
}

// TestServePUTWithoutContentLengthBuffersToDetermineSize documents the fix
// for a real, user-reported bug caught live via the heavy-logging feature:
// macOS Finder's copy engine sends the real-content PUT of its two-step
// create-then-write sequence via chunked Transfer-Encoding (no Content-Length
// header) for at least some writes. Go reports that as ContentLength == -1.
// Session.Put requires a known length for encrypted writes (the TDE1 stream
// header embeds the plaintext size and cannot be back-filled afterward), so
// this used to hard-reject with ErrWriteLengthRequired -- and because the
// pending-empty-create placeholder is superseded the moment any PUT arrives
// for the path, regardless of whether that PUT then succeeds, the net result
// was nothing ever committed at all: a silent, total upload failure.
// servePut now buffers an unknown-length body first to learn its real size,
// so the coordinator always sees a normal, non-negative ContentLength.
func TestServePUTWithoutContentLengthBuffersToDetermineSize(t *testing.T) {
	writer := &recordingWriteCoordinator{putResult: MutationResult{Created: true}}
	handler := newWritableTestHandler(t, writer)

	content := "the real file content, arriving with no Content-Length header"
	request := trustedRequest(http.MethodPut, testCapability+"/Docs/photo.png", strings.NewReader(content))
	request.ContentLength = -1 // simulate what net/http reports for chunked Transfer-Encoding

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body=%q", recorder.Code, recorder.Body.String())
	}
	if writer.putRequest.Path != "/Docs/photo.png" {
		t.Fatalf("coordinator was never invoked: %+v", writer.putRequest)
	}
	if writer.putRequest.ContentLength != int64(len(content)) {
		t.Fatalf("coordinator ContentLength = %d, want %d (the buffered real size, not -1)", writer.putRequest.ContentLength, len(content))
	}
	if string(writer.putBody) != content {
		t.Fatalf("coordinator received %q, want %q", writer.putBody, content)
	}
}

// TestServePUTKnownContentLengthIsNotBuffered proves the fix is scoped to
// the unknown-length case only: a normal PUT that already declares
// Content-Length must stream straight through exactly as before, not take
// the buffering path.
func TestServePUTKnownContentLengthIsNotBuffered(t *testing.T) {
	writer := &recordingWriteCoordinator{putResult: MutationResult{Created: true}}
	handler := newWritableTestHandler(t, writer)

	content := "ordinary content with a normal Content-Length header"
	request := trustedRequest(http.MethodPut, testCapability+"/Docs/note.txt", strings.NewReader(content))

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201", recorder.Code)
	}
	if writer.putRequest.ContentLength != int64(len(content)) {
		t.Fatalf("coordinator ContentLength = %d, want %d", writer.putRequest.ContentLength, len(content))
	}
	if string(writer.putBody) != content {
		t.Fatalf("coordinator received %q, want %q", writer.putBody, content)
	}
}

func TestWritablePathsUseOnePortableNamespace(t *testing.T) {
	for _, path := range []string{
		"/Docs/" + strings.Repeat("a", maxWritableComponentBytes+1),
		"/Docs/CON.txt",
		"/Docs/COM%C2%B9.txt",
		"/Docs/LPT%C2%B2",
		"/Docs/CLOCK$",
		"/Docs/trailing.%20",
		"/Docs/has%3F.txt",
		"/Docs/control%0A.txt",
		"/Docs/report%E2%80%AEfdp.exe",
	} {
		writer := &recordingWriteCoordinator{}
		handler := newWritableTestHandler(t, writer)
		request := trustedRequest(http.MethodPut, testCapability+path, strings.NewReader("x"))
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusBadRequest {
			t.Errorf("PUT %q status = %d, want 400", path, recorder.Code)
		}
		if writer.putRequest.Path != "" {
			t.Errorf("invalid path %q reached writer", path)
		}
	}

	writer := &recordingWriteCoordinator{putResult: MutationResult{Created: true}}
	handler := newWritableTestHandler(t, writer)
	request := trustedRequest(http.MethodPut, testCapability+"/Docs/Cafe%CC%81.txt", strings.NewReader("x"))
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusCreated || writer.putRequest.Path != "/Docs/Café.txt" {
		t.Fatalf("normalized PUT = %d, path %q", recorder.Code, writer.putRequest.Path)
	}
}

func TestWritableMethodSpecificValidationRunsBeforeCoordinator(t *testing.T) {
	for _, test := range []struct {
		name    string
		method  string
		target  string
		body    string
		headers map[string]string
		status  int
	}{
		{name: "PUT malformed content range", method: http.MethodPut, target: "/Docs/note.txt", headers: map[string]string{"Content-Range": "bytes not-a-range"}, status: http.StatusBadRequest},
		{name: "MKCOL body", method: "MKCOL", target: "/Docs/New", body: "extension", status: http.StatusUnsupportedMediaType},
		{name: "MOVE body", method: "MOVE", target: "/Docs/note.txt", body: "extension", headers: map[string]string{"Destination": testCapability + "/Docs/moved.txt"}, status: http.StatusUnsupportedMediaType},
		{name: "MOVE bad depth", method: "MOVE", target: "/Docs/note.txt", headers: map[string]string{"Destination": testCapability + "/Docs/moved.txt", "Depth": "1"}, status: http.StatusBadRequest},
		{name: "DELETE body", method: http.MethodDelete, target: "/Docs/note.txt", body: "extension", status: http.StatusUnsupportedMediaType},
		{name: "DELETE collection bad depth", method: http.MethodDelete, target: "/Docs", headers: map[string]string{"Depth": "0"}, status: http.StatusBadRequest},
		{name: "root delete", method: http.MethodDelete, target: "/", status: http.StatusForbidden},
		{name: "encoded source separator", method: http.MethodPut, target: "/Docs%2Fnote.txt", status: http.StatusBadRequest},
	} {
		t.Run(test.name, func(t *testing.T) {
			writer := &recordingWriteCoordinator{}
			handler := newWritableTestHandler(t, writer)
			request := trustedRequest(test.method, testCapability+test.target, strings.NewReader(test.body))
			for name, value := range test.headers {
				request.Header.Set(name, value)
			}
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, request)
			if recorder.Code != test.status {
				t.Fatalf("status = %d, want %d; body=%q", recorder.Code, test.status, recorder.Body.String())
			}
			if writer.putRequest.Path != "" || writer.mkdirRequest.Path != "" || writer.moveRequest.SourcePath != "" || writer.deleteRequest.Path != "" {
				t.Fatalf("invalid request reached writer: %+v", writer)
			}
		})
	}
}

// TestPUTResumeContinuationAssemblesAndCommitsTheCompleteFile is the fixed
// counterpart of the real, user-reported bug this documented: macOS's
// built-in WebDAV client (mount_webdav) resumes an interrupted large-file
// upload by sending a follow-up PUT carrying a Content-Range header for the
// same resource, after an initial plain PUT already delivered however many
// bytes it managed to send. servePut used to reject any Content-Range PUT
// outright (see the removed "PUT content range" case that was in
// TestWritableMethodSpecificValidationRunsBeforeCoordinator), leaving
// whatever the first, truncated PUT committed standing as if it were a
// complete file. It now buffers the continuation on disk (resumeStore) and,
// once the final chunk arrives, assembles the full content and commits it
// through the coordinator exactly once -- so the coordinator only ever sees
// the complete, correct file, never the truncated intermediate state.
func TestPUTResumeContinuationAssemblesAndCommitsTheCompleteFile(t *testing.T) {
	writer := &recordingWriteCoordinator{putResult: MutationResult{Created: true}}
	fs := testFSWithSeeds(t, []byte("first-chunk-bytes"), "photo.png")
	handler := newWritableTestHandlerFull(t, writer, newTestResumeStore(t), fs)

	first := trustedRequest(http.MethodPut, testCapability+"/Docs/photo.png", strings.NewReader("first-chunk-bytes"))
	firstRecorder := httptest.NewRecorder()
	handler.ServeHTTP(firstRecorder, first)
	if firstRecorder.Code != http.StatusCreated {
		t.Fatalf("first PUT status = %d, want 201", firstRecorder.Code)
	}
	if string(writer.putBody) != "first-chunk-bytes" {
		t.Fatalf("coordinator received %q, want the first attempt's bytes", writer.putBody)
	}

	second := trustedRequest(http.MethodPut, testCapability+"/Docs/photo.png", strings.NewReader("rest-of-the-file"))
	second.Header.Set("Content-Range", "bytes 17-32/33")
	secondRecorder := httptest.NewRecorder()
	handler.ServeHTTP(secondRecorder, second)
	if secondRecorder.Code != http.StatusCreated {
		t.Fatalf("resume PUT status = %d, want 201, body=%q", secondRecorder.Code, secondRecorder.Body.String())
	}

	if string(writer.putBody) != "first-chunk-bytesrest-of-the-file" {
		t.Fatalf("coordinator received %q, want the full 33-byte assembled content", writer.putBody)
	}
	if writer.putRequest.ContentLength != 33 {
		t.Fatalf("coordinator ContentLength = %d, want 33", writer.putRequest.ContentLength)
	}
}

// TestPUTResumeThreeChunkSequenceAssemblesInOrder covers a sequence longer
// than two requests: a plain PUT, then two Content-Range continuations, only
// the last of which completes and commits.
func TestPUTResumeThreeChunkSequenceAssemblesInOrder(t *testing.T) {
	writer := &recordingWriteCoordinator{putResult: MutationResult{Created: true}}
	resume := newTestResumeStore(t)
	fs := testFSWithSeeds(t, []byte("AAAA"), "movie.mov")
	handler := newWritableTestHandlerFull(t, writer, resume, fs)

	first := trustedRequest(http.MethodPut, testCapability+"/Docs/movie.mov", strings.NewReader("AAAA"))
	firstRecorder := httptest.NewRecorder()
	handler.ServeHTTP(firstRecorder, first)
	if firstRecorder.Code != http.StatusCreated {
		t.Fatalf("first PUT status = %d", firstRecorder.Code)
	}

	second := trustedRequest(http.MethodPut, testCapability+"/Docs/movie.mov", strings.NewReader("BBBB"))
	second.Header.Set("Content-Range", "bytes 4-7/12")
	secondRecorder := httptest.NewRecorder()
	handler.ServeHTTP(secondRecorder, second)
	if secondRecorder.Code != http.StatusNoContent {
		t.Fatalf("second (non-final) chunk status = %d, want 204, body=%q", secondRecorder.Code, secondRecorder.Body.String())
	}
	if writer.putRequest.Path != "/Docs/movie.mov" || len(writer.putBody) != 4 {
		t.Fatalf("coordinator was invoked for a non-final chunk: %+v body=%q", writer.putRequest, writer.putBody)
	}

	third := trustedRequest(http.MethodPut, testCapability+"/Docs/movie.mov", strings.NewReader("CCCC"))
	third.Header.Set("Content-Range", "bytes 8-11/12")
	thirdRecorder := httptest.NewRecorder()
	handler.ServeHTTP(thirdRecorder, third)
	if thirdRecorder.Code != http.StatusCreated {
		t.Fatalf("final chunk status = %d, want 201, body=%q", thirdRecorder.Code, thirdRecorder.Body.String())
	}
	if string(writer.putBody) != "AAAABBBBCCCC" {
		t.Fatalf("coordinator received %q, want the full 12-byte assembled content", writer.putBody)
	}
}

// TestPUTResumeOffsetMismatchIsRejectedWithoutCorruptingTheBuffer covers a
// continuation whose Content-Range start does not line up with what has
// actually been accumulated: it must be rejected cleanly, and a subsequent,
// correctly-aligned chunk must still be able to complete the original
// sequence -- proving the mismatched request left the buffer untouched.
func TestPUTResumeOffsetMismatchIsRejectedWithoutCorruptingTheBuffer(t *testing.T) {
	writer := &recordingWriteCoordinator{putResult: MutationResult{Created: true}}
	resume := newTestResumeStore(t)
	fs := testFSWithSeeds(t, []byte("AAAA"), "movie.mov")
	handler := newWritableTestHandlerFull(t, writer, resume, fs)

	first := trustedRequest(http.MethodPut, testCapability+"/Docs/movie.mov", strings.NewReader("AAAA"))
	firstRecorder := httptest.NewRecorder()
	handler.ServeHTTP(firstRecorder, first)
	if firstRecorder.Code != http.StatusCreated {
		t.Fatalf("first PUT status = %d", firstRecorder.Code)
	}

	wrong := trustedRequest(http.MethodPut, testCapability+"/Docs/movie.mov", strings.NewReader("ZZZZ"))
	wrong.Header.Set("Content-Range", "bytes 5-8/12")
	wrongRecorder := httptest.NewRecorder()
	handler.ServeHTTP(wrongRecorder, wrong)
	if wrongRecorder.Code != http.StatusConflict {
		t.Fatalf("misaligned chunk status = %d, want 409, body=%q", wrongRecorder.Code, wrongRecorder.Body.String())
	}
	if writer.putRequest.Path == "/Docs/movie.mov" && len(writer.putBody) != 4 {
		t.Fatalf("misaligned chunk reached the coordinator: %+v", writer.putRequest)
	}

	correct := trustedRequest(http.MethodPut, testCapability+"/Docs/movie.mov", strings.NewReader("BBBBBBBB"))
	correct.Header.Set("Content-Range", "bytes 4-11/12")
	correctRecorder := httptest.NewRecorder()
	handler.ServeHTTP(correctRecorder, correct)
	if correctRecorder.Code != http.StatusCreated {
		t.Fatalf("correctly-aligned final chunk status = %d, want 201, body=%q", correctRecorder.Code, correctRecorder.Body.String())
	}
	if string(writer.putBody) != "AAAABBBBBBBB" {
		t.Fatalf("coordinator received %q, want the correctly assembled content unaffected by the rejected chunk", writer.putBody)
	}
}

// TestPUTResumeChangedTotalDiscardsTheStaleSequence covers a client that
// abandons a resume sequence and starts a new one (declaring a different
// TOTAL) for the same path: the stale buffer must be discarded rather than
// corrupting the new sequence.
func TestPUTResumeChangedTotalDiscardsTheStaleSequence(t *testing.T) {
	writer := &recordingWriteCoordinator{putResult: MutationResult{Created: true}}
	resume := newTestResumeStore(t)
	fs := testFSWithSeeds(t, []byte("AAAA"), "movie.mov")
	handler := newWritableTestHandlerFull(t, writer, resume, fs)

	first := trustedRequest(http.MethodPut, testCapability+"/Docs/movie.mov", strings.NewReader("AAAA"))
	firstRecorder := httptest.NewRecorder()
	handler.ServeHTTP(firstRecorder, first)
	if firstRecorder.Code != http.StatusCreated {
		t.Fatalf("first PUT status = %d", firstRecorder.Code)
	}

	stale := trustedRequest(http.MethodPut, testCapability+"/Docs/movie.mov", strings.NewReader("BBBB"))
	stale.Header.Set("Content-Range", "bytes 4-7/100")
	staleRecorder := httptest.NewRecorder()
	handler.ServeHTTP(staleRecorder, stale)
	if staleRecorder.Code != http.StatusNoContent {
		t.Fatalf("stale-sequence chunk status = %d, want 204, body=%q", staleRecorder.Code, staleRecorder.Body.String())
	}

	restarted := trustedRequest(http.MethodPut, testCapability+"/Docs/movie.mov", strings.NewReader("CCCC"))
	restarted.Header.Set("Content-Range", "bytes 4-7/8")
	restartedRecorder := httptest.NewRecorder()
	handler.ServeHTTP(restartedRecorder, restarted)
	if restartedRecorder.Code != http.StatusCreated {
		t.Fatalf("restarted sequence final chunk status = %d, want 201, body=%q", restartedRecorder.Code, restartedRecorder.Body.String())
	}
	if string(writer.putBody) != "AAAACCCC" {
		t.Fatalf("coordinator received %q, want the restarted (total=8) sequence, not a mix with the abandoned total=100 one", writer.putBody)
	}
}

// TestPUTResumeAbandonedSequenceIsReaped covers cleanup of a resume sequence
// whose final chunk never arrives: it must not hold its temp file and quota
// reservation forever. Uses an injectable clock instead of a real sleep.
func TestPUTResumeAbandonedSequenceIsReaped(t *testing.T) {
	current := time.Unix(1_700_000_000, 0)
	clock := func() time.Time { return current }
	writer := &recordingWriteCoordinator{putResult: MutationResult{Created: true}}
	resume := newTestResumeStoreWithClock(t, time.Minute, clock)
	fs := testFSWithSeeds(t, []byte("AAAA"), "movie.mov")
	handler := newWritableTestHandlerFull(t, writer, resume, fs)

	first := trustedRequest(http.MethodPut, testCapability+"/Docs/movie.mov", strings.NewReader("AAAA"))
	firstRecorder := httptest.NewRecorder()
	handler.ServeHTTP(firstRecorder, first)
	if firstRecorder.Code != http.StatusCreated {
		t.Fatalf("first PUT status = %d", firstRecorder.Code)
	}

	abandoned := trustedRequest(http.MethodPut, testCapability+"/Docs/movie.mov", strings.NewReader("BBBB"))
	abandoned.Header.Set("Content-Range", "bytes 4-7/12")
	abandonedRecorder := httptest.NewRecorder()
	handler.ServeHTTP(abandonedRecorder, abandoned)
	if abandonedRecorder.Code != http.StatusNoContent {
		t.Fatalf("abandoned sequence's first chunk status = %d, want 204", abandonedRecorder.Code)
	}

	resume.mu.Lock()
	_, exists := resume.entries["/Docs/movie.mov"]
	entries := len(resume.entries)
	resume.mu.Unlock()
	if !exists || entries != 1 {
		t.Fatalf("expected exactly one tracked in-progress sequence before the timeout, got %d (exists=%v)", entries, exists)
	}

	current = current.Add(2 * time.Minute)

	// Any subsequent Content-Range request -- for an unrelated path here --
	// is what actually gives the store a chance to reap; it happens lazily
	// on the next access, the same no-background-goroutine approach
	// boundedLockSystem already uses for lock expiry (see reapLocked).
	unrelated := trustedRequest(http.MethodPut, testCapability+"/Docs/other.mov", strings.NewReader("x"))
	unrelated.Header.Set("Content-Range", "bytes 0-0/1")
	unrelatedRecorder := httptest.NewRecorder()
	handler.ServeHTTP(unrelatedRecorder, unrelated)
	if unrelatedRecorder.Code != http.StatusCreated {
		t.Fatalf("unrelated resume PUT status = %d, want 201, body=%q", unrelatedRecorder.Code, unrelatedRecorder.Body.String())
	}

	resume.mu.Lock()
	_, stillExists := resume.entries["/Docs/movie.mov"]
	remainingEntries := len(resume.entries)
	resume.mu.Unlock()
	if stillExists {
		t.Fatal("abandoned sequence was not reaped after its idle timeout elapsed")
	}
	if remainingEntries != 0 {
		t.Fatalf("resume store retained %d entries after reaping, want 0", remainingEntries)
	}
}

// TestPUTResumeConcurrentSequencesForDifferentPathsDoNotInterfere covers two
// independent Content-Range resume sequences for different paths making
// progress concurrently without corrupting each other's accumulator.
func TestPUTResumeConcurrentSequencesForDifferentPathsDoNotInterfere(t *testing.T) {
	writer := &recordingWriteCoordinator{putResult: MutationResult{Created: true}}
	resume := newTestResumeStore(t)
	fs := testFSWithSeeds(t, []byte("AAAA"), "a.bin", "b.bin")
	handler := newWritableTestHandlerFull(t, writer, resume, fs)

	for _, path := range []string{"/Docs/a.bin", "/Docs/b.bin"} {
		request := trustedRequest(http.MethodPut, testCapability+path, strings.NewReader("AAAA"))
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusCreated {
			t.Fatalf("seed PUT %q status = %d", path, recorder.Code)
		}
	}

	var group sync.WaitGroup
	results := make([]int, 2)
	paths := []string{"/Docs/a.bin", "/Docs/b.bin"}
	for index, path := range paths {
		group.Add(1)
		go func(index int, path string) {
			defer group.Done()
			request := trustedRequest(http.MethodPut, testCapability+path, strings.NewReader("BBBB"))
			request.Header.Set("Content-Range", "bytes 4-7/8")
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, request)
			results[index] = recorder.Code
		}(index, path)
	}
	group.Wait()

	for _, code := range results {
		if code != http.StatusCreated {
			t.Fatalf("concurrent final chunk statuses = %v, want both 201", results)
		}
	}
}

// TestPUTWithoutContentRangeNeverTouchesTheResumeStore proves the fast path
// for a normal (non-resumed) PUT is unchanged: it commits immediately with
// no buffering, even when the resume store is unavailable (nil).
func TestPUTWithoutContentRangeNeverTouchesTheResumeStore(t *testing.T) {
	writer := &recordingWriteCoordinator{putResult: MutationResult{Created: true, ETag: `"r1"`}}
	handler := newWritableTestHandlerWithResume(t, writer, nil)

	request := trustedRequest(http.MethodPut, testCapability+"/Docs/plain.txt", strings.NewReader("payload"))
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusCreated || recorder.Header().Get("ETag") != `"r1"` {
		t.Fatalf("plain PUT with nil resume store = %d/%#v", recorder.Code, recorder.Header())
	}
	if string(writer.putBody) != "payload" {
		t.Fatalf("coordinator body = %q", writer.putBody)
	}
}

// TestPUTContentRangeWithoutResumeStoreFailsCleanly covers a writable mount
// somehow missing its resume store (should not happen outside tests, but
// must fail safely rather than panic on a nil dereference).
func TestPUTContentRangeWithoutResumeStoreFailsCleanly(t *testing.T) {
	writer := &recordingWriteCoordinator{}
	handler := newWritableTestHandlerWithResume(t, writer, nil)

	request := trustedRequest(http.MethodPut, testCapability+"/Docs/photo.png", strings.NewReader("chunk"))
	request.Header.Set("Content-Range", "bytes 0-4/10")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("Content-Range PUT without a resume store = %d, want 400", recorder.Code)
	}
	if writer.putRequest.Path != "" {
		t.Fatalf("reached the coordinator: %+v", writer.putRequest)
	}
}

// TestPUTContentRangeMalformedHeaderIsRejected covers syntactically invalid
// Content-Range headers.
func TestPUTContentRangeMalformedHeaderIsRejected(t *testing.T) {
	for _, header := range []string{
		"bytes 0-1",
		"bytes -1-5/10",
		"bytes 5-1/10",
		"bytes 0-10/10",
		"bytes abc-5/10",
		"seconds 0-1/2",
		"bytes 0-1/0",
	} {
		t.Run(header, func(t *testing.T) {
			writer := &recordingWriteCoordinator{}
			handler := newWritableTestHandler(t, writer)
			request := trustedRequest(http.MethodPut, testCapability+"/Docs/note.txt", strings.NewReader("x"))
			request.Header.Set("Content-Range", header)
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, request)
			if recorder.Code != http.StatusBadRequest {
				t.Errorf("Content-Range %q status = %d, want 400", header, recorder.Code)
			}
			if writer.putRequest.Path != "" {
				t.Errorf("malformed Content-Range %q reached the coordinator", header)
			}
		})
	}
}

// TestServeWriteMethodsFakeSuccessForMacOSJunkPaths covers the fix for a
// second user-reported issue: macOS Finder writes .DS_Store/AppleDouble
// metadata into any writable volume it mounts, including this one. An
// earlier version of this fix rejected those writes at the path-cleaning
// layer (400/403), which risked Finder's copy engine treating a rejected
// AppleDouble sidecar write as failing the whole visible file copy -- the
// same class of user-visible error this fix set out to reduce, not add to.
// Instead these paths get a normal-looking success with nothing ever staged,
// coordinated, or stored.
func TestServeWriteMethodsFakeSuccessForMacOSJunkPaths(t *testing.T) {
	for _, test := range []struct {
		name       string
		method     string
		target     string
		body       string
		wantStatus int
	}{
		{name: "PUT .DS_Store", method: http.MethodPut, target: "/Docs/.DS_Store", body: "junk", wantStatus: http.StatusCreated},
		{name: "PUT AppleDouble sidecar", method: http.MethodPut, target: "/Docs/._photo.png", body: "junk", wantStatus: http.StatusCreated},
		{name: "MKCOL Spotlight index", method: "MKCOL", target: "/.Spotlight-V100", wantStatus: http.StatusCreated},
		{name: "DELETE .DS_Store", method: http.MethodDelete, target: "/Docs/.DS_Store", wantStatus: http.StatusNoContent},
	} {
		t.Run(test.name, func(t *testing.T) {
			writer := &recordingWriteCoordinator{putResult: MutationResult{Created: true}}
			handler := newWritableTestHandler(t, writer)

			var body io.Reader
			if test.body != "" {
				body = strings.NewReader(test.body)
			}
			request := trustedRequest(test.method, testCapability+test.target, body)
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, request)

			if recorder.Code != test.wantStatus {
				t.Fatalf("status = %d, want %d", recorder.Code, test.wantStatus)
			}
			if writer.putRequest.Path != "" || writer.mkdirRequest.Path != "" || writer.deleteRequest.Path != "" {
				t.Fatalf("coordinator was invoked for a macOS junk path: put=%q mkdir=%q delete=%q",
					writer.putRequest.Path, writer.mkdirRequest.Path, writer.deleteRequest.Path)
			}
		})
	}
}

func TestWritableLockTokenIsRequiredAndCanBeReleased(t *testing.T) {
	writer := &recordingWriteCoordinator{putResult: MutationResult{Created: false, ETag: `"r2"`}}
	handler := newWritableTestHandler(t, writer)
	lockBody := `<D:lockinfo xmlns:D="DAV:"><D:lockscope><D:exclusive/></D:lockscope><D:locktype><D:write/></D:locktype><D:owner><D:href>tdrive</D:href></D:owner></D:lockinfo>`
	lock := trustedRequest("LOCK", testCapability+"/Docs/note.txt", strings.NewReader(lockBody))
	lock.Header.Set("Depth", "0")
	lock.Header.Set("Timeout", "Second-600")
	lockRecorder := httptest.NewRecorder()
	handler.ServeHTTP(lockRecorder, lock)
	if lockRecorder.Code != http.StatusOK {
		t.Fatalf("LOCK status = %d, body=%q", lockRecorder.Code, lockRecorder.Body.String())
	}
	tokenHeader := lockRecorder.Header().Get("Lock-Token")
	if !strings.HasPrefix(tokenHeader, "<") || !strings.HasSuffix(tokenHeader, ">") || !strings.Contains(lockRecorder.Body.String(), "lockdiscovery") {
		t.Fatalf("LOCK response headers/body = %#v/%q", lockRecorder.Header(), lockRecorder.Body.String())
	}

	put := trustedRequest(http.MethodPut, testCapability+"/Docs/note.txt", strings.NewReader("blocked"))
	putRecorder := httptest.NewRecorder()
	handler.ServeHTTP(putRecorder, put)
	if putRecorder.Code != statusLocked {
		t.Fatalf("unconditional PUT under lock = %d, want 423", putRecorder.Code)
	}

	put = trustedRequest(http.MethodPut, testCapability+"/Docs/note.txt", strings.NewReader("allowed"))
	put.Header.Set("If", "("+tokenHeader+")")
	putRecorder = httptest.NewRecorder()
	handler.ServeHTTP(putRecorder, put)
	if putRecorder.Code != http.StatusNoContent || len(writer.putRequest.Conditions.LockTokens) != 1 {
		t.Fatalf("conditional PUT = %d, conditions %+v, body=%q", putRecorder.Code, writer.putRequest.Conditions, putRecorder.Body.String())
	}

	unlock := trustedRequest("UNLOCK", testCapability+"/Docs/note.txt", nil)
	unlock.Header.Set("Lock-Token", tokenHeader)
	unlockRecorder := httptest.NewRecorder()
	handler.ServeHTTP(unlockRecorder, unlock)
	if unlockRecorder.Code != http.StatusNoContent {
		t.Fatalf("UNLOCK status = %d, body=%q", unlockRecorder.Code, unlockRecorder.Body.String())
	}
	unlockedAgain := httptest.NewRecorder()
	handler.ServeHTTP(unlockedAgain, unlock)
	if unlockedAgain.Code != http.StatusConflict {
		t.Fatalf("second UNLOCK status = %d, want 409", unlockedAgain.Code)
	}
}

func TestWritableRejectsMalformedIfAndUnsupportedCOPY(t *testing.T) {
	writer := &recordingWriteCoordinator{}
	handler := newWritableTestHandler(t, writer)
	request := trustedRequest(http.MethodDelete, testCapability+"/Docs/note.txt", nil)
	request.Header.Set("If", "(<unterminated)")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("malformed If status = %d, want 400", recorder.Code)
	}

	request = trustedRequest("COPY", testCapability+"/Docs/note.txt", nil)
	request.Header.Set("Destination", "http://127.0.0.1:7331"+testCapability+"/Docs/copy.txt")
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusNotImplemented {
		t.Fatalf("COPY status = %d, want stable 501", recorder.Code)
	}
}

func TestWritableCoordinatorErrorsUseSanitizedHTTPMapping(t *testing.T) {
	for _, test := range []struct {
		err    error
		status int
	}{
		{err: ErrWriteInvalid, status: http.StatusBadRequest},
		{err: ErrWriteForbidden, status: http.StatusForbidden},
		{err: ErrWriteNotFound, status: http.StatusNotFound},
		{err: ErrWriteConflict, status: http.StatusConflict},
		{err: ErrWritePreconditionFailed, status: http.StatusPreconditionFailed},
		{err: ErrWriteLengthRequired, status: http.StatusLengthRequired},
		{err: ErrWriteTooLarge, status: http.StatusRequestEntityTooLarge},
		{err: ErrWriteLocked, status: statusLocked},
		{err: ErrWriteInsufficientStorage, status: statusInsufficientStorage},
		{err: ErrWriteUnavailable, status: http.StatusServiceUnavailable},
		{err: context.Canceled, status: http.StatusRequestTimeout},
		{err: errors.New("telegram token secret"), status: http.StatusInternalServerError},
	} {
		writer := &recordingWriteCoordinator{err: test.err}
		handler := newWritableTestHandler(t, writer)
		request := trustedRequest(http.MethodPut, testCapability+"/new.txt", strings.NewReader("x"))
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		if recorder.Code != test.status {
			t.Errorf("error %v mapped to %d, want %d", test.err, recorder.Code, test.status)
		}
		if strings.Contains(recorder.Body.String(), "telegram") || strings.Contains(recorder.Body.String(), "secret") {
			t.Errorf("error %v leaked through body %q", test.err, recorder.Body.String())
		}
		if test.err == ErrWriteUnavailable && recorder.Header().Get("Retry-After") == "" {
			t.Error("temporary write failure omitted Retry-After")
		}
	}
}

func TestWritableStartConfigurationRequiresExplicitCapabilityPair(t *testing.T) {
	filesystem := testFS(t, nil)
	writer := &recordingWriteCoordinator{}
	var typedNilWriter *recordingWriteCoordinator
	for _, test := range []struct {
		name     string
		writable bool
		writer   WriteCoordinator
		valid    bool
	}{
		{name: "read only default", valid: true},
		{name: "writable pair", writable: true, writer: writer, valid: true},
		{name: "flag without writer", writable: true},
		{name: "flag with typed nil writer", writable: true, writer: typedNilWriter},
		{name: "writer without flag", writer: writer},
	} {
		t.Run(test.name, func(t *testing.T) {
			active, err := validateStartConfig(context.Background(), StartConfig{
				FS:       filesystem.fs,
				DriveID:  42,
				Writable: test.writable,
				Writer:   test.writer,
			})
			if test.valid && err != nil {
				t.Fatalf("validateStartConfig: %v", err)
			}
			if !test.valid && err == nil {
				t.Fatalf("invalid configuration produced %+v", active)
			}
			if test.valid && (active.writable != test.writable || active.writer != test.writer) {
				t.Fatalf("active configuration = %+v", active)
			}
		})
	}
}

func TestWritableModeAndTimeoutAreTruthful(t *testing.T) {
	if got := mountMode(false); got != "read-only" {
		t.Fatalf("read mode = %q", got)
	}
	if got := mountMode(true); got != "read-write" {
		t.Fatalf("write mode = %q", got)
	}
	if got := serverReadTimeout(false); got != defaultReadTimeout {
		t.Fatalf("read-only timeout = %s, want %s", got, defaultReadTimeout)
	}
	if got := serverReadTimeout(true); got != 0 {
		t.Fatalf("streaming writable timeout = %s, want no fixed whole-body deadline", got)
	}
}

func TestWritableProtectionBoundsStreamingWritesSeparately(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	handler := newProtectedHandler(protectionConfig{
		capabilityPath:     testCapability,
		authority:          "127.0.0.1:7331",
		writable:           true,
		maxConcurrent:      4,
		maxConcurrentWrite: 1,
	}, http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		close(entered)
		<-release
		response.WriteHeader(http.StatusCreated)
	}))

	firstDone := make(chan struct{})
	go func() {
		defer close(firstDone)
		handler.ServeHTTP(
			httptest.NewRecorder(),
			trustedRequest(http.MethodPut, testCapability+"/one", strings.NewReader("one")),
		)
	}()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("first write did not enter application")
	}

	second := httptest.NewRecorder()
	handler.ServeHTTP(second, trustedRequest(http.MethodPut, testCapability+"/two", strings.NewReader("two")))
	if second.Code != http.StatusServiceUnavailable || second.Header().Get("Retry-After") != serverBusyRetrySeconds {
		t.Fatalf("concurrent PUT response = %d/%#v", second.Code, second.Header())
	}
	close(release)
	select {
	case <-firstDone:
	case <-time.After(time.Second):
		t.Fatal("first write did not finish")
	}
}

func TestServerWiresExplicitWritableCoordinatorEndToEnd(t *testing.T) {
	filesystem := testFS(t, nil)
	writer := &recordingWriteCoordinator{putResult: MutationResult{Created: true, ETag: `"new-r1"`}}
	server := NewServer()
	status, err := server.Start(context.Background(), StartConfig{
		FS:         filesystem.fs,
		DriveID:    42,
		DriveTitle: "Personal",
		Writable:   true,
		Writer:     writer,
	})
	if err != nil {
		t.Fatalf("Start writable server: %v", err)
	}
	t.Cleanup(func() { _ = server.Stop(context.Background()) })
	if status.Mode != "read-write" || !status.Running {
		t.Fatalf("writable status = %+v", status)
	}

	request, err := http.NewRequest(http.MethodPut, status.URL+"Docs/new.txt", strings.NewReader("committed"))
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("PUT writable server: %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusCreated || response.Header.Get("ETag") != `"new-r1"` {
		t.Fatalf("PUT response = %d/%#v", response.StatusCode, response.Header)
	}
	if writer.putRequest.Path != "/Docs/new.txt" || string(writer.putBody) != "committed" {
		t.Fatalf("writer request/body = %+v/%q", writer.putRequest, writer.putBody)
	}

	patchBody := `<D:propertyupdate xmlns:D="DAV:" xmlns:Z="urn:schemas-microsoft-com:"><D:set><D:prop><Z:Win32LastModifiedTime>Sun, 14 Sep 2018 12:50:00 GMT</Z:Win32LastModifiedTime></D:prop></D:set></D:propertyupdate>`
	request, err = http.NewRequest("PROPPATCH", status.URL+"Docs/note.txt", strings.NewReader(patchBody))
	if err != nil {
		t.Fatalf("New PROPPATCH request: %v", err)
	}
	request.Header.Set("Content-Type", "text/xml; charset=utf-8")
	request.Header.Set("User-Agent", "Microsoft-WebDAV-MiniRedir/10.0.26100")
	response, err = http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("PROPPATCH writable server: %v", err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusMultiStatus {
		t.Fatalf("PROPPATCH response = %d/%#v", response.StatusCode, response.Header)
	}

	request, err = http.NewRequest(http.MethodDelete, status.URL+"Docs/note.txt", nil)
	if err != nil {
		t.Fatalf("New DELETE request: %v", err)
	}
	request.Header.Set("Depth", "0")
	request.Header.Set("User-Agent", "Microsoft-WebDAV-MiniRedir/10.0.26100")
	response, err = http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("DELETE writable server: %v", err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusNoContent || writer.deleteRequest.Path != "/Docs/note.txt" {
		t.Fatalf("DELETE response/request = %d/%+v", response.StatusCode, writer.deleteRequest)
	}
}
