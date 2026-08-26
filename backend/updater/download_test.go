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

func TestFetchSmallCapsResponseSize(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(make([]byte, smallAssetLimitBytes+1))
	}))
	defer server.Close()
	if _, err := fetchSmall(context.Background(), server.Client(), "", server.URL); err == nil {
		t.Fatalf("oversized response must be rejected")
	}
}
