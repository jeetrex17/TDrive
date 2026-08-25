package mountdav

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"sync"
	"testing"

	"TDrive/backend/mountfs"

	"golang.org/x/net/webdav"
)

func TestPropfindNeverAdvertisesWriteLocks(t *testing.T) {
	portable := testFS(t, nil).fs
	handler := newPropfindTestHandler(portable)

	requests := []struct {
		name string
		body string
	}{
		{
			name: "allprop",
			body: `<?xml version="1.0"?><D:propfind xmlns:D="DAV:"><D:allprop/></D:propfind>`,
		},
		{
			name: "explicit supportedlock",
			body: `<?xml version="1.0"?><D:propfind xmlns:D="DAV:"><D:prop><D:supportedlock/></D:prop></D:propfind>`,
		},
	}
	for _, test := range requests {
		t.Run(test.name, func(t *testing.T) {
			request := trustedRequest("PROPFIND", testCapability+"/Docs/", bytes.NewBufferString(test.body))
			request.Header.Set("Depth", "0")
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, request)

			if recorder.Code != 207 {
				t.Fatalf("PROPFIND status = %d, body=%q", recorder.Code, recorder.Body.String())
			}
			assertEmptySupportedLock(t, recorder.Body.Bytes())
			if got := recorder.Header().Get("Allow"); got != allowedMethodsHeader {
				t.Fatalf("Allow = %q, want %q", got, allowedMethodsHeader)
			}
			if got := recorder.Header().Get("DAV"); got != "1" {
				t.Fatalf("DAV = %q, want class 1", got)
			}
		})
	}
}

func TestPropfindPreservesNonMultistatusErrors(t *testing.T) {
	portable := testFS(t, nil).fs
	handler := newPropfindTestHandler(portable)
	request := trustedRequest("PROPFIND", testCapability+"/Docs/", bytes.NewBufferString("<invalid"))
	request.Header.Set("Depth", "0")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusBadRequest || !bytes.Contains(recorder.Body.Bytes(), []byte("Bad Request")) {
		t.Fatalf("malformed PROPFIND = %d/%q", recorder.Code, recorder.Body.String())
	}
}

func TestMalformedPropfindRejectedBeforeMetadataPreflight(t *testing.T) {
	source := &sequencedSource{responses: []directoryResponse{{entries: nil}}}
	opener := &rejectingOpener{}
	portable := newUncachedMountFS(t, source, opener)
	handler := newPropfindTestHandler(portable)
	request := trustedRequest("PROPFIND", testCapability+"/", bytes.NewBufferString("<invalid"))
	request.Header.Set("Depth", "1")
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("malformed PROPFIND status = %d, want %d; body=%q", recorder.Code, http.StatusBadRequest, recorder.Body.String())
	}
	if calls := source.callCount(); calls != 0 {
		t.Fatalf("directory source calls = %d, want none before XML validation", calls)
	}
	if calls := opener.callCount(); calls != 0 {
		t.Fatalf("content opener calls = %d, want none", calls)
	}
}

func TestPropfindPreflightRejectsMetadataFailures(t *testing.T) {
	tests := []struct {
		name   string
		err    error
		status int
	}{
		{name: "unclassified source failure", err: errors.New("database detail: projection unavailable"), status: http.StatusInternalServerError},
		{name: "typed temporarily unavailable", err: fmt.Errorf("%w: database detail", mountfs.ErrContentUnavailable), status: http.StatusServiceUnavailable},
		{name: "access denied", err: fmt.Errorf("%w: database detail", mountfs.ErrAccessDenied), status: http.StatusForbidden},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			source := &sequencedSource{responses: []directoryResponse{{err: test.err}}}
			opener := &rejectingOpener{}
			portable := newUncachedMountFS(t, source, opener)
			handler := newPropfindTestHandler(portable)
			request := trustedRequest("PROPFIND", testCapability+"/", bytes.NewBufferString(`<D:propfind xmlns:D="DAV:"><D:allprop/></D:propfind>`))
			request.Header.Set("Depth", "1")
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, request)

			if recorder.Code != test.status {
				t.Fatalf("PROPFIND status = %d, want %d; body=%q", recorder.Code, test.status, recorder.Body.String())
			}
			if bytes.Contains(recorder.Body.Bytes(), []byte("database detail")) {
				t.Fatalf("PROPFIND leaked source error: %q", recorder.Body.String())
			}
			if opener.callCount() != 0 {
				t.Fatalf("metadata preflight opened content %d times", opener.callCount())
			}
		})
	}
}

func TestPropfindUsesOneCompleteMetadataSnapshot(t *testing.T) {
	children := []mountfs.SourceEntry{{
		ID:         "f:note",
		ParentID:   mountfs.RootID,
		Name:       "note.txt",
		Kind:       mountfs.KindFile,
		Size:       4,
		ContentRef: "telegram:1",
	}}
	source := &sequencedSource{responses: []directoryResponse{
		{entries: children},
		{err: mountfs.ErrContentUnavailable},
	}}
	opener := &rejectingOpener{}
	portable := newUncachedMountFS(t, source, opener)
	handler := newPropfindTestHandler(portable)
	request := trustedRequest("PROPFIND", testCapability+"/", bytes.NewBufferString(`<D:propfind xmlns:D="DAV:"><D:allprop/></D:propfind>`))
	request.Header.Set("Depth", "1")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)

	if recorder.Code != 207 || !bytes.Contains(recorder.Body.Bytes(), []byte("note.txt")) {
		t.Fatalf("PROPFIND returned a partial snapshot: status=%d body=%q", recorder.Code, recorder.Body.String())
	}
	if calls := source.callCount(); calls != 1 {
		t.Fatalf("directory source calls = %d, want one preflight snapshot", calls)
	}
	if opener.callCount() != 0 {
		t.Fatalf("PROPFIND opened content %d times", opener.callCount())
	}
}

func newPropfindTestHandler(portable *mountfs.FS) http.Handler {
	return protectedTestHandler(2, &readApplication{
		capabilityPath: testCapability,
		fs:             NewFileSystem(portable),
		lockSystem:     webdav.NewMemLS(),
	})
}

type directoryResponse struct {
	entries []mountfs.SourceEntry
	err     error
}

type sequencedSource struct {
	mu        sync.Mutex
	responses []directoryResponse
	calls     int
}

func (source *sequencedSource) ListDirectory(context.Context, int64, string) ([]mountfs.SourceEntry, error) {
	source.mu.Lock()
	defer source.mu.Unlock()
	index := min(source.calls, len(source.responses)-1)
	source.calls++
	response := source.responses[index]
	return slices.Clone(response.entries), response.err
}

func (source *sequencedSource) callCount() int {
	source.mu.Lock()
	defer source.mu.Unlock()
	return source.calls
}

type rejectingOpener struct {
	mu    sync.Mutex
	calls int
}

func (opener *rejectingOpener) OpenContent(context.Context, int64, mountfs.SourceEntry) (mountfs.RandomAccessContent, error) {
	opener.mu.Lock()
	defer opener.mu.Unlock()
	opener.calls++
	return nil, errors.New("content must not open during PROPFIND")
}

func (opener *rejectingOpener) callCount() int {
	opener.mu.Lock()
	defer opener.mu.Unlock()
	return opener.calls
}

func newUncachedMountFS(t *testing.T, source mountfs.DirectorySource, opener mountfs.ContentOpener) *mountfs.FS {
	t.Helper()
	filesystem, err := mountfs.NewWithOptions(42, source, opener, mountfs.Options{DisableSnapshotCache: true})
	if err != nil {
		t.Fatalf("mountfs.NewWithOptions: %v", err)
	}
	return filesystem
}

func assertEmptySupportedLock(t *testing.T, body []byte) {
	t.Helper()
	decoder := xml.NewDecoder(bytes.NewReader(body))
	insideSupportedLock := 0
	supportedLockCount := 0
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("PROPFIND returned invalid XML: %v\n%s", err, body)
		}
		switch token := token.(type) {
		case xml.StartElement:
			if insideSupportedLock > 0 {
				t.Fatalf("supportedlock is not empty; found %s:%s in %q", token.Name.Space, token.Name.Local, body)
			}
			if token.Name.Space == webDAVNamespace && token.Name.Local == "supportedlock" {
				supportedLockCount++
				insideSupportedLock = 1
			}
		case xml.EndElement:
			if insideSupportedLock > 0 && token.Name.Space == webDAVNamespace && token.Name.Local == "supportedlock" {
				insideSupportedLock = 0
			}
		}
	}
	if supportedLockCount == 0 {
		t.Fatalf("PROPFIND omitted supportedlock capability: %q", body)
	}
}

var _ http.Handler = (*readApplication)(nil)
