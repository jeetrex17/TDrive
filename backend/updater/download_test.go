package updater

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func TestDownloadFileVerifiesAndRenames(t *testing.T) {
	payload := make([]byte, 3*downloadBufferSize+17)
	for i := range payload {
		payload[i] = byte(i)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(payload)
	}))
	defer server.Close()

	dest := filepath.Join(t.TempDir(), "TDrive-v1.7.0-macos-arm64.zip")
	var last, calls int64
	err := downloadFile(context.Background(), server.Client(), "ua", server.URL, dest, int64(len(payload)), sha256Hex(payload), func(done, total int64) {
		calls++
		if done < last {
			t.Errorf("progress went backwards: %d after %d", done, last)
		}
		if total != int64(len(payload)) {
			t.Errorf("total = %d, want %d", total, len(payload))
		}
		last = done
	})
	if err != nil {
		t.Fatalf("downloadFile: %v", err)
	}
	if calls == 0 || last != int64(len(payload)) {
		t.Fatalf("progress calls=%d last=%d", calls, last)
	}
	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatalf("read dest: %v", err)
	}
	if string(got) != string(payload) {
		t.Fatalf("payload mismatch")
	}
	if _, err := os.Stat(dest + partSuffix); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf(".part file left behind: %v", err)
	}
	if err := verifyFile(dest, int64(len(payload)), sha256Hex(payload)); err != nil {
		t.Fatalf("verifyFile: %v", err)
	}
	if err := verifyFile(dest, int64(len(payload)), sha256Hex([]byte("other"))); !errors.Is(err, ErrChecksumMismatch) {
		t.Fatalf("verifyFile with wrong digest = %v", err)
	}
}

func TestDownloadFileRejectsChecksumMismatch(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("tampered"))
	}))
	defer server.Close()

	dest := filepath.Join(t.TempDir(), "payload.zip")
	err := downloadFile(context.Background(), server.Client(), "", server.URL, dest, 0, sha256Hex([]byte("expected")), nil)
	if !errors.Is(err, ErrChecksumMismatch) {
		t.Fatalf("err = %v, want ErrChecksumMismatch", err)
	}
	for _, path := range []string{dest, dest + partSuffix} {
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("%s should not exist after a failed verification", path)
		}
	}
}

func TestDownloadFileRejectsSizeMismatch(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("short"))
	}))
	defer server.Close()
	dest := filepath.Join(t.TempDir(), "payload.zip")
	err := downloadFile(context.Background(), server.Client(), "", server.URL, dest, 99, sha256Hex([]byte("short")), nil)
	if err == nil {
		t.Fatalf("expected size mismatch error")
	}
	if _, statErr := os.Stat(dest); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("dest must not exist after size mismatch")
	}
}

func TestDownloadFileHonoursCancellation(t *testing.T) {
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "1000000")
		_, _ = w.Write(make([]byte, 1024))
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		<-release
	}))
	defer server.Close()
	defer close(release)

	ctx, cancel := context.WithCancel(context.Background())
	dest := filepath.Join(t.TempDir(), "payload.zip")
	done := make(chan error, 1)
	go func() {
		done <- downloadFile(ctx, server.Client(), "", server.URL, dest, 0, sha256Hex(nil), func(got, total int64) {
			if got > 0 {
				cancel()
			}
		})
	}()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("err = %v, want context.Canceled", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("download did not stop after cancellation")
	}
	if _, err := os.Stat(dest + partSuffix); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf(".part must be removed after cancellation")
	}
}

func TestDownloadFileRequiresChecksum(t *testing.T) {
	if err := downloadFile(context.Background(), http.DefaultClient, "", "http://127.0.0.1:1", filepath.Join(t.TempDir(), "x"), 0, "", nil); err == nil {
		t.Fatalf("download without an expected checksum must fail closed")
	}
}

func TestDownloadFileTimesOutWaitingForResponseHeaders(t *testing.T) {
	release := make(chan struct{})
	requestCanceled := make(chan bool, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
			requestCanceled <- true
		case <-release:
			requestCanceled <- false
		}
	}))
	t.Cleanup(func() {
		close(release)
		server.Close()
	})

	dest := filepath.Join(t.TempDir(), "payload.zip")
	result := make(chan error, 1)
	go func() {
		result <- downloadFileWithLimits(
			context.Background(),
			server.Client(),
			"",
			server.URL,
			dest,
			1,
			sha256Hex([]byte{0}),
			nil,
			downloadLimits{
				responseHeaderTimeout: 100 * time.Millisecond,
				maxBytes:              maxDownloadBytes,
			},
		)
	}()

	select {
	case err := <-result:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("err = %v, want context.DeadlineExceeded", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatalf("download did not time out while waiting for response headers")
	}

	select {
	case canceled := <-requestCanceled:
		if !canceled {
			t.Fatalf("header timeout did not cancel the request")
		}
	case <-time.After(3 * time.Second):
		t.Fatalf("server did not observe request cancellation")
	}
	if _, err := os.Stat(dest + partSuffix); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf(".part must not exist after a response-header timeout")
	}
}

func TestDownloadFileResponseHeaderTimeoutDoesNotBoundBody(t *testing.T) {
	payload := []byte("body arrived after headers")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		time.Sleep(250 * time.Millisecond)
		_, _ = w.Write(payload)
	}))
	defer server.Close()

	err := downloadFileWithLimits(
		context.Background(),
		server.Client(),
		"",
		server.URL,
		filepath.Join(t.TempDir(), "payload.zip"),
		int64(len(payload)),
		sha256Hex(payload),
		nil,
		downloadLimits{
			responseHeaderTimeout: 100 * time.Millisecond,
			maxBytes:              maxDownloadBytes,
		},
	)
	if err != nil {
		t.Fatalf("downloadFileWithLimits: %v", err)
	}
}

func TestDownloadFileRejectsExpectedSizeAboveHardCapBeforeRequest(t *testing.T) {
	if maxDownloadBytes != 1<<30 {
		t.Fatalf("maxDownloadBytes = %d, want 1 GiB", maxDownloadBytes)
	}

	requested := make(chan struct{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requested <- struct{}{}
	}))
	defer server.Close()

	err := downloadFile(
		context.Background(),
		server.Client(),
		"",
		server.URL,
		filepath.Join(t.TempDir(), "payload.zip"),
		maxDownloadBytes+1,
		sha256Hex(nil),
		nil,
	)
	if err == nil {
		t.Fatalf("expected an oversized release asset to be rejected")
	}
	select {
	case <-requested:
		t.Fatalf("oversized release asset triggered a network request")
	default:
	}
}

func TestDownloadFileStopsChunkedResponseAtSizeLimit(t *testing.T) {
	tests := []struct {
		name     string
		wantSize int64
		limits   downloadLimits
	}{
		{
			name:     "expected asset size",
			wantSize: 32,
			limits: downloadLimits{
				responseHeaderTimeout: githubTimeout,
				maxBytes:              maxDownloadBytes,
			},
		},
		{
			name:     "hard cap when expected size is unknown",
			wantSize: 0,
			limits: downloadLimits{
				responseHeaderTimeout: githubTimeout,
				maxBytes:              32,
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			streamLimit := test.limits.maxBytes
			if test.wantSize > 0 {
				streamLimit = test.wantSize
			}
			release := make(chan struct{})
			requestCanceled := make(chan bool, 1)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				flusher, ok := w.(http.Flusher)
				if !ok {
					t.Errorf("response writer does not support flushing")
					return
				}
				w.WriteHeader(http.StatusOK)
				flusher.Flush()
				_, _ = w.Write(make([]byte, streamLimit+1))
				flusher.Flush()

				select {
				case <-r.Context().Done():
					requestCanceled <- true
				case <-release:
					requestCanceled <- false
				}
			}))
			t.Cleanup(func() {
				close(release)
				server.Close()
			})

			dest := filepath.Join(t.TempDir(), "payload.zip")
			var lastProgress int64
			result := make(chan error, 1)
			go func() {
				result <- downloadFileWithLimits(
					context.Background(),
					server.Client(),
					"",
					server.URL,
					dest,
					test.wantSize,
					sha256Hex(nil),
					func(done, total int64) {
						lastProgress = done
					},
					test.limits,
				)
			}()

			select {
			case err := <-result:
				if err == nil {
					t.Fatalf("expected chunked response to be rejected at the size limit")
				}
			case <-time.After(3 * time.Second):
				t.Fatalf("download did not stop when the chunked response exceeded its size limit")
			}
			if lastProgress > streamLimit {
				t.Fatalf("progress reached %d bytes, limit is %d", lastProgress, streamLimit)
			}
			select {
			case canceled := <-requestCanceled:
				if !canceled {
					t.Fatalf("size-limit failure did not cancel the request")
				}
			case <-time.After(3 * time.Second):
				t.Fatalf("server did not observe request cancellation")
			}
			for _, path := range []string{dest, dest + partSuffix} {
				if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("%s should not exist after a size-limit failure", path)
				}
			}
		})
	}
}

func TestFetchSmallCapsResponseSize(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(make([]byte, smallAssetLimitBytes+1))
	}))
	defer server.Close()
	if _, err := fetchSmall(context.Background(), server.Client(), "", server.URL); err == nil {
		t.Fatalf("oversized response must be rejected")
	}
}
