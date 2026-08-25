package mountdav

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

const testCapability = "/tdrive-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

func trustedRequest(method, target string, body io.Reader) *http.Request {
	request := httptest.NewRequest(method, target, body)
	request.Host = "127.0.0.1:7331"
	request.RemoteAddr = "127.0.0.1:54321"
	return request
}

func protectedTestHandler(maxConcurrent int, next http.Handler) http.Handler {
	return newProtectedHandler(protectionConfig{
		capabilityPath: testCapability,
		authority:      "127.0.0.1:7331",
		maxConcurrent:  maxConcurrent,
	}, next)
}

func TestProtectedHandlerAdvertisesOnlyReadMethods(t *testing.T) {
	calls := 0
	handler := protectedTestHandler(2, http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		calls++
		response.WriteHeader(http.StatusMultiStatus)
	}))

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, trustedRequest(http.MethodOptions, testCapability+"/", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("OPTIONS status = %d, want 200", recorder.Code)
	}
	if got := recorder.Header().Get("Allow"); got != allowedMethodsHeader {
		t.Fatalf("Allow = %q, want %q", got, allowedMethodsHeader)
	}
	if recorder.Header().Get("Content-Length") != "0" {
		t.Fatalf("OPTIONS Content-Length = %q, want 0", recorder.Header().Get("Content-Length"))
	}
	if strings.Contains(recorder.Header().Get("Allow"), "PUT") || recorder.Header().Get("DAV") != "1" || recorder.Header().Get("MS-Author-Via") != "DAV" {
		t.Fatalf("OPTIONS headers are not read-only: %#v", recorder.Header())
	}
	if recorder.Header().Get("X-Content-Type-Options") != "nosniff" || recorder.Header().Get("Referrer-Policy") != "no-referrer" || recorder.Header().Get("Content-Security-Policy") != "default-src 'none'; sandbox" {
		t.Fatalf("security headers are missing: %#v", recorder.Header())
	}
	if calls != 0 {
		t.Fatalf("OPTIONS reached application %d times", calls)
	}

	for _, method := range []string{http.MethodPut, http.MethodPost, http.MethodDelete, "MKCOL", "COPY", "MOVE", "LOCK", "UNLOCK", "PROPPATCH", "PATCH", "TRACE"} {
		recorder = httptest.NewRecorder()
		handler.ServeHTTP(recorder, trustedRequest(method, testCapability+"/file", strings.NewReader("ignored")))
		if recorder.Code != http.StatusMethodNotAllowed {
			t.Errorf("%s status = %d, want 405", method, recorder.Code)
		}
		if recorder.Header().Get("Allow") != allowedMethodsHeader {
			t.Errorf("%s Allow = %q", method, recorder.Header().Get("Allow"))
		}
	}
}

func TestProtectedHandlerRejectsUntrustedMetadata(t *testing.T) {
	handler := protectedTestHandler(2, http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.WriteHeader(http.StatusOK)
	}))
	tests := []struct {
		name   string
		mutate func(*http.Request)
		status int
	}{
		{name: "wrong capability", mutate: func(request *http.Request) { request.URL.Path = "/wrong/file" }, status: http.StatusNotFound},
		{name: "capability suffix", mutate: func(request *http.Request) { request.URL.Path = testCapability + "-suffix/file" }, status: http.StatusNotFound},
		{name: "wrong host", mutate: func(request *http.Request) { request.Host = "localhost:7331" }, status: http.StatusForbidden},
		{name: "non-loopback peer", mutate: func(request *http.Request) { request.RemoteAddr = "192.0.2.1:1234" }, status: http.StatusForbidden},
		{name: "malformed peer", mutate: func(request *http.Request) { request.RemoteAddr = "invalid" }, status: http.StatusForbidden},
		{name: "origin", mutate: func(request *http.Request) { request.Header.Set("Origin", "null") }, status: http.StatusForbidden},
		{name: "empty origin", mutate: func(request *http.Request) { request.Header["origin"] = []string{""} }, status: http.StatusForbidden},
		{name: "browser fetch metadata", mutate: func(request *http.Request) { request.Header.Set("Sec-Fetch-Site", "none") }, status: http.StatusForbidden},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := trustedRequest(http.MethodGet, testCapability+"/file", nil)
			test.mutate(request)
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, request)
			if recorder.Code != test.status {
				t.Fatalf("status = %d, want %d", recorder.Code, test.status)
			}
		})
	}
}

func TestProtectedHandlerValidatesDepthAndBody(t *testing.T) {
	handler := protectedTestHandler(2, http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		body, err := io.ReadAll(request.Body)
		if err != nil {
			t.Errorf("application ReadAll: %v", err)
		}
		response.WriteHeader(http.StatusMultiStatus)
		_, _ = response.Write(body)
	}))

	for _, test := range []struct {
		depth  string
		status int
	}{
		{depth: "", status: http.StatusForbidden},
		{depth: "infinity", status: http.StatusForbidden},
		{depth: "2", status: http.StatusBadRequest},
		{depth: " 1", status: http.StatusBadRequest},
	} {
		request := trustedRequest("PROPFIND", testCapability+"/", nil)
		if test.depth != "" {
			request.Header.Set("Depth", test.depth)
		}
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		if recorder.Code != test.status {
			t.Errorf("Depth %q status = %d, want %d", test.depth, recorder.Code, test.status)
		}
	}

	validBody := `<D:propfind xmlns:D="DAV:"><D:allprop/></D:propfind>`
	request := trustedRequest("PROPFIND", testCapability+"/", strings.NewReader(validBody))
	request.Header.Set("Depth", "0")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusMultiStatus || recorder.Body.String() != validBody {
		t.Fatalf("bounded body response = %d/%q", recorder.Code, recorder.Body.String())
	}

	request = trustedRequest("PROPFIND", testCapability+"/", bytes.NewReader(make([]byte, maxRequestBodyBytes+1)))
	request.Header.Set("Depth", "1")
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized body status = %d, want 413", recorder.Code)
	}
}

func TestProtectedHandlerBoundsConcurrency(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	handler := protectedTestHandler(1, http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		once.Do(func() { close(entered) })
		<-release
		response.WriteHeader(http.StatusOK)
	}))

	firstDone := make(chan struct{})
	go func() {
		defer close(firstDone)
		handler.ServeHTTP(httptest.NewRecorder(), trustedRequest(http.MethodGet, testCapability+"/one", nil))
	}()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("first request never entered application")
	}

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, trustedRequest(http.MethodHead, testCapability+"/two", nil))
	if recorder.Code != http.StatusServiceUnavailable || recorder.Header().Get("Retry-After") != serverBusyRetrySeconds {
		t.Fatalf("concurrent response = %d, headers %#v", recorder.Code, recorder.Header())
	}
	close(release)
	select {
	case <-firstDone:
	case <-time.After(time.Second):
		t.Fatal("first request did not finish")
	}
}
