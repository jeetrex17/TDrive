package mountdav

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"golang.org/x/net/webdav"
)

func TestBoundedLockSystemPublicTokensCapacityAndExpiry(t *testing.T) {
	locks := newBoundedLockSystem(1)
	now := time.Unix(1_700_000_000, 0)
	details := webdav.LockDetails{Root: "/one", Duration: time.Second, ZeroDepth: true}
	token, err := locks.Create(now, details)
	if err != nil || !strings.HasPrefix(token, "opaquelocktoken:") {
		t.Fatalf("Create = (%q, %v)", token, err)
	}
	if _, err := locks.Create(now, webdav.LockDetails{Root: "/two", Duration: time.Minute}); !errors.Is(err, errLockCapacity) {
		t.Fatalf("capacity Create error = %v, want errLockCapacity", err)
	}

	release, err := locks.Confirm(now, "/one", "", webdav.Condition{Token: token})
	if err != nil || release == nil {
		t.Fatalf("Confirm returned release=%t, error=%v", release != nil, err)
	}
	if err := locks.Unlock(now, token); !errors.Is(err, webdav.ErrLocked) {
		t.Fatalf("Unlock held lock error = %v, want locked", err)
	}
	release()

	refreshed, err := locks.Refresh(now, token, 2*time.Second)
	if err != nil || refreshed.Duration != 2*time.Second {
		t.Fatalf("Refresh = (%+v, %v)", refreshed, err)
	}
	if _, err := locks.Refresh(now, "opaquelocktoken:missing", time.Second); !errors.Is(err, webdav.ErrNoSuchLock) {
		t.Fatalf("missing Refresh error = %v", err)
	}
	if _, err := locks.Confirm(now, "/one", "", lockCondition("opaquelocktoken:"+"missing")); !errors.Is(err, webdav.ErrConfirmationFailed) {
		t.Fatalf("unknown Confirm error = %v", err)
	}

	if _, err := locks.Create(now.Add(3*time.Second), webdav.LockDetails{Root: "/two", Duration: time.Second}); err != nil {
		t.Fatalf("expired lock did not free capacity: %v", err)
	}
}

func TestWritableLOCKRefreshCapsTimeout(t *testing.T) {
	handler := newWritableTestHandler(t, &recordingWriteCoordinator{})
	token := createTestLock(t, handler, testCapability+"/Docs/note.txt")
	request := trustedRequest("LOCK", testCapability+"/Docs/note.txt", nil)
	request.Header.Set("If", "(<"+token+">)")
	request.Header.Set("Timeout", "Infinite")
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), "Second-3600") {
		t.Fatalf("refresh LOCK = %d/%#v/%q", recorder.Code, recorder.Header(), recorder.Body.String())
	}
}

func TestWritableLOCKRefreshRequiresMatchingResource(t *testing.T) {
	handler := newWritableTestHandler(t, &recordingWriteCoordinator{})
	token := createTestLock(t, handler, testCapability+"/Docs/note.txt")
	request := trustedRequest("LOCK", testCapability+"/Docs/other.txt", nil)
	request.Header.Set("If", "(<"+token+">)")
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusPreconditionFailed {
		t.Fatalf("wrong-resource LOCK refresh status = %d, want 412", recorder.Code)
	}
}

func TestWritableLOCKValidation(t *testing.T) {
	validBody := `<D:lockinfo xmlns:D="DAV:"><D:lockscope><D:exclusive/></D:lockscope><D:locktype><D:write/></D:locktype></D:lockinfo>`
	for _, test := range []struct {
		name    string
		body    string
		depth   string
		timeout string
		status  int
	}{
		{name: "malformed XML", body: "<lockinfo", status: http.StatusBadRequest},
		{name: "wrong namespace", body: `<lockinfo><lockscope><exclusive/></lockscope><locktype><write/></locktype></lockinfo>`, status: http.StatusBadRequest},
		{name: "mixed child namespace", body: `<D:lockinfo xmlns:D="DAV:" xmlns:X="urn:not-dav"><X:lockscope><X:exclusive/></X:lockscope><D:locktype><D:write/></D:locktype></D:lockinfo>`, status: http.StatusBadRequest},
		{name: "missing scope", body: `<D:lockinfo xmlns:D="DAV:"><D:locktype><D:write/></D:locktype></D:lockinfo>`, status: http.StatusBadRequest},
		{name: "shared unsupported", body: `<D:lockinfo xmlns:D="DAV:"><D:lockscope><D:shared/></D:lockscope><D:locktype><D:write/></D:locktype></D:lockinfo>`, status: http.StatusNotImplemented},
		{name: "owner too large", body: `<D:lockinfo xmlns:D="DAV:"><D:lockscope><D:exclusive/></D:lockscope><D:locktype><D:write/></D:locktype><D:owner>` + strings.Repeat("x", maxLockOwnerBytes+1) + `</D:owner></D:lockinfo>`, status: http.StatusRequestEntityTooLarge},
		{name: "invalid depth", body: validBody, depth: "1", status: http.StatusBadRequest},
		{name: "invalid timeout", body: validBody, timeout: "Second-nope", status: http.StatusBadRequest},
	} {
		t.Run(test.name, func(t *testing.T) {
			handler := newWritableTestHandler(t, &recordingWriteCoordinator{})
			request := trustedRequest("LOCK", testCapability+"/Docs/note.txt", strings.NewReader(test.body))
			if test.depth != "" {
				request.Header.Set("Depth", test.depth)
			}
			if test.timeout != "" {
				request.Header.Set("Timeout", test.timeout)
			}
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, request)
			if recorder.Code != test.status {
				t.Fatalf("LOCK status = %d, want %d; body=%q", recorder.Code, test.status, recorder.Body.String())
			}
		})
	}
}

func TestWritableUNLOCKRequiresMatchingResource(t *testing.T) {
	handler := newWritableTestHandler(t, &recordingWriteCoordinator{})
	token := createTestLock(t, handler, testCapability+"/Docs/note.txt")
	request := trustedRequest("UNLOCK", testCapability+"/Docs/other.txt", nil)
	request.Header.Set("Lock-Token", "<"+token+">")
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusForbidden {
		t.Fatalf("wrong-resource UNLOCK status = %d, want 403", recorder.Code)
	}
}

func TestNegatedLockTokenNeverAuthorizesMutation(t *testing.T) {
	writer := &recordingWriteCoordinator{}
	handler := newWritableTestHandler(t, writer)
	token := createTestLock(t, handler, testCapability+"/Docs/note.txt")
	request := trustedRequest(http.MethodDelete, testCapability+"/Docs/note.txt", nil)
	request.Header.Set("If", "(Not <"+token+">)")
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusPreconditionFailed {
		t.Fatalf("negated token DELETE status = %d, want 412", recorder.Code)
	}
	if writer.deleteRequest.Path != "" {
		t.Fatalf("negated token reached writer: %+v", writer.deleteRequest)
	}
}

func TestFalseNegatedTokenCanAccompanyValidPositiveLockToken(t *testing.T) {
	writer := &recordingWriteCoordinator{}
	handler := newWritableTestHandler(t, writer)
	token := createTestLock(t, handler, testCapability+"/Docs/note.txt")
	request := trustedRequest(http.MethodDelete, testCapability+"/Docs/note.txt", nil)
	request.Header.Set("If", "(Not <opaquelocktoken:missing> <"+token+">)")
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusNoContent {
		t.Fatalf("valid compound lock condition status = %d, want 204", recorder.Code)
	}
	if len(writer.deleteRequest.Conditions.LockTokens) != 1 || writer.deleteRequest.Conditions.LockTokens[0] != token {
		t.Fatalf("validated tokens = %+v", writer.deleteRequest.Conditions.LockTokens)
	}
}

func TestInvalidPositiveTokenMakesCompoundLockListFail(t *testing.T) {
	writer := &recordingWriteCoordinator{}
	handler := newWritableTestHandler(t, writer)
	token := createTestLock(t, handler, testCapability+"/Docs/note.txt")
	request := trustedRequest(http.MethodDelete, testCapability+"/Docs/note.txt", nil)
	request.Header.Set("If", "(<"+token+"> <opaquelocktoken:missing>)")
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusPreconditionFailed {
		t.Fatalf("false compound lock condition status = %d, want 412", recorder.Code)
	}
	if writer.deleteRequest.Path != "" {
		t.Fatalf("false lock list reached writer: %+v", writer.deleteRequest)
	}
}

func TestWritableLockLimitIsBounded(t *testing.T) {
	locks := newBoundedLockSystem(1)
	application := &readApplication{
		capabilityPath: testCapability,
		authority:      "127.0.0.1:7331",
		fs:             testFS(t, nil),
		lockSystem:     locks,
		writer:         &recordingWriteCoordinator{},
	}
	handler := newProtectedHandler(protectionConfig{
		capabilityPath: testCapability,
		authority:      "127.0.0.1:7331",
		writable:       true,
	}, application)
	_ = createTestLock(t, handler, testCapability+"/Docs/note.txt")

	request := trustedRequest("LOCK", testCapability+"/other.txt", strings.NewReader(`<D:lockinfo xmlns:D="DAV:"><D:lockscope><D:exclusive/></D:lockscope><D:locktype><D:write/></D:locktype></D:lockinfo>`))
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusServiceUnavailable || recorder.Header().Get("Retry-After") == "" {
		t.Fatalf("lock capacity response = %d/%#v", recorder.Code, recorder.Header())
	}
}

func TestWritablePropfindAdvertisesExclusiveWriteLocks(t *testing.T) {
	handler := newWritableTestHandler(t, &recordingWriteCoordinator{})
	request := trustedRequest("PROPFIND", testCapability+"/Docs/", strings.NewReader(`<D:propfind xmlns:D="DAV:"><D:prop><D:supportedlock/></D:prop></D:propfind>`))
	request.Header.Set("Depth", "0")
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusMultiStatus || !strings.Contains(recorder.Body.String(), "lockentry") || !strings.Contains(recorder.Body.String(), "exclusive") {
		t.Fatalf("writable PROPFIND = %d/%q", recorder.Code, recorder.Body.String())
	}
}

func createTestLock(t *testing.T, handler http.Handler, target string) string {
	t.Helper()
	body := `<D:lockinfo xmlns:D="DAV:"><D:lockscope><D:exclusive/></D:lockscope><D:locktype><D:write/></D:locktype></D:lockinfo>`
	request := trustedRequest("LOCK", target, strings.NewReader(body))
	request.Header.Set("Depth", "0")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("create lock status = %d, body=%q", recorder.Code, recorder.Body.String())
	}
	header := recorder.Header().Get("Lock-Token")
	if len(header) < 3 || header[0] != '<' || header[len(header)-1] != '>' {
		t.Fatalf("Lock-Token = %q", header)
	}
	return header[1 : len(header)-1]
}
