package mountdav

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"
)

type blockedUploadCoordinator struct {
	*recordingWriteCoordinator
	started chan struct{}
	proceed chan struct{}
	once    sync.Once
}

func (writer *blockedUploadCoordinator) Put(ctx context.Context, request PutRequest, body io.Reader) (MutationResult, error) {
	first := false
	writer.once.Do(func() { first = true; close(writer.started) })
	if first {
		select {
		case <-ctx.Done():
			return MutationResult{}, ctx.Err()
		case <-writer.proceed:
		}
	}
	return writer.recordingWriteCoordinator.Put(ctx, request, body)
}

func TestUploadOwnerCanRefreshWithoutAllowingCompetingWrites(t *testing.T) {
	for _, tc := range []struct {
		name    string
		failure error
		cancel  bool
		status  int
	}{
		{"success", nil, false, http.StatusCreated},
		{"remote failure", ErrWriteUnavailable, false, http.StatusServiceUnavailable},
		{"cancelled", nil, true, http.StatusRequestTimeout},
	} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				writer := &blockedUploadCoordinator{recordingWriteCoordinator: &recordingWriteCoordinator{putResult: MutationResult{Created: true}, err: tc.failure}, started: make(chan struct{}), proceed: make(chan struct{})}
				handler := newWritableTestHandler(t, writer)
				target := testCapability + "/Docs/upload.mp4"
				lock := trustedRequest("LOCK", target, strings.NewReader(`<D:lockinfo xmlns:D="DAV:"><D:lockscope><D:exclusive/></D:lockscope><D:locktype><D:write/></D:locktype></D:lockinfo>`))
				lock.Header.Set("Depth", "0")
				lock.Header.Set("Timeout", "Second-1")
				lr := httptest.NewRecorder()
				handler.ServeHTTP(lr, lock)
				if lr.Code != http.StatusOK {
					t.Fatalf("initial LOCK=%d", lr.Code)
				}
				token := lr.Header().Get("Lock-Token")
				ctx, cancel := context.WithCancel(t.Context())
				put := trustedRequest(http.MethodPut, target, strings.NewReader("original upload bytes")).WithContext(ctx)
				put.Header.Set("If", "("+token+")")
				result := httptest.NewRecorder()
				done := make(chan struct{})
				go func() { defer close(done); handler.ServeHTTP(result, put) }()
				var finish sync.Once
				unblock := func() { finish.Do(func() { close(writer.proceed) }) }
				t.Cleanup(func() { cancel(); unblock(); <-done })
				<-writer.started
				for _, invalid := range []struct{ target, token string }{
					{testCapability + "/Docs/other.mp4", token},
					{target, "<opaquelocktoken:unknown>"},
				} {
					refresh := trustedRequest("LOCK", invalid.target, nil)
					refresh.Header.Set("If", "("+invalid.token+")")
					rejected := httptest.NewRecorder()
					handler.ServeHTTP(rejected, refresh)
					if rejected.Code != http.StatusPreconditionFailed {
						t.Fatalf("unauthorized refresh=%d, want412", rejected.Code)
					}
				}
				for _, seconds := range []int{1, 600} {
					// Refresh both before and after the original deadline.
					refresh := trustedRequest("LOCK", target, nil)
					refresh.Header.Set("If", "("+token+")")
					refresh.Header.Set("Timeout", fmt.Sprintf("Second-%d", seconds))
					rr := httptest.NewRecorder()
					handler.ServeHTTP(rr, refresh)
					if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), fmt.Sprintf("Second-%d", seconds)) {
						t.Fatalf("owner refresh=%d body=%q", rr.Code, rr.Body.String())
					}
					if seconds == 1 {
						time.Sleep(2 * time.Second)
					}
				}
				for _, competitor := range []struct {
					name, condition string
					status          int
				}{
					{"no token", "", 423}, {"same token", "(" + token + ")", 412}, {"unknown token", "(<opaquelocktoken:unknown>)", 412},
				} {
					request := trustedRequest(http.MethodPut, target, strings.NewReader("competing bytes"))
					if competitor.condition != "" {
						request.Header.Set("If", competitor.condition)
					}
					recorder := httptest.NewRecorder()
					handler.ServeHTTP(recorder, request)
					if recorder.Code != competitor.status {
						t.Fatalf("%s PUT=%d, want%d", competitor.name, recorder.Code, competitor.status)
					}
				}
				unlock := trustedRequest("UNLOCK", target, nil)
				unlock.Header.Set("Lock-Token", token)
				ur := httptest.NewRecorder()
				handler.ServeHTTP(ur, unlock)
				if ur.Code != 423 {
					t.Fatalf("UNLOCK during upload=%d, want423", ur.Code)
				}
				if tc.cancel {
					cancel()
				} else {
					unblock()
				}
				<-done
				if result.Code != tc.status {
					t.Fatalf("upload=%d, want%d; body=%q", result.Code, tc.status, result.Body.String())
				}
				if !tc.cancel && string(writer.putBody) != "original upload bytes" {
					t.Fatalf("uploaded bytes=%q", writer.putBody)
				}
				ur = httptest.NewRecorder()
				handler.ServeHTTP(ur, unlock)
				if ur.Code != http.StatusNoContent {
					t.Fatalf("UNLOCK after upload=%d body=%q", ur.Code, ur.Body.String())
				}
			})
		})
	}
}

func TestUnconditionalUploadKeepsTemporaryLockUntilReturn(t *testing.T) {
	for _, cancelled := range []bool{false, true} {
		t.Run(fmt.Sprintf("cancelled=%v", cancelled), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				writer := &blockedUploadCoordinator{recordingWriteCoordinator: &recordingWriteCoordinator{putResult: MutationResult{Created: true}}, started: make(chan struct{}), proceed: make(chan struct{})}
				handler := newWritableTestHandler(t, writer)
				target := testCapability + "/Docs/temporary.mp4"
				ctx, cancel := context.WithCancel(t.Context())
				request := trustedRequest(http.MethodPut, target, strings.NewReader("original bytes")).WithContext(ctx)
				recorder := httptest.NewRecorder()
				done := make(chan struct{})
				go func() { defer close(done); handler.ServeHTTP(recorder, request) }()
				var finish sync.Once
				unblock := func() { finish.Do(func() { close(writer.proceed) }) }
				t.Cleanup(func() { cancel(); unblock(); <-done })
				<-writer.started
				time.Sleep(temporaryLockDuration + time.Second)
				competitor := trustedRequest(http.MethodPut, target, strings.NewReader("replacement bytes"))
				blocked := httptest.NewRecorder()
				handler.ServeHTTP(blocked, competitor)
				if blocked.Code != 423 {
					t.Fatalf("competing upload=%d, want423", blocked.Code)
				}
				if cancelled {
					cancel()
				} else {
					unblock()
				}
				<-done
				want := http.StatusCreated
				if cancelled {
					want = http.StatusRequestTimeout
				}
				if recorder.Code != want {
					t.Fatalf("upload=%d, want%d", recorder.Code, want)
				}
				if !cancelled && string(writer.putBody) != "original bytes" {
					t.Fatalf("original upload changed=%q", writer.putBody)
				}
				unblock()
				after := httptest.NewRecorder()
				handler.ServeHTTP(after, competitor)
				if after.Code != http.StatusCreated || string(writer.putBody) != "replacement bytes" {
					t.Fatalf("completed upload retained temporary lock: status=%d bytes=%q", after.Code, writer.putBody)
				}
			})
		})
	}
}
