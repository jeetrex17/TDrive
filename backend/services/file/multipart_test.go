package file

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

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
	svc, db, _, _ := newTestService(t)
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
	wireEncryption(svc, bytes.Repeat([]byte{5}, 32))

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
