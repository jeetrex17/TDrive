package file

import (
	"bytes"
	"context"
	"errors"
	"os"
	"testing"

	"TDrive/backend/projection"
)

func TestBackupUploadProjectsWithoutManualTransferEvents(t *testing.T) {
	svc, db, _, _ := newTestService(t)
	events := &eventRecorder{}
	svc.Events = events
	path := writeTempNamedFile(t, "photo.jpg", []byte("original photo bytes"))
	meta, err := svc.UploadBackup(context.Background(), personalChannelID, path, "", false)
	if err != nil || meta.MsgID <= 0 {
		t.Fatalf("backup upload = %+v, %v", meta, err)
	}
	var count int
	if err := db.QueryRow("SELECT count(*) FROM files WHERE channel_id=? AND msg_id=?", personalChannelID, meta.MsgID).Scan(&count); err != nil || count != 1 {
		t.Fatalf("projected count = %d, %v", count, err)
	}
	if events.Has("upload_start") || events.Has("upload_complete") || events.Has("upload_error") {
		t.Fatal("backup must not reuse manual-upload row IDs/events")
	}
}

func TestBackupUploadKeepsReceiptWhenProjectionFails(t *testing.T) {
	svc, db, _, _ := newTestService(t)
	_, err := db.Exec(`CREATE TRIGGER fail_backup_projection BEFORE INSERT ON files BEGIN SELECT RAISE(FAIL, 'test projection failure'); END`)
	if err != nil {
		t.Fatal(err)
	}
	meta, err := svc.UploadBackup(context.Background(), personalChannelID, writeTempNamedFile(t, "photo.jpg", []byte("original")), "", false)
	if meta.MsgID <= 0 || err == nil {
		t.Fatalf("remote receipt must survive local failure: %+v, %v", meta, err)
	}
}

func TestBackupUploadCancellationAndMissingSource(t *testing.T) {
	svc, _, _, _ := newTestService(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := svc.UploadBackup(ctx, personalChannelID, "missing.jpg", "", false); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled upload = %v", err)
	}
	if meta, err := svc.UploadBackup(context.Background(), personalChannelID, "missing.jpg", "", false); err == nil || meta.MsgID != 0 {
		t.Fatalf("missing source = %+v, %v", meta, err)
	}
}

func TestBackupUploadMultipartReceipt(t *testing.T) {
	svc, db, _, _ := newTestService(t)
	svc.MaxUploadBytes = 1000
	meta, err := svc.UploadBackup(context.Background(), personalChannelID, writeTempNamedFile(t, "movie.mp4", bigBody(2503)), "", false)
	if err != nil {
		t.Fatal(err)
	}
	parts, err := projection.MultipartParts(db, personalChannelID, int64(meta.MsgID))
	if err != nil || len(parts) != 3 {
		t.Fatalf("parts = %d, %v", len(parts), err)
	}
}

func TestBackupUploadRequiresReadyConnection(t *testing.T) {
	if _, err := (&Service{}).UploadBackup(context.Background(), personalChannelID, "photo.jpg", "", false); err == nil {
		t.Fatal("uninitialized service must fail closed")
	}
	svc, _, _, _ := newTestService(t)
	svc.TG = nil
	if _, err := svc.UploadBackup(context.Background(), personalChannelID, "photo.jpg", "", false); err == nil {
		t.Fatal("missing transport must fail closed")
	}
}

func TestBackupUploadDoesNotFallBackToPlaintext(t *testing.T) {
	svc, _, fakeTG, _ := newTestService(t)
	svc.MasterKeyForUpload = func(int64, bool) ([]byte, error) { return nil, errors.New("vault locked") }
	meta, err := svc.UploadBackup(context.Background(), personalChannelID, writeTempNamedFile(t, "photo.jpg", []byte("private original")), "", true)
	if err == nil || meta.MsgID != 0 {
		t.Fatalf("locked vault = %+v, %v", meta, err)
	}
	if len(fakeTG.SentFiles()) != 0 {
		t.Fatal("locked vault must not send plaintext")
	}
}

func TestBackupUploadReportsCurrentFileProgress(t *testing.T) {
	svc, _, _, _ := newTestService(t)
	var updates []BackupUploadProgress
	meta, err := svc.UploadBackup(context.Background(), personalChannelID, writeTempNamedFile(t, "photo.jpg", []byte("original")), "", false, func(p BackupUploadProgress) { updates = append(updates, p) })
	if err != nil || meta.MsgID <= 0 {
		t.Fatalf("upload = %+v, %v", meta, err)
	}
	if len(updates) < 2 || updates[0].Name != "photo.jpg" || updates[0].BytesTotal != 8 || updates[0].Percent != 0 || updates[len(updates)-1].Percent != 100 {
		t.Fatalf("progress = %+v", updates)
	}
}

func TestEncryptedBackupPublishesEncryptedPreviewsFromLocalSource(t *testing.T) {
	svc, db, _, _ := newTestService(t)
	key := bytes.Repeat([]byte{7}, 32)
	svc.MasterKeyForUpload = func(int64, bool) ([]byte, error) { return append([]byte(nil), key...), nil }
	svc.RequireEncryptionKey = func(bool) ([]byte, error) { return append([]byte(nil), key...), nil }
	path := writeTempNamedFile(t, "photo.jpg", tinyRenditionJPEG(t))
	replaced := false
	meta, err := svc.UploadBackup(context.Background(), personalChannelID, path, "", true, func(BackupUploadProgress) {
		if !replaced {
			replaced = true
			if err := os.WriteFile(path, []byte("replacement must never become preview pixels"), 0o600); err != nil {
				t.Fatal(err)
			}
		}
	})
	if err != nil || meta.MsgID <= 0 {
		t.Fatalf("upload = %+v, %v", meta, err)
	}
	ref, err := projection.CurrentFileRendition(context.Background(), db, personalChannelID, int64(meta.MsgID), "thumbnail")
	if err != nil || !ref.Encrypted {
		t.Fatalf("encrypted thumbnail = %+v, %v", ref, err)
	}
}
