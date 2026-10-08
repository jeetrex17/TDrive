package file

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"testing"
	"time"

	"TDrive/backend/projection"
	"TDrive/backend/tgclient"
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

func TestBackupUnknownReceiptSurvivesRetryCancellation(t *testing.T) {
	for _, multipart := range []bool{false, true} {
		t.Run(map[bool]string{false: "single", true: "multipart"}[multipart], func(t *testing.T) {
			svc, _, fake, _ := newTestService(t)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			client := &visibleAcceptThenLoseReceiptClient{Fake: fake, fileFailAt: 1}
			body := []byte("original")
			if multipart {
				svc.MaxUploadBytes = 1000
				body = bigBody(2500)
				client.fileFailAt = 0
				client.controlFailAll = true
			}
			svc.TG = client
			policy := instantRetryPolicy()
			policy.Sleep = func(context.Context, time.Duration) error {
				cancel()
				return context.Canceled
			}
			svc.FloodWaitRetry = policy
			meta, err := svc.UploadBackup(ctx, personalChannelID, writeTempNamedFile(t, "original.bin", body), "", false)
			if meta.MsgID != 0 || !errors.Is(err, tgclient.ErrSendOutcomeUnknown) || !errors.Is(err, context.Canceled) {
				t.Fatalf("lost receipt = %+v, %v; want unknown outcome and cancellation", meta, err)
			}
			if errors.Is(err, ErrBackupUploadNotStarted) {
				t.Fatal("an attempted send was classified as unsent")
			}
		})
	}
}

func TestBackupCancellationBeforeSendIsExplicitlySafeToRetry(t *testing.T) {
	for _, stage := range []string{"slot", "encryption"} {
		t.Run(stage, func(t *testing.T) {
			svc, _, fake, _ := newTestService(t)
			configureEncryptedUpload(t, svc, bytes.Repeat([]byte{7}, 32))
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if stage == "slot" {
				svc.MaxConcurrentUploads = 1
				release, err := svc.acquireUploadSlot(t.Context())
				if err != nil {
					t.Fatal(err)
				}
				defer release()
				svc.ActorID = func(context.Context) (int64, error) { cancel(); return 7, nil }
			} else {
				write := svc.WriteCiphertextTemp
				svc.WriteCiphertextTemp = func(plain io.Reader, size int64, key []byte) (*os.File, error) {
					cancel()
					file, err := write(plain, size, key)
					if !errors.Is(err, context.Canceled) {
						t.Errorf("ciphertext preparation ignored cancellation: %v", err)
					}
					return file, err
				}
			}
			meta, err := svc.UploadBackup(ctx, personalChannelID, writeTempNamedFile(t, "photo.bin", []byte("original")), "", true)
			if meta.MsgID != 0 || !errors.Is(err, context.Canceled) || !errors.Is(err, ErrBackupUploadNotStarted) {
				t.Fatalf("cancel before send = %+v, %v", meta, err)
			}
			if len(fake.SentFiles()) != 0 {
				t.Fatal("sent a body after pre-send cancellation")
			}
		})
	}
}

func TestBackupPreviewAdmissionCancellationPreservesReceipt(t *testing.T) {
	svc, _, _, _ := newTestService(t)
	configureEncryptedUpload(t, svc, bytes.Repeat([]byte{7}, 32))
	w := newBackupRenditionWorker(t.Context(), personalChannelID, func(ctx context.Context, _ projection.File, _ []byte) error {
		<-ctx.Done()
		return ctx.Err()
	}, func(context.Context) error { return nil })
	t.Cleanup(w.Close)
	if !w.enqueue(projection.File{ChannelID: personalChannelID, MsgID: 1}, []byte{7}) {
		t.Fatal("could not reserve preparation budget")
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	meta, err := svc.UploadBackupWithRenditions(ctx, personalChannelID, writeTempNamedFile(t, "photo.jpg", tinyRenditionJPEG(t)), "", true, w, func(p BackupUploadProgress) {
		if p.Percent == 100 {
			w.mu.Lock()
			count := w.count
			w.mu.Unlock()
			if count != 2 {
				t.Errorf("original sent without its preview reservation: count=%d", count)
			}
			cancel()
		}
	})
	if meta.MsgID <= 0 || err != nil {
		t.Fatalf("receipt lost while preview admission canceled: %+v, %v", meta, err)
	}
}

func TestBackupFullPreviewBudgetCancelsBeforeAnyOriginalSend(t *testing.T) {
	svc, _, fake, _ := newTestService(t)
	configureEncryptedUpload(t, svc, bytes.Repeat([]byte{7}, 32))
	w := newBackupRenditionWorker(t.Context(), personalChannelID, func(ctx context.Context, _ projection.File, _ []byte) error {
		<-ctx.Done()
		return ctx.Err()
	}, func(context.Context) error { return nil })
	t.Cleanup(w.Close)
	for id := int64(1); id <= 2; id++ {
		if !w.enqueue(projection.File{ChannelID: personalChannelID, MsgID: id}, []byte{7}) {
			t.Fatal("could not fill preparation budget")
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	path := writeTempNamedFile(t, "photo.jpg", tinyRenditionJPEG(t))
	result := make(chan error, 1)
	go func() {
		_, err := svc.UploadBackupWithRenditions(ctx, personalChannelID, path, "", true, w)
		result <- err
	}()
	cancel()
	select {
	case err := <-result:
		if !errors.Is(err, ErrBackupUploadNotStarted) || !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled reservation = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("full preview budget ignored cancellation")
	}
	if len(fake.SentFiles()) != 0 {
		t.Fatal("original was sent before preview budget was reserved")
	}
}

func TestBackupUploadTimingReportsBoundedStages(t *testing.T) {
	svc, _, _, _ := newTestService(t)
	configureEncryptedUpload(t, svc, bytes.Repeat([]byte{7}, 32))
	stages := make(map[string]time.Duration)
	ctx := WithBackupUploadTiming(t.Context(), func(stage string, duration time.Duration) { stages[stage] += duration })
	_, err := svc.UploadBackup(ctx, personalChannelID, writeTempNamedFile(t, "not-logged.bin", []byte("private source")), "", true)
	if err != nil {
		t.Fatal(err)
	}
	for _, stage := range []string{"source_prepare", "encrypt", "transfer", "projection"} {
		if elapsed, ok := stages[stage]; !ok || elapsed < 0 {
			t.Fatalf("stage %q = %v, present=%v", stage, elapsed, ok)
		}
	}
	if len(stages) != 4 {
		t.Fatalf("unexpected stages: %v", stages)
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
