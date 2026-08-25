package file

import (
	"bytes"
	"context"
	"errors"
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

// visibleAcceptThenLoseReceiptClient simulates MessagesSendMedia accepting a
// document before its response is lost with a retryable transport error.
// SendFile deliberately creates a fresh id on each call, matching a legacy
// non-idempotent client. SendFileWithRandomID lets the fake deduplicate a
// retry made through the idempotent extension.
type visibleAcceptThenLoseReceiptClient struct {
	*tgclient.Fake
	failAt    int
	calls     int
	randomIDs []int64
}

func (c *visibleAcceptThenLoseReceiptClient) SendFile(
	ctx context.Context,
	peer tgclient.InputPeer,
	source io.Reader,
	name string,
	caption string,
	size int64,
	progress func(int64, int64),
) (tgclient.SendFileResult, error) {
	return c.SendFileWithRandomID(ctx, peer, source, name, caption, size, progress, int64(c.calls+1))
}

func (c *visibleAcceptThenLoseReceiptClient) SendFileWithRandomID(
	ctx context.Context,
	peer tgclient.InputPeer,
	source io.Reader,
	name string,
	caption string,
	size int64,
	progress func(int64, int64),
	randomID int64,
) (tgclient.SendFileResult, error) {
	c.calls++
	c.randomIDs = append(c.randomIDs, randomID)
	result, err := c.Fake.SendFileWithRandomID(ctx, peer, source, name, caption, size, progress, randomID)
	if err != nil {
		return result, err
	}
	if c.calls == c.failAt {
		return tgclient.SendFileResult{}, errors.Join(tgclient.ErrSendOutcomeUnknown, tgclient.ErrInjectedTransport)
	}
	return result, nil
}

func TestSingleUploadRetriesLostAcceptedReceiptIdempotently(t *testing.T) {
	svc, _, fakeTG, _ := newTestService(t)
	svc.FloodWaitRetry = instantRetryPolicy()
	client := &visibleAcceptThenLoseReceiptClient{Fake: fakeTG, failAt: 1}
	svc.TG = client

	path := writeTempNamedFile(t, "receipt-lost.txt", []byte("one accepted body"))
	files, err := svc.Upload(context.Background(), personalChannelID, []string{path}, []string{""}, false)
	if err != nil {
		t.Fatalf("upload after lost accepted receipt: %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("uploaded files = %+v, want one", files)
	}
	if client.calls != 2 {
		t.Fatalf("send attempts = %d, want two", client.calls)
	}
	if len(client.randomIDs) != 2 || client.randomIDs[0] <= 0 || client.randomIDs[0] != client.randomIDs[1] {
		t.Fatalf("retry random ids = %v, want the same positive id", client.randomIDs)
	}
	sent := fakeTG.SentFiles()
	if len(sent) != 1 {
		t.Fatalf("accepted sends = %+v, want exactly one", sent)
	}
	if files[0].MsgID != int(sent[0].MsgID) {
		t.Fatalf("uploaded msg id = %d, want original receipt %d", files[0].MsgID, sent[0].MsgID)
	}
}

func TestPlaintextMultipartRetriesLostAcceptedReceiptIdempotently(t *testing.T) {
	svc, db, fakeTG, _ := newTestService(t)
	svc.MaxUploadBytes = 1000
	svc.FloodWaitRetry = instantRetryPolicy()
	client := &visibleAcceptThenLoseReceiptClient{Fake: fakeTG, failAt: 2}
	svc.TG = client

	path := writeTempNamedFile(t, "receipt-lost.bin", bigBody(2500))
	files, err := svc.Upload(context.Background(), personalChannelID, []string{path}, []string{""}, false)
	if err != nil {
		t.Fatalf("multipart upload after lost accepted receipt: %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("uploaded files = %+v, want one", files)
	}
	if client.calls != 4 {
		t.Fatalf("send attempts = %d, want four", client.calls)
	}
	if len(client.randomIDs) != 4 || client.randomIDs[1] <= 0 || client.randomIDs[1] != client.randomIDs[2] {
		t.Fatalf("second part retry random ids = %v, want the same positive id", client.randomIDs)
	}
	sent := fakeTG.SentFiles()
	if len(sent) != 3 {
		t.Fatalf("accepted sends = %+v, want exactly three parts", sent)
	}
	parts, err := projection.MultipartParts(db, personalChannelID, int64(files[0].MsgID))
	if err != nil {
		t.Fatalf("MultipartParts: %v", err)
	}
	if len(parts) != 3 {
		t.Fatalf("projected parts = %d, want three", len(parts))
	}
}

func TestVisibleUploadDoesNotRetryUnknownOutcomeForLegacyClient(t *testing.T) {
	svc, _, fakeTG, _ := newTestService(t)
	svc.FloodWaitRetry = instantRetryPolicy()
	client := &visibleAcceptThenLoseReceiptClient{Fake: fakeTG, failAt: 1}
	// Hide the idempotent extension to emulate an older Client implementation.
	svc.TG = struct{ tgclient.Client }{Client: client}

	path := writeTempNamedFile(t, "legacy-receipt-lost.txt", []byte("accepted legacy body"))
	_, _, _, err := svc.uploadSingle(context.Background(), 0, path, "", personalChannelID, false)
	if !errors.Is(err, tgclient.ErrSendOutcomeUnknown) {
		t.Fatalf("upload error = %v, want unknown send outcome", err)
	}
	if client.calls != 1 {
		t.Fatalf("legacy send attempts = %d, want one", client.calls)
	}
	if sent := fakeTG.SentFiles(); len(sent) != 1 {
		t.Fatalf("legacy accepted sends = %+v, want exactly one", sent)
	}
}

func TestVisibleUploadRetriesPreSendTransientFailureForLegacyClient(t *testing.T) {
	svc, _, fakeTG, _ := newTestService(t)
	svc.FloodWaitRetry = instantRetryPolicy()
	// Hide the idempotent extension while retaining the legacy SendFile path.
	svc.TG = struct{ tgclient.Client }{Client: fakeTG}
	fakeTG.InjectTransientFailures(1)

	path := writeTempNamedFile(t, "legacy-transient.txt", []byte("retry before send"))
	files, err := svc.Upload(context.Background(), personalChannelID, []string{path}, []string{""}, false)
	if err != nil {
		t.Fatalf("legacy upload after pre-send transient failure: %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("uploaded files = %+v, want one", files)
	}
	if sent := fakeTG.SentFiles(); len(sent) != 1 {
		t.Fatalf("legacy accepted sends = %+v, want exactly one", sent)
	}
}

// countingClient tracks concurrent SendFile calls while delegating to Fake.
type countingClient struct {
	*tgclient.Fake
	mu       sync.Mutex
	inFlight int
	maxSeen  int
}

func (c *countingClient) beginSend() func() {
	c.mu.Lock()
	c.inFlight++
	if c.inFlight > c.maxSeen {
		c.maxSeen = c.inFlight
	}
	c.mu.Unlock()
	return func() {
		c.mu.Lock()
		c.inFlight--
		c.mu.Unlock()
	}
}

func (c *countingClient) SendFile(ctx context.Context, peer tgclient.InputPeer, r io.Reader, name, caption string, totalSize int64, onProgress func(sent, total int64)) (tgclient.SendFileResult, error) {
	defer c.beginSend()()
	return c.Fake.SendFile(ctx, peer, r, name, caption, totalSize, onProgress)
}

func (c *countingClient) SendFileWithRandomID(ctx context.Context, peer tgclient.InputPeer, r io.Reader, name, caption string, totalSize int64, onProgress func(sent, total int64), randomID int64) (tgclient.SendFileResult, error) {
	defer c.beginSend()()
	return c.Fake.SendFileWithRandomID(ctx, peer, r, name, caption, totalSize, onProgress, randomID)
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
