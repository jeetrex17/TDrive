package mountdav

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

type manualClock struct {
	mu  sync.Mutex
	now time.Time
}

func (clock *manualClock) Now() time.Time {
	clock.mu.Lock()
	defer clock.mu.Unlock()
	return clock.now
}

func (clock *manualClock) Advance(duration time.Duration) {
	clock.mu.Lock()
	defer clock.mu.Unlock()
	clock.now = clock.now.Add(duration)
}

type deadlineRecorder struct {
	*httptest.ResponseRecorder
	mu        sync.Mutex
	deadlines []time.Time
}

func newDeadlineRecorder() *deadlineRecorder {
	return &deadlineRecorder{ResponseRecorder: httptest.NewRecorder()}
}

func (recorder *deadlineRecorder) SetReadDeadline(deadline time.Time) error {
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	recorder.deadlines = append(recorder.deadlines, deadline)
	return nil
}

func (recorder *deadlineRecorder) Deadlines() []time.Time {
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	return append([]time.Time(nil), recorder.deadlines...)
}

type timeoutReadError struct{}

func (timeoutReadError) Error() string   { return "injected stalled body" }
func (timeoutReadError) Timeout() bool   { return true }
func (timeoutReadError) Temporary() bool { return true }

type timeoutBody struct{}

func (timeoutBody) Read([]byte) (int, error) { return 0, timeoutReadError{} }
func (timeoutBody) Close() error             { return nil }

type advancingBody struct {
	clock  *manualClock
	chunks []string
	index  int
}

func (body *advancingBody) Read(buffer []byte) (int, error) {
	if body.index >= len(body.chunks) {
		return 0, io.EOF
	}
	chunk := body.chunks[body.index]
	body.index++
	n := copy(buffer, chunk)
	body.clock.Advance(20 * time.Second)
	return n, nil
}

func (*advancingBody) Close() error { return nil }

func writableDeadlineTestHandler(t *testing.T, clock *manualClock, writer WriteCoordinator) http.Handler {
	t.Helper()
	locks := newBoundedLockSystem(defaultMaxActiveLocks)
	application := &readApplication{
		capabilityPath: testCapability,
		authority:      "127.0.0.1:7331",
		fs:             testFS(t, nil),
		lockSystem:     locks,
		writer:         writer,
	}
	return newProtectedHandler(protectionConfig{
		capabilityPath:         testCapability,
		authority:              "127.0.0.1:7331",
		maxConcurrent:          2,
		maxConcurrentWrite:     1,
		writable:               true,
		enforceBodyReadTimeout: true,
		putBodyIdleTimeout:     10 * time.Second,
		controlBodyReadTimeout: 5 * time.Second,
		bodyReadNow:            clock.Now,
	}, application)
}

func TestStalledPUTTimesOutAndReleasesWriteSlot(t *testing.T) {
	clock := &manualClock{now: time.Unix(1_700_000_000, 0)}
	writer := &recordingWriteCoordinator{putResult: MutationResult{Created: true}}
	handler := writableDeadlineTestHandler(t, clock, writer)
	request := trustedRequest(http.MethodPut, testCapability+"/stalled.bin", timeoutBody{})
	recorder := newDeadlineRecorder()

	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusRequestTimeout {
		t.Fatalf("stalled PUT status = %d, want 408; body=%q", recorder.Code, recorder.Body.String())
	}
	deadlines := recorder.Deadlines()
	if len(deadlines) != 2 || !deadlines[0].Equal(clock.Now().Add(10*time.Second)) || !deadlines[1].IsZero() {
		t.Fatalf("stalled PUT deadlines = %v", deadlines)
	}

	second := trustedRequest(http.MethodPut, testCapability+"/next.bin", strings.NewReader("ok"))
	secondRecorder := newDeadlineRecorder()
	handler.ServeHTTP(secondRecorder, second)
	if secondRecorder.Code != http.StatusCreated {
		t.Fatalf("write slot remained held after timeout: status=%d body=%q", secondRecorder.Code, secondRecorder.Body.String())
	}
}

func TestActiveLongPUTUsesIdleNotAbsoluteDeadline(t *testing.T) {
	started := time.Unix(1_700_000_000, 0)
	clock := &manualClock{now: started}
	writer := &recordingWriteCoordinator{putResult: MutationResult{Created: true}}
	handler := writableDeadlineTestHandler(t, clock, writer)
	body := &advancingBody{clock: clock, chunks: []string{"one", "two", "three"}}
	request := trustedRequest(http.MethodPut, testCapability+"/active.bin", body)
	recorder := newDeadlineRecorder()

	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusCreated || string(writer.putBody) != "onetwothree" {
		t.Fatalf("active PUT = %d/%q", recorder.Code, writer.putBody)
	}
	if elapsed := clock.Now().Sub(started); elapsed <= 10*time.Second {
		t.Fatalf("test did not exceed one idle interval: %s", elapsed)
	}
	deadlines := recorder.Deadlines()
	if len(deadlines) != 8 {
		t.Fatalf("active PUT deadline calls = %v, want set/clear for every read including EOF", deadlines)
	}
	for index := 0; index < len(deadlines); index += 2 {
		if deadlines[index].IsZero() || !deadlines[index+1].IsZero() {
			t.Fatalf("deadline pair %d = %v/%v", index/2, deadlines[index], deadlines[index+1])
		}
		if index >= 2 && !deadlines[index].After(deadlines[index-2]) {
			t.Fatalf("idle deadline was not renewed: %v", deadlines)
		}
	}
}

func TestControlBodyUsesOneBoundedAbsoluteDeadline(t *testing.T) {
	started := time.Unix(1_700_000_000, 0)
	clock := &manualClock{now: started}
	handler := writableDeadlineTestHandler(t, clock, &recordingWriteCoordinator{})
	request := trustedRequest("PROPFIND", testCapability+"/", timeoutBody{})
	request.Header.Set("Depth", "0")
	recorder := newDeadlineRecorder()

	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusRequestTimeout {
		t.Fatalf("stalled PROPFIND status = %d, want 408", recorder.Code)
	}
	deadlines := recorder.Deadlines()
	if len(deadlines) != 2 || !deadlines[0].Equal(started.Add(5*time.Second)) || !deadlines[1].IsZero() {
		t.Fatalf("control-body deadlines = %v", deadlines)
	}
}

func TestBodyDeadlineSetupFailsClosed(t *testing.T) {
	clock := &manualClock{now: time.Unix(1_700_000_000, 0)}
	handler := writableDeadlineTestHandler(t, clock, &recordingWriteCoordinator{})
	for _, test := range []struct {
		name    string
		method  string
		target  string
		body    string
		headers map[string]string
	}{
		{name: "streamed PUT", method: http.MethodPut, target: "/file", body: "data"},
		{name: "bounded control body", method: "PROPFIND", target: "/", body: `<D:propfind xmlns:D="DAV:"/>`, headers: map[string]string{"Depth": "0"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := trustedRequest(test.method, testCapability+test.target, strings.NewReader(test.body))
			for name, value := range test.headers {
				request.Header.Set(name, value)
			}
			recorder := httptest.NewRecorder() // ResponseController cannot set a socket deadline.

			handler.ServeHTTP(recorder, request)

			if recorder.Code != http.StatusInternalServerError {
				t.Fatalf("unsupported deadline status = %d, want fail-closed 500", recorder.Code)
			}
		})
	}
}

func TestBodyReadTimeoutClassification(t *testing.T) {
	if !isBodyReadTimeout(timeoutReadError{}) {
		t.Fatal("timeout net.Error was not classified")
	}
	if isBodyReadTimeout(errors.New("ordinary")) {
		t.Fatal("ordinary error was classified as timeout")
	}
}
