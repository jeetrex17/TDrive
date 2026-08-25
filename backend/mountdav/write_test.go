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
	locks := newBoundedLockSystem(defaultMaxActiveLocks)
	application := &readApplication{
		capabilityPath: testCapability,
		fs:             testFS(t, nil),
		lockSystem:     locks,
		writer:         writer,
		authority:      "127.0.0.1:7331",
	}
	return newProtectedHandler(protectionConfig{
		capabilityPath:     testCapability,
		authority:          "127.0.0.1:7331",
		maxConcurrent:      8,
		maxConcurrentWrite: 2,
		writable:           true,
	}, application)
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
		{name: "PUT content range", method: http.MethodPut, target: "/Docs/note.txt", headers: map[string]string{"Content-Range": "bytes 0-1/2"}, status: http.StatusBadRequest},
		{name: "MKCOL body", method: "MKCOL", target: "/Docs/New", body: "extension", status: http.StatusUnsupportedMediaType},
		{name: "MOVE body", method: "MOVE", target: "/Docs/note.txt", body: "extension", headers: map[string]string{"Destination": testCapability + "/Docs/moved.txt"}, status: http.StatusUnsupportedMediaType},
		{name: "MOVE bad depth", method: "MOVE", target: "/Docs/note.txt", headers: map[string]string{"Destination": testCapability + "/Docs/moved.txt", "Depth": "1"}, status: http.StatusBadRequest},
		{name: "DELETE body", method: http.MethodDelete, target: "/Docs/note.txt", body: "extension", status: http.StatusUnsupportedMediaType},
		{name: "DELETE bad depth", method: http.MethodDelete, target: "/Docs/note.txt", headers: map[string]string{"Depth": "0"}, status: http.StatusBadRequest},
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
}
