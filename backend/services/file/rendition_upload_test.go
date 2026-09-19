package file

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"testing"

	"TDrive/backend/projection"
)

func seedRenditionSource(t *testing.T, s *Service, db *sql.DB, raw []byte, encrypted bool) projection.File {
	t.Helper()
	op := projection.Op{Type: projection.OpFileUpload, Name: "photo.jpg", FileSize: int64(len(raw)), Encrypted: encrypted, PlaintextSize: int64(len(raw)), EncryptionVersion: 1}
	if _, err := projection.ProjectFromOp(db, personalChannelID, 1, op, 7, projection.Format(op)); err != nil {
		t.Fatal(err)
	}
	source, found, err := projection.FileByID(db, personalChannelID, 1)
	if err != nil || !found {
		t.Fatalf("seed: %v", err)
	}
	return source
}
func TestPrepareRenditionsUploadsHiddenImagesAndSkipsCompleted(t *testing.T) {
	for _, encrypted := range []bool{false, true} {
		t.Run(map[bool]string{false: "plain", true: "encrypted"}[encrypted], func(t *testing.T) {
			s, db, tg, _ := newTestService(t)
			raw := tinyRenditionJPEG(t)
			key := bytes.Repeat([]byte{3}, 32)
			s.RequireEncryptionKey = func(want bool) ([]byte, error) {
				if want {
					return append([]byte(nil), key...), nil
				}
				return nil, nil
			}
			// A real original upload reserves message one before sidecars are sent.
			peer, _ := s.Peers.ResolvePeer(context.Background(), personalChannelID)
			if _, err := tg.SendFile(context.Background(), peer, bytes.NewReader(raw), "photo.jpg", "", int64(len(raw)), nil); err != nil {
				t.Fatal(err)
			}
			source := seedRenditionSource(t, s, db, raw, encrypted)
			if err := s.PrepareRenditions(context.Background(), source, bytes.NewReader(raw)); err != nil {
				t.Fatal(err)
			}
			for _, kind := range []string{"thumbnail", "preview"} {
				ref, err := projection.CurrentFileRendition(context.Background(), db, personalChannelID, 1, kind)
				if err != nil {
					t.Fatal(err)
				}
				var stored bytes.Buffer
				if err := tg.DownloadFile(context.Background(), peer, ref.MsgID, &stored, nil); err != nil {
					t.Fatal(err)
				}
				got, err := decryptRendition(stored.Bytes(), key, ref)
				if err != nil || len(got) == 0 {
					t.Fatalf("second-device decode: %v", err)
				}
			}
			if err := s.PrepareRenditions(context.Background(), source, bytes.NewReader([]byte("unreadable, must not decode again"))); err != nil {
				t.Fatal(err)
			}
			var count int
			if err := db.QueryRow(`SELECT COUNT(*) FROM file_renditions`).Scan(&count); err != nil || count != 2 {
				t.Fatalf("refs=%d err=%v", count, err)
			}
			var pending int
			if err := db.QueryRow(`SELECT COUNT(*) FROM pending_rendition_uploads`).Scan(&pending); err != nil || pending != 0 {
				t.Fatalf("pending=%d err=%v", pending, err)
			}
		})
	}
}

func TestRenditionOutboxReconcilesAcceptedReceiptAfterLocalFailure(t *testing.T) {
	s, db, tg, _ := newTestService(t)
	raw := tinyRenditionJPEG(t)
	ctx := context.Background()
	peer, _ := s.Peers.ResolvePeer(ctx, personalChannelID)
	if _, err := tg.SendFile(ctx, peer, bytes.NewReader(raw), "photo.jpg", "", int64(len(raw)), nil); err != nil {
		t.Fatal(err)
	}
	source := seedRenditionSource(t, s, db, raw, false)
	ref, err := renditionDescriptor(source, "thumbnail", raw)
	if err != nil {
		t.Fatal(err)
	}
	job, err := newRenditionJob(ref, raw, 1234)
	if err != nil {
		t.Fatal(err)
	}
	if err := projection.QueueRendition(ctx, db, job); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TRIGGER fail_rendition_projection BEFORE INSERT ON file_renditions BEGIN SELECT RAISE(ABORT,'disk failure'); END`); err != nil {
		t.Fatal(err)
	}
	if err := s.ResumeRenditionUploads(ctx, personalChannelID, 16); err == nil {
		t.Fatal("expected local failure")
	}
	if _, err := db.Exec(`DROP TRIGGER fail_rendition_projection`); err != nil {
		t.Fatal(err)
	}
	if err := s.ResumeRenditionUploads(ctx, personalChannelID, 16); err != nil {
		t.Fatal(err)
	}
	messages, err := tg.GetHistory(ctx, peer, 0, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 2 {
		t.Fatalf("idempotent retry made %d messages; want original+one derivative", len(messages))
	}
}
func TestRenditionResumeDeletesStaleAcceptedBody(t *testing.T) {
	s, db, tg, _ := newTestService(t)
	ctx := context.Background()
	raw := tinyRenditionJPEG(t)
	peer, _ := s.Peers.ResolvePeer(ctx, personalChannelID)
	if _, err := tg.SendFile(ctx, peer, bytes.NewReader(raw), "photo.jpg", "", int64(len(raw)), nil); err != nil {
		t.Fatal(err)
	}
	source := seedRenditionSource(t, s, db, raw, false)
	ref, _ := renditionDescriptor(source, "thumbnail", raw)
	job, _ := newRenditionJob(ref, raw, 1234)
	if err := projection.QueueRendition(ctx, db, job); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE files SET tombstoned=1 WHERE channel_id=? AND msg_id=1`, personalChannelID); err != nil {
		t.Fatal(err)
	}
	if err := s.ResumeRenditionUploads(ctx, personalChannelID, 16); err != nil {
		t.Fatal(err)
	}
	if _, err := projection.CurrentFileRendition(ctx, db, personalChannelID, 1, "thumbnail"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("stale ref %v", err)
	}
	var derivativeID int64
	if err := db.QueryRow(`SELECT msg_id FROM file_renditions WHERE file_msg_id=1`).Scan(&derivativeID); err != nil {
		t.Fatal(err)
	}
	if missing, err := tg.MissingMessages(ctx, peer, []int64{derivativeID}); err != nil || len(missing) != 1 {
		t.Fatalf("stale blob exists: %v %v", missing, err)
	}
}
