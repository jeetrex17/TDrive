package file

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
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

func TestMultipartManifestProjectionFailureReturnsOpForLocalRetry(t *testing.T) {
	svc, db, _, actorID := newTestService(t)
	svc.MaxUploadBytes = 1000
	if _, err := db.Exec(`
		CREATE TRIGGER fail_visible_manifest_projection
		BEFORE INSERT ON files
		WHEN NEW.upload_uuid != ''
		BEGIN
			SELECT RAISE(ABORT, 'injected manifest projection failure');
		END;
	`); err != nil {
		t.Fatalf("create manifest projection trigger: %v", err)
	}

	path := writeTempNamedFile(t, "manifest-project-failure.bin", bigBody(2500))
	meta, op, header, err := svc.uploadSingle(context.Background(), 0, path, "", personalChannelID, false)
	if err == nil || !errors.Is(err, tgclient.ErrSendOutcomeUnknown) && !strings.Contains(err.Error(), "injected manifest projection failure") {
		t.Fatalf("uploadSingle error = %v, want manifest projection failure", err)
	}
	if meta.MsgID == 0 {
		t.Fatalf("metadata = %+v, want accepted manifest msg id", meta)
	}
	if op.Type != projection.OpFileManifest || header == "" {
		t.Fatalf("returned op/header = %+v/%q, want manifest op for caller retry", op, header)
	}
	if _, err := db.Exec(`DROP TRIGGER fail_visible_manifest_projection`); err != nil {
		t.Fatalf("drop manifest projection trigger: %v", err)
	}
	if _, err := projection.ProjectFromOp(db, personalChannelID, int64(meta.MsgID), op, *actorID, header); err != nil {
		t.Fatalf("retry manifest projection: %v", err)
	}
	if !projection.FileExists(db, personalChannelID, int64(meta.MsgID)) {
		t.Fatal("manifest did not become visible after local projection retry")
	}
}

func TestMultipartUploadReportsManifestProjectionFailure(t *testing.T) {
	svc, db, _, _ := newTestService(t)
	svc.MaxUploadBytes = 1000
	if _, err := db.Exec(`
		CREATE TRIGGER fail_visible_manifest_projection
		BEFORE INSERT ON files
		WHEN NEW.upload_uuid != ''
		BEGIN
			SELECT RAISE(ABORT, 'injected manifest projection failure');
		END;
	`); err != nil {
		t.Fatalf("create manifest projection trigger: %v", err)
	}
	defer func() {
		_, _ = db.Exec(`DROP TRIGGER fail_visible_manifest_projection`)
	}()

	path := writeTempNamedFile(t, "manifest-project-failure-batch.bin", bigBody(2500))
	files, err := svc.Upload(context.Background(), personalChannelID, []string{path}, []string{""}, false)
	if err == nil || !strings.Contains(err.Error(), "injected manifest projection failure") {
		t.Fatalf("Upload error = %v, want manifest projection failure", err)
	}
	if len(files) != 1 || files[0].MsgID == 0 {
		t.Fatalf("Upload files = %+v, want accepted remote manifest metadata", files)
	}
	if projection.FileExists(db, personalChannelID, int64(files[0].MsgID)) {
		t.Fatal("manifest unexpectedly became visible after persistent projection failure")
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
	mu              sync.Mutex
	failStream      bool
	failAt          bool
	failAtCount     int
	failStreamCount int
}

func (c *transientDownloadClient) takeFailure(stream bool) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if stream && c.failStreamCount > 0 {
		c.failStreamCount--
		return true
	}
	if !stream && c.failAtCount > 0 {
		c.failAtCount--
		return true
	}
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

func TestEncryptedMultipartDownloadFailureReportsNetworkError(t *testing.T) {
	svc, _, fakeTG, _ := newTestService(t)
	svc.MaxUploadBytes = 1000
	svc.FloodWaitRetry = instantRetryPolicy()
	masterKey := bytes.Repeat([]byte{7}, 32)
	configureEncryptedUpload(t, svc, masterKey)

	path := writeTempNamedFile(t, "encrypted-network.bin", bigBody(2500))
	files, err := svc.Upload(context.Background(), personalChannelID, []string{path}, []string{""}, true)
	if err != nil {
		t.Fatalf("encrypted multipart upload: %v", err)
	}

	svc.FloodWaitRetry = tgclient.FloodWaitRetryPolicy{
		Sleep:               func(context.Context, time.Duration) error { return nil },
		MaxTransientRetries: 1,
		TransientBackoff:    time.Millisecond,
	}
	svc.TG = &transientDownloadClient{Fake: fakeTG, failAtCount: 2}
	savePath := filepath.Join(t.TempDir(), "network.out")
	result := svc.Download(context.Background(), personalChannelID, files[0].MsgID, files[0].MsgID, func(string) (string, error) {
		return savePath, nil
	})
	if result.Status != "error" || !strings.Contains(result.Message, "Network Error") {
		t.Fatalf("download = %+v, want network error", result)
	}
}

func TestEncryptedMultipartDecryptFailureReportsDecryptError(t *testing.T) {
	svc, _, fakeTG, _ := newTestService(t)
	svc.MaxUploadBytes = 1000
	svc.FloodWaitRetry = instantRetryPolicy()
	uploadKey := bytes.Repeat([]byte{7}, 32)
	configureEncryptedUpload(t, svc, uploadKey)

	path := writeTempNamedFile(t, "encrypted-wrong-key.bin", bigBody(2500))
	files, err := svc.Upload(context.Background(), personalChannelID, []string{path}, []string{""}, true)
	if err != nil {
		t.Fatalf("encrypted multipart upload: %v", err)
	}

	wrongKey := bytes.Repeat([]byte{8}, 32)
	svc.TG = fakeTG
	svc.RequireEncryptionKey = func(encrypted bool) ([]byte, error) {
		if !encrypted {
			return nil, nil
		}
		return append([]byte(nil), wrongKey...), nil
	}
	savePath := filepath.Join(t.TempDir(), "wrong-key.out")
	result := svc.Download(context.Background(), personalChannelID, files[0].MsgID, files[0].MsgID, func(string) (string, error) {
		return savePath, nil
	})
	if result.Status != "error" || !strings.Contains(result.Message, "Download/decrypt failed") {
		t.Fatalf("download = %+v, want decrypt error", result)
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
	calls    int
	entered  chan<- struct{}
	release  <-chan struct{}
}

func (c *countingClient) beginSend() func() {
	c.mu.Lock()
	c.inFlight++
	c.calls++
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

func (c *countingClient) waitForRelease(ctx context.Context) error {
	if c.entered != nil {
		c.entered <- struct{}{}
	}
	if c.release == nil {
		return nil
	}
	select {
	case <-c.release:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (c *countingClient) snapshot() (calls, inFlight, maxSeen int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.calls, c.inFlight, c.maxSeen
}

func (c *countingClient) SendFile(ctx context.Context, peer tgclient.InputPeer, r io.Reader, name, caption string, totalSize int64, onProgress func(sent, total int64)) (tgclient.SendFileResult, error) {
	defer c.beginSend()()
	if err := c.waitForRelease(ctx); err != nil {
		return tgclient.SendFileResult{}, err
	}
	return c.Fake.SendFile(ctx, peer, r, name, caption, totalSize, onProgress)
}

func (c *countingClient) SendFileWithRandomID(ctx context.Context, peer tgclient.InputPeer, r io.Reader, name, caption string, totalSize int64, onProgress func(sent, total int64), randomID int64) (tgclient.SendFileResult, error) {
	defer c.beginSend()()
	if err := c.waitForRelease(ctx); err != nil {
		return tgclient.SendFileResult{}, err
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
			svc.MaxConcurrentUploads = tc.limit

			var paths, parents []string
			for i := 0; i < fileCount; i++ {
				paths = append(paths, writeTempNamedFile(t, "f"+string(rune('a'+i))+".txt", []byte("body")))
				parents = append(parents, "")
			}
			synctest.Test(t, func(t *testing.T) {
				entered := make(chan struct{}, fileCount)
				release := make(chan struct{})
				client := &countingClient{Fake: fakeTG, entered: entered, release: release}
				svc.TG = client

				resultCh := make(chan []Metadata, 1)
				errCh := make(chan error, 1)
				go func() {
					files, err := svc.Upload(context.Background(), personalChannelID, paths, parents, false)
					resultCh <- files
					errCh <- err
				}()

				for i := 0; i < tc.wantMax; i++ {
					<-entered
				}
				synctest.Wait()
				calls, inFlight, maxSeen := client.snapshot()
				if calls != tc.wantMax || inFlight != tc.wantMax || maxSeen != tc.wantMax {
					t.Fatalf("before release calls/in-flight/max = %d/%d/%d, want %d/%d/%d", calls, inFlight, maxSeen, tc.wantMax, tc.wantMax, tc.wantMax)
				}

				close(release)
				synctest.Wait()
				if err := <-errCh; err != nil {
					t.Fatalf("upload: %v", err)
				}
				if files := <-resultCh; len(files) != fileCount {
					t.Fatalf("uploaded = %d, want %d", len(files), fileCount)
				}
				calls, inFlight, maxSeen = client.snapshot()
				if calls != fileCount || inFlight != 0 || maxSeen != tc.wantMax {
					t.Fatalf("final calls/in-flight/max = %d/%d/%d, want %d/0/%d", calls, inFlight, maxSeen, fileCount, tc.wantMax)
				}
			})
		})
	}
}
