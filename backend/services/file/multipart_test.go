package file

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"TDrive/backend/projection"
)

// bigBody returns deterministic, non-repeating-ish bytes of length n so a
// reassembly mistake (wrong order, dropped/duplicated part) shows up.
func bigBody(n int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte((i*31 + 7) % 251)
	}
	return b
}

func TestMultipartRoundTripPlain(t *testing.T) {
	svc, db, fakeTG, _ := newTestService(t)
	svc.MaxUploadBytes = 1000 // force splitting above 1000 stored bytes

	body := bigBody(3503) // -> 4 parts (1000,1000,1000,503)
	path := writeTempNamedFile(t, "movie.bin", body)
	files, err := svc.Upload(context.Background(), personalChannelID, []string{path}, []string{""}, false)
	if err != nil {
		t.Fatalf("upload: %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("uploaded files = %d, want 1", len(files))
	}

	parts, err := projection.MultipartParts(db, personalChannelID, int64(files[0].MsgID))
	if err != nil {
		t.Fatalf("MultipartParts: %v", err)
	}
	if len(parts) != 4 {
		t.Fatalf("parts = %d, want 4", len(parts))
	}

	// Each part's Telegram attachment should show the original filename
	// (suffixed for order, since 4 messages share it), not "part-00000".
	sent := fakeTG.SentFiles()
	if len(sent) != 4 {
		t.Fatalf("sent files = %+v, want 4", sent)
	}
	for i, want := range []string{"movie.bin.part0", "movie.bin.part1", "movie.bin.part2", "movie.bin.part3"} {
		if sent[i].Name != want {
			t.Fatalf("part %d attachment name = %q, want %q", i, sent[i].Name, want)
		}
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
		t.Fatalf("round-trip mismatch: got %d bytes, want %d", len(got), len(body))
	}
}

func TestMultipartRoundTripEncrypted(t *testing.T) {
	svc, db, _, _ := newTestService(t)
	svc.MaxUploadBytes = 1000
	masterKey := bytes.Repeat([]byte{5}, 32)
	wireEncryption(svc, masterKey)

	body := bigBody(5000) // ciphertext ~5066 -> 6 parts
	path := writeTempNamedFile(t, "secret.bin", body)
	files, err := svc.Upload(context.Background(), personalChannelID, []string{path}, []string{""}, true)
	if err != nil {
		t.Fatalf("upload: %v", err)
	}

	parts, err := projection.MultipartParts(db, personalChannelID, int64(files[0].MsgID))
	if err != nil {
		t.Fatalf("MultipartParts: %v", err)
	}
	if len(parts) < 2 {
		t.Fatalf("encrypted parts = %d, want >= 2", len(parts))
	}

	var downloadKey []byte
	svc.RequireEncryptionKey = func(encrypted bool) ([]byte, error) {
		if !encrypted {
			return nil, nil
		}
		downloadKey = append([]byte(nil), masterKey...)
		return downloadKey, nil
	}
	savePath := filepath.Join(t.TempDir(), "secret.out")
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
		t.Fatalf("encrypted round-trip mismatch: got %d bytes, want %d", len(got), len(body))
	}
	assertKeyZeroed(t, downloadKey)
}

func TestMultipartEncryptedUploadCopiesClearsAndJoinsProducerKey(t *testing.T) {
	svc, _, fakeTG, _ := newTestService(t)
	svc.MaxUploadBytes = 32

	uploadKey := bytes.Repeat([]byte{0x4d}, 32)
	svc.MasterKeyForUpload = func(channelID int64, wantEncrypted bool) ([]byte, error) {
		if !wantEncrypted {
			return nil, nil
		}
		return uploadKey, nil
	}

	producerStarted := make(chan struct{})
	releaseProducer := make(chan struct{})
	producerErr := errors.New("test producer stopped")
	var (
		producerKey       []byte
		sharesCallerKey   bool
		producerKeyActive bool
	)
	svc.encryptStream = func(_ io.Reader, _ io.Writer, key []byte, _ int64) error {
		producerKey = key
		sharesCallerKey = len(key) > 0 && len(uploadKey) > 0 && &key[0] == &uploadKey[0]
		producerKeyActive = bytes.Equal(key, bytes.Repeat([]byte{0x4d}, 32))
		close(producerStarted)
		<-releaseProducer
		return producerErr
	}

	// The send fails without draining the pipe. uploadMultipart must close the
	// reader and still join the blocked producer before its caller can wipe the
	// caller-owned key and return.
	fakeTG.FailNextSend()
	path := writeTempNamedFile(t, "joined.bin", bigBody(64))
	done := make(chan error, 1)
	go func() {
		_, err := svc.Upload(context.Background(), personalChannelID, []string{path}, []string{""}, true)
		done <- err
	}()

	select {
	case <-producerStarted:
	case <-time.After(2 * time.Second):
		close(releaseProducer)
		t.Fatal("encrypted multipart producer did not start")
	}

	select {
	case err := <-done:
		close(releaseProducer)
		t.Fatalf("upload returned before its encryption producer stopped: %v", err)
	case <-time.After(150 * time.Millisecond):
	}

	if sharesCallerKey {
		close(releaseProducer)
		t.Fatal("multipart encryption producer received the caller-owned key buffer")
	}
	if !producerKeyActive {
		close(releaseProducer)
		t.Fatal("multipart encryption producer did not receive an active key copy")
	}
	if !bytes.Equal(uploadKey, bytes.Repeat([]byte{0x4d}, 32)) {
		close(releaseProducer)
		t.Fatal("caller-owned key was cleared while uploadMultipart was still using its producer")
	}

	close(releaseProducer)
	if err := <-done; err == nil {
		t.Fatal("upload unexpectedly succeeded after injected send failure")
	}
	assertKeyZeroed(t, producerKey)
	assertKeyZeroed(t, uploadKey)
}

func TestMultipartDeleteDropsParts(t *testing.T) {
	svc, db, _, _ := newTestService(t)
	svc.MaxUploadBytes = 1000

	body := bigBody(2500) // 3 parts
	path := writeTempNamedFile(t, "a.bin", body)
	files, err := svc.Upload(context.Background(), personalChannelID, []string{path}, []string{""}, false)
	if err != nil {
		t.Fatalf("upload: %v", err)
	}
	manifestMsgID := int64(files[0].MsgID)
	if parts, _ := projection.MultipartParts(db, personalChannelID, manifestMsgID); len(parts) != 3 {
		t.Fatalf("parts = %d, want 3", len(parts))
	}

	if err := svc.Delete(context.Background(), personalChannelID, files[0].MsgID); err != nil {
		t.Fatalf("delete: %v", err)
	}

	// The file_parts rows are dropped...
	if left, _ := projection.MultipartParts(db, personalChannelID, manifestMsgID); len(left) != 0 {
		t.Fatalf("file_parts after delete = %d, want 0", len(left))
	}
	// ...and the file is tombstoned (no longer visible).
	var tombstoned int
	if err := db.QueryRow(`SELECT tombstoned FROM files WHERE channel_id = ? AND msg_id = ?`, personalChannelID, manifestMsgID).Scan(&tombstoned); err != nil {
		t.Fatalf("read file row: %v", err)
	}
	if tombstoned != 1 {
		t.Fatalf("tombstoned = %d, want 1", tombstoned)
	}
}
