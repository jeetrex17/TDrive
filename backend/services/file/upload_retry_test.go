package file

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	tdcrypto "TDrive/backend/crypto"
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

func configureEncryptedUpload(t *testing.T, svc *Service, masterKey []byte) {
	t.Helper()
	svc.MasterKeyForUpload = func(channelID int64, wantEncrypted bool) ([]byte, error) {
		if !wantEncrypted {
			return nil, nil
		}
		return append([]byte(nil), masterKey...), nil
	}
	svc.WriteCiphertextTemp = func(plain io.Reader, plaintextSize int64, key []byte) (*os.File, error) {
		tmp, err := os.CreateTemp("", "tdrive-test-cipher-*")
		if err != nil {
			return nil, err
		}
		if err := tdcrypto.EncryptStream(plain, tmp, key, plaintextSize); err != nil {
			_ = tmp.Close()
			_ = os.Remove(tmp.Name())
			return nil, err
		}
		if _, err := tmp.Seek(0, io.SeekStart); err != nil {
			_ = tmp.Close()
			_ = os.Remove(tmp.Name())
			return nil, err
		}
		return tmp, nil
	}
	svc.RequireEncryptionKey = func(encrypted bool) ([]byte, error) {
		if !encrypted {
			return nil, nil
		}
		return append([]byte(nil), masterKey...), nil
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
	fileFailAt      int
	controlFailAt   int
	controlFailAll  bool
	calls           int
	controlCalls    int
	randomIDs       []int64
	controlRandomID []int64
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
	if c.calls == c.fileFailAt {
		return tgclient.SendFileResult{}, errors.Join(tgclient.ErrSendOutcomeUnknown, tgclient.ErrInjectedTransport)
	}
	return result, nil
}

func (c *visibleAcceptThenLoseReceiptClient) SendControlWithRandomID(
	ctx context.Context,
	peer tgclient.InputPeer,
	text string,
	silent bool,
	randomID int64,
) (int64, error) {
	c.controlCalls++
	c.controlRandomID = append(c.controlRandomID, randomID)
	msgID, err := c.Fake.SendControlWithRandomID(ctx, peer, text, silent, randomID)
	if err != nil {
		return msgID, err
	}
	if c.controlCalls == c.controlFailAt || c.controlFailAll {
		return 0, errors.Join(tgclient.ErrSendOutcomeUnknown, tgclient.ErrInjectedTransport)
	}
	return msgID, nil
}

func TestSingleUploadRetriesLostAcceptedReceiptIdempotently(t *testing.T) {
	svc, _, fakeTG, _ := newTestService(t)
	svc.FloodWaitRetry = instantRetryPolicy()
	client := &visibleAcceptThenLoseReceiptClient{Fake: fakeTG, fileFailAt: 1}
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
	client := &visibleAcceptThenLoseReceiptClient{Fake: fakeTG, fileFailAt: 2}
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

func TestMultipartManifestRetriesLostAcceptedReceiptIdempotently(t *testing.T) {
	svc, db, fakeTG, _ := newTestService(t)
	svc.MaxUploadBytes = 1000
	svc.FloodWaitRetry = instantRetryPolicy()
	client := &visibleAcceptThenLoseReceiptClient{Fake: fakeTG, controlFailAt: 1}
	svc.TG = client

	path := writeTempNamedFile(t, "manifest-receipt-lost.bin", bigBody(2500))
	files, err := svc.Upload(context.Background(), personalChannelID, []string{path}, []string{""}, false)
	if err != nil {
		t.Fatalf("multipart upload after lost manifest receipt: %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("uploaded files = %+v, want one", files)
	}
	if client.controlCalls != 2 {
		t.Fatalf("manifest send attempts = %d, want two", client.controlCalls)
	}
	if len(client.controlRandomID) != 2 || client.controlRandomID[0] <= 0 || client.controlRandomID[0] != client.controlRandomID[1] {
		t.Fatalf("manifest retry random ids = %v, want the same positive id", client.controlRandomID)
	}
	if controls := fakeTG.SentControls(); len(controls) != 1 {
		t.Fatalf("accepted manifests = %+v, want exactly one", controls)
	} else if files[0].MsgID != int(controls[0].MsgID) {
		t.Fatalf("manifest msg id = %d, want accepted id %d", files[0].MsgID, controls[0].MsgID)
	}
	parts, err := projection.MultipartParts(db, personalChannelID, int64(files[0].MsgID))
	if err != nil {
		t.Fatalf("MultipartParts: %v", err)
	}
	if len(parts) != 3 {
		t.Fatalf("projected parts = %d, want three", len(parts))
	}
}

func TestMultipartManifestUnknownOutcomeKeepsPartsForReconcile(t *testing.T) {
	svc, db, fakeTG, _ := newTestService(t)
	svc.MaxUploadBytes = 1000
	policy := instantRetryPolicy()
	policy.MaxTransientRetries = 2
	svc.FloodWaitRetry = policy
	client := &visibleAcceptThenLoseReceiptClient{Fake: fakeTG, controlFailAll: true}
	svc.TG = client

	path := writeTempNamedFile(t, "manifest-unknown.bin", bigBody(2500))
	if _, err := svc.Upload(context.Background(), personalChannelID, []string{path}, []string{""}, false); !errors.Is(err, tgclient.ErrSendOutcomeUnknown) {
		t.Fatalf("upload error = %v, want unknown manifest outcome", err)
	}
	if controls := fakeTG.SentControls(); len(controls) != 1 {
		t.Fatalf("accepted manifests = %+v, want exactly one accepted manifest", controls)
	}
	if files := fakeTG.SentFiles(); len(files) != 3 {
		t.Fatalf("accepted parts = %+v, want three parts retained", files)
	}
	if deleted := fakeTG.DeletedBatches(); len(deleted) != 0 {
		t.Fatalf("deleted batches = %+v, want no part cleanup while manifest outcome is unknown", deleted)
	}
	var partRows int
	if err := db.QueryRow(`SELECT COUNT(*) FROM file_parts WHERE channel_id = ?`, personalChannelID).Scan(&partRows); err != nil {
		t.Fatalf("count file_parts: %v", err)
	}
	if partRows != 3 {
		t.Fatalf("file_parts rows = %d, want three retained parts", partRows)
	}
}

func TestMultipartManifestCancellationAfterSendKeepsParts(t *testing.T) {
	svc, db, fakeTG, _ := newTestService(t)
	svc.MaxUploadBytes = 1000
	svc.FloodWaitRetry = tgclient.FloodWaitRetryPolicy{
		MaxTransientRetries: 1,
		TransientBackoff:    time.Millisecond,
		Sleep: func(context.Context, time.Duration) error {
			return context.Canceled
		},
	}
	client := &visibleAcceptThenLoseReceiptClient{Fake: fakeTG, controlFailAll: true}
	svc.TG = client

	path := writeTempNamedFile(t, "manifest-canceled.bin", bigBody(2500))
	if _, err := svc.Upload(context.Background(), personalChannelID, []string{path}, []string{""}, false); !errors.Is(err, context.Canceled) {
		t.Fatalf("upload error = %v, want cancellation from retry backoff", err)
	}
	if deleted := fakeTG.DeletedBatches(); len(deleted) != 0 {
		t.Fatalf("deleted batches = %+v, want no cleanup after manifest send started", deleted)
	}
	var partRows int
	if err := db.QueryRow(`SELECT COUNT(*) FROM file_parts WHERE channel_id = ?`, personalChannelID).Scan(&partRows); err != nil {
		t.Fatalf("count file_parts: %v", err)
	}
	if partRows != 3 {
		t.Fatalf("file_parts rows = %d, want three retained parts", partRows)
	}
}

type midstreamTransientClient struct {
	*tgclient.Fake
	mu     sync.Mutex
	calls  int
	failAt int
}

type transientDownloadClient struct {
	*tgclient.Fake
	mu         sync.Mutex
	failStream bool
	failAt     bool
}

func (c *transientDownloadClient) takeFailure(stream bool) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if stream && c.failStream {
		c.failStream = false
		return true
	}
	if !stream && c.failAt {
		c.failAt = false
		return true
	}
	return false
}

func (c *transientDownloadClient) DownloadFile(
	ctx context.Context,
	peer tgclient.InputPeer,
	msgID int64,
	dst io.Writer,
	progress func(int64, int64),
) error {
	if !c.takeFailure(true) {
		return c.Fake.DownloadFile(ctx, peer, msgID, dst, progress)
	}
	var body bytes.Buffer
	if err := c.Fake.DownloadFile(ctx, peer, msgID, &body, nil); err != nil {
		return err
	}
	half := max(1, body.Len()/2)
	n, err := dst.Write(body.Bytes()[:half])
	if progress != nil {
		progress(int64(n), int64(body.Len()))
	}
	if err != nil {
		return err
	}
	return tgclient.ErrInjectedTransport
}

func (c *transientDownloadClient) DownloadFileAt(
	ctx context.Context,
	peer tgclient.InputPeer,
	msgID int64,
	dst io.WriterAt,
	baseOffset int64,
	progress func(int64, int64),
) error {
	if !c.takeFailure(false) {
		return c.Fake.DownloadFileAt(ctx, peer, msgID, dst, baseOffset, progress)
	}
	var body bytes.Buffer
	if err := c.Fake.DownloadFile(ctx, peer, msgID, &body, nil); err != nil {
		return err
	}
	half := max(1, body.Len()/2)
	n, err := dst.WriteAt(body.Bytes()[:half], baseOffset)
	if progress != nil {
		progress(int64(n), int64(body.Len()))
	}
	if err != nil {
		return err
	}
	return tgclient.ErrInjectedTransport
}

func (c *midstreamTransientClient) SendFileWithRandomID(
	ctx context.Context,
	peer tgclient.InputPeer,
	source io.Reader,
	name string,
	caption string,
	size int64,
	progress func(int64, int64),
	randomID int64,
) (tgclient.SendFileResult, error) {
	c.mu.Lock()
	c.calls++
	call := c.calls
	c.mu.Unlock()
	if call == c.failAt {
		consumed, _ := io.CopyN(io.Discard, source, min(size, 37))
		if progress != nil {
			progress(consumed, size)
		}
		return tgclient.SendFileResult{}, tgclient.ErrInjectedTransport
	}
	return c.Fake.SendFileWithRandomID(ctx, peer, source, name, caption, size, progress, randomID)
}

func TestEncryptedMultipartRetriesAfterMidstreamRead(t *testing.T) {
	svc, db, fakeTG, _ := newTestService(t)
	svc.MaxUploadBytes = 1000
	svc.FloodWaitRetry = instantRetryPolicy()
	configureEncryptedUpload(t, svc, bytes.Repeat([]byte{6}, 32))
	client := &midstreamTransientClient{Fake: fakeTG, failAt: 2}
	svc.TG = client

	body := bigBody(2500)
	path := writeTempNamedFile(t, "encrypted-midstream.bin", body)
	files, err := svc.Upload(context.Background(), personalChannelID, []string{path}, []string{""}, true)
	if err != nil {
		t.Fatalf("encrypted multipart upload: %v", err)
	}
	parts, err := projection.MultipartParts(db, personalChannelID, int64(files[0].MsgID))
	if err != nil {
		t.Fatalf("MultipartParts: %v", err)
	}
	if len(parts) != 3 {
		t.Fatalf("parts = %d, want three", len(parts))
	}

	svc.TG = &transientDownloadClient{Fake: fakeTG, failAt: true}
	savePath := filepath.Join(t.TempDir(), "round-trip.bin")
	result := svc.Download(context.Background(), personalChannelID, files[0].MsgID, files[0].MsgID, func(string) (string, error) {
		return savePath, nil
	})
	if result.Status != "success" {
		t.Fatalf("download = %+v", result)
	}
	got, err := os.ReadFile(savePath)
	if err != nil {
		t.Fatalf("read round trip: %v", err)
	}
	if !bytes.Equal(got, body) {
		t.Fatalf("encrypted round trip mismatch: got %d bytes, want %d", len(got), len(body))
	}
}

func TestPreviewRetriesIntoFreshBufferAfterPartialDownload(t *testing.T) {
	svc, _, fakeTG, _ := newTestService(t)
	svc.FloodWaitRetry = instantRetryPolicy()
	path := writeTempNamedFile(t, "preview.png", tinyPNG)
	files, err := svc.Upload(context.Background(), personalChannelID, []string{path}, []string{""}, false)
	if err != nil {
		t.Fatalf("upload preview fixture: %v", err)
	}
	svc.TG = &transientDownloadClient{Fake: fakeTG, failStream: true}

	payload, err := svc.PreviewFile(context.Background(), personalChannelID, files[0].MsgID)
	if err != nil {
		t.Fatalf("PreviewFile: %v", err)
	}
	got, err := base64.StdEncoding.DecodeString(payload.DataBase64)
	if err != nil {
		t.Fatalf("decode preview: %v", err)
	}
	if !bytes.Equal(got, tinyPNG) {
		t.Fatalf("preview retry bytes = %x, want %x", got, tinyPNG)
	}
}

func TestEncryptedMultipartRetriesLostAcceptedReceiptIdempotently(t *testing.T) {
	svc, db, fakeTG, _ := newTestService(t)
	svc.MaxUploadBytes = 1000
	svc.FloodWaitRetry = instantRetryPolicy()
	configureEncryptedUpload(t, svc, bytes.Repeat([]byte{4}, 32))
	client := &visibleAcceptThenLoseReceiptClient{Fake: fakeTG, fileFailAt: 2}
	svc.TG = client

	body := bigBody(2500)
	path := writeTempNamedFile(t, "encrypted-receipt-lost.bin", body)
	files, err := svc.Upload(context.Background(), personalChannelID, []string{path}, []string{""}, true)
	if err != nil {
		t.Fatalf("encrypted multipart upload after lost accepted receipt: %v", err)
	}
	if client.calls != 4 {
		t.Fatalf("file send attempts = %d, want four", client.calls)
	}
	if len(client.randomIDs) != 4 || client.randomIDs[1] <= 0 || client.randomIDs[1] != client.randomIDs[2] {
		t.Fatalf("encrypted part retry random ids = %v, want the same positive id", client.randomIDs)
	}
	if sent := fakeTG.SentFiles(); len(sent) != 3 {
		t.Fatalf("accepted encrypted parts = %+v, want exactly three", sent)
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
	client := &visibleAcceptThenLoseReceiptClient{Fake: fakeTG, fileFailAt: 1}
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
	delay    time.Duration
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
	if c.delay > 0 {
		time.Sleep(c.delay)
	}
	return c.Fake.SendFile(ctx, peer, r, name, caption, totalSize, onProgress)
}

func (c *countingClient) SendFileWithRandomID(ctx context.Context, peer tgclient.InputPeer, r io.Reader, name, caption string, totalSize int64, onProgress func(sent, total int64), randomID int64) (tgclient.SendFileResult, error) {
	defer c.beginSend()()
	if c.delay > 0 {
		time.Sleep(c.delay)
	}
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

func TestConcurrentUploadCallsShareConfiguredLimiter(t *testing.T) {
	svc, _, fakeTG, _ := newTestService(t)
	client := &countingClient{Fake: fakeTG, delay: 20 * time.Millisecond}
	svc.TG = client
	svc.MaxConcurrentUploads = 1

	pathA := writeTempNamedFile(t, "a.txt", []byte("alpha"))
	pathB := writeTempNamedFile(t, "b.txt", []byte("bravo"))
	errCh := make(chan error, 2)
	for _, path := range []string{pathA, pathB} {
		path := path
		go func() {
			_, err := svc.Upload(context.Background(), personalChannelID, []string{path}, []string{""}, false)
			errCh <- err
		}()
	}
	for i := 0; i < 2; i++ {
		if err := <-errCh; err != nil {
			t.Fatalf("upload %d: %v", i, err)
		}
	}
	if client.maxSeen > 1 {
		t.Fatalf("max concurrent sends across Upload calls = %d, want <= 1", client.maxSeen)
	}
}

func TestVisibleAndHiddenUploadsShareConfiguredLimiter(t *testing.T) {
	svc, _, fakeTG, _ := newTestService(t)
	client := &countingClient{Fake: fakeTG, delay: 20 * time.Millisecond}
	svc.TG = client
	svc.MaxConcurrentUploads = 1

	visiblePath := writeTempNamedFile(t, "visible.txt", []byte("visible"))
	hiddenBody := []byte("hidden")
	start := make(chan struct{})
	errCh := make(chan error, 2)
	go func() {
		<-start
		_, err := svc.Upload(context.Background(), personalChannelID, []string{visiblePath}, []string{""}, false)
		errCh <- err
	}()
	go func() {
		<-start
		_, err := svc.UploadHidden(
			context.Background(),
			personalChannelID,
			HiddenUploadRequest{
				OperationID:   "global-upload-limit-hidden",
				Name:          "hidden.txt",
				StoredSize:    int64(len(hiddenBody)),
				PlaintextSize: int64(len(hiddenBody)),
			},
			bytes.NewReader(hiddenBody),
		)
		errCh <- err
	}()
	close(start)
	for i := 0; i < 2; i++ {
		if err := <-errCh; err != nil {
			t.Fatalf("upload %d: %v", i, err)
		}
	}
	if client.maxSeen > 1 {
		t.Fatalf("max concurrent visible/hidden sends = %d, want <= 1", client.maxSeen)
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
