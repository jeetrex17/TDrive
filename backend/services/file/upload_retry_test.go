package file

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"TDrive/backend/projection"
	"TDrive/backend/tgclient"
)

// instantRetryPolicy mirrors the production budgets but never actually sleeps.
func instantRetryPolicy() tgclient.FloodWaitRetryPolicy {
	return tgclient.FloodWaitRetryPolicy{
		MaxRetries:          1,
		MaxWait:             time.Second,
		MaxTotalWait:        time.Second,
		Sleep:               func(context.Context, time.Duration) error { return nil },
		MaxTransientRetries: 4,
		TransientBackoff:    time.Millisecond,
	}
}

func TestMultipartUploadRecoversAfterTransientFailure(t *testing.T) {
	svc, db, fakeTG, _ := newTestService(t)
	svc.MaxUploadBytes = 1000 // force splitting above 1000 stored bytes
	svc.FloodWaitRetry = instantRetryPolicy()

	body := bigBody(3503) // -> 4 parts (1000,1000,1000,503)
	path := writeTempNamedFile(t, "movie.bin", body)
	fakeTG.InjectTransientFailures(2)

	files, err := svc.Upload(context.Background(), personalChannelID, []string{path}, []string{""}, false)
	if err != nil {
		t.Fatalf("upload after transient failures: %v", err)
	}
	parts, err := projection.MultipartParts(db, personalChannelID, int64(files[0].MsgID))
	if err != nil {
		t.Fatalf("MultipartParts: %v", err)
	}
	if len(parts) != 4 {
		t.Fatalf("parts = %d, want 4", len(parts))
	}

	savePath := filepath.Join(t.TempDir(), "out.bin")
	result := svc.Download(context.Background(), personalChannelID, files[0].MsgID, files[0].MsgID, func(string) (string, error) {
		return savePath, nil
	})
	if result.Status != "success" {
		t.Fatalf("download = %+v", result)
	}
	got, err := os.ReadFile(savePath)
	if err != nil {
		t.Fatalf("read downloaded file: %v", err)
	}
	if !bytes.Equal(got, body) {
		t.Fatalf("round-trip mismatch after retries: got %d bytes, want %d", len(got), len(body))
	}
}

func TestSingleUploadRecoversAfterTransientFailure(t *testing.T) {
	svc, _, fakeTG, _ := newTestService(t)
	svc.FloodWaitRetry = instantRetryPolicy()

	body := []byte("hello tdrive transient retry")
	path := writeTempNamedFile(t, "note.txt", body)
	fakeTG.InjectTransientFailures(1)

	files, err := svc.Upload(context.Background(), personalChannelID, []string{path}, []string{""}, false)
	if err != nil {
		t.Fatalf("upload after transient failure: %v", err)
	}
	if len(files) != 1 || files[0].Size != int64(len(body)) {
		t.Fatalf("uploaded = %+v, want one %d-byte file", files, len(body))
	}
}

func TestUploadFailsWhenTransientBudgetExhausted(t *testing.T) {
	svc, _, fakeTG, _ := newTestService(t)
	svc.MaxUploadBytes = 1000
	policy := instantRetryPolicy()
	policy.MaxTransientRetries = 2
	svc.FloodWaitRetry = policy

	path := writeTempNamedFile(t, "movie.bin", bigBody(1500))
	fakeTG.InjectTransientFailures(5)

	if _, err := svc.Upload(context.Background(), personalChannelID, []string{path}, []string{""}, false); err == nil {
		t.Fatal("upload succeeded past exhausted transient budget, want failure")
	}
}

// countingClient tracks concurrent SendFile calls while delegating to Fake.
type countingClient struct {
	*tgclient.Fake
	mu       sync.Mutex
	inFlight int
	maxSeen  int
}

func (c *countingClient) SendFile(ctx context.Context, peer tgclient.InputPeer, r io.Reader, name, caption string, totalSize int64, onProgress func(sent, total int64)) (tgclient.SendFileResult, error) {
	c.mu.Lock()
	c.inFlight++
	if c.inFlight > c.maxSeen {
		c.maxSeen = c.inFlight
	}
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		c.inFlight--
		c.mu.Unlock()
	}()
	return c.Fake.SendFile(ctx, peer, r, name, caption, totalSize, onProgress)
}

func TestUploadBatchHonorsConfiguredConcurrency(t *testing.T) {
	const fileCount = 6
	for _, tc := range []struct {
		name    string
		limit   int
		wantMax int
	}{
		{"serial limit", 1, 1},
		{"default limit stays bounded", 0, 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc, _, fakeTG, _ := newTestService(t)
			client := &countingClient{Fake: fakeTG}
			svc.TG = client
			svc.MaxConcurrentUploads = tc.limit

			var paths, parents []string
			for i := 0; i < fileCount; i++ {
				paths = append(paths, writeTempNamedFile(t, "f"+string(rune('a'+i))+".txt", []byte("body")))
				parents = append(parents, "")
			}
			files, err := svc.Upload(context.Background(), personalChannelID, paths, parents, false)
			if err != nil {
				t.Fatalf("upload: %v", err)
			}
			if len(files) != fileCount {
				t.Fatalf("uploaded = %d, want %d", len(files), fileCount)
			}
			if client.maxSeen > tc.wantMax {
				t.Fatalf("max concurrent sends = %d, want <= %d", client.maxSeen, tc.wantMax)
			}
		})
	}
}

func TestUploadConcurrencyClamp(t *testing.T) {
	for _, tc := range []struct {
		configured int
		want       int
	}{
		{configured: 0, want: 3},
		{configured: -2, want: 3},
		{configured: 1, want: 1},
		{configured: 5, want: 5},
		{configured: 99, want: 8},
	} {
		svc := &Service{MaxConcurrentUploads: tc.configured}
		if got := svc.uploadConcurrency(); got != tc.want {
			t.Fatalf("uploadConcurrency(%d) = %d, want %d", tc.configured, got, tc.want)
		}
	}
}
