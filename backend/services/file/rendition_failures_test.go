package file

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"

	"TDrive/backend/projection"
	"TDrive/backend/tgclient"
	"TDrive/backend/thumbnail"
)

type rejectingRenditionClient struct {
	*tgclient.Fake
	sendError    error
	deleteError  error
	emptyReceipt bool
}

func (c *rejectingRenditionClient) SendFileWithRandomID(ctx context.Context, peer tgclient.InputPeer, r io.Reader, name, caption string, size int64, progress func(int64, int64), randomID int64) (tgclient.SendFileResult, error) {
	if c.sendError != nil {
		return tgclient.SendFileResult{}, c.sendError
	}
	if c.emptyReceipt {
		return tgclient.SendFileResult{}, nil
	}
	return c.Fake.SendFileWithRandomID(ctx, peer, r, name, caption, size, progress, randomID)
}
func (c *rejectingRenditionClient) DeleteMessages(ctx context.Context, peer tgclient.InputPeer, ids []int64) error {
	if c.deleteError != nil {
		return c.deleteError
	}
	return c.Fake.DeleteMessages(ctx, peer, ids)
}

func TestEncryptedDerivativeFailureLeavesOnlyCiphertextDurable(t *testing.T) {
	s, db, fake, _ := newTestService(t)
	raw := tinyRenditionJPEG(t)
	key := bytes.Repeat([]byte{7}, 32)
	s.RequireEncryptionKey = func(bool) ([]byte, error) { return append([]byte(nil), key...), nil }
	source := preparationOriginal(t, s, raw, true)
	s.TG = &rejectingRenditionClient{Fake: fake, sendError: errors.New("permission unavailable")}
	if err := s.PrepareRenditions(context.Background(), source, bytes.NewReader(raw)); err == nil {
		t.Fatal("expected send failure")
	}
	ids, err := projection.PendingRenditionIDs(context.Background(), db, personalChannelID, 16)
	if err != nil || len(ids) != 2 {
		t.Fatalf("both derivative intents must survive: %v %v", ids, err)
	}
	for _, id := range ids {
		job, err := projection.LoadPendingRendition(context.Background(), db, personalChannelID, id)
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(job.Payload, raw) || !bytes.HasPrefix(job.Payload, []byte("TDE1")) {
			t.Fatal("plaintext persisted after failure")
		}
	}
	s.TG = fake
	if err := s.ResumeRenditionUploads(context.Background(), personalChannelID, 16); err != nil {
		t.Fatal(err)
	}
}
func TestRenditionRecoveryRetainsIntentUntilReceiptAndCleanupAreCertain(t *testing.T) {
	for _, mode := range []string{"empty receipt", "stale delete failure", "completion failure", "corrupt header", "corrupt payload"} {
		t.Run(mode, func(t *testing.T) {
			s, db, fake, _ := newTestService(t)
			raw := tinyRenditionJPEG(t)
			source := preparationOriginal(t, s, raw, false)
			ref, _ := renditionDescriptor(source, "thumbnail", raw)
			job, _ := newRenditionJob(ref, raw, 1234)
			ctx := context.Background()
			if err := projection.QueueRendition(ctx, db, job); err != nil {
				t.Fatal(err)
			}
			switch mode {
			case "empty receipt":
				s.TG = &rejectingRenditionClient{Fake: fake, emptyReceipt: true}
			case "stale delete failure":
				s.TG = &rejectingRenditionClient{Fake: fake, deleteError: errors.New("delete unavailable")}
				if _, err := db.Exec(`UPDATE files SET tombstoned=1`); err != nil {
					t.Fatal(err)
				}
			case "completion failure":
				if _, err := db.Exec(`CREATE TRIGGER fail_completion BEFORE DELETE ON pending_rendition_uploads BEGIN SELECT RAISE(ABORT,'disk failure'); END`); err != nil {
					t.Fatal(err)
				}
			case "corrupt header":
				if _, err := db.Exec(`UPDATE pending_rendition_uploads SET header='invalid'`); err != nil {
					t.Fatal(err)
				}
			case "corrupt payload":
				if _, err := db.Exec(`UPDATE pending_rendition_uploads SET payload=X'01'`); err != nil {
					t.Fatal(err)
				}
			}
			if err := s.ResumeRenditionUploads(ctx, personalChannelID, 16); err == nil {
				t.Fatal("uncertain outcome reported success")
			}
			if _, err := projection.LoadPendingRendition(ctx, db, personalChannelID, job.JobID); err != nil {
				t.Fatalf("lost recoverable ownership: %v", err)
			}
		})
	}
}
func TestRenditionPreparationRejectsInvalidLocalSources(t *testing.T) {
	s, _, _, _ := newTestService(t)
	ctx := context.Background()
	raw := tinyRenditionJPEG(t)
	source := preparationOriginal(t, s, raw, false)
	if err := s.PrepareRenditions(nil, source, bytes.NewReader(raw)); err == nil {
		t.Fatal("nil context accepted")
	}
	if err := s.PrepareRenditions(ctx, source, bytes.NewReader([]byte("corrupt image"))); !errors.Is(err, thumbnail.ErrUnsupported) {
		t.Fatalf("corrupt source %v", err)
	}
	if err := s.ResumeRenditionUploads(nil, personalChannelID, 1); err == nil {
		t.Fatal("nil resume context")
	}
	if err := s.ResumeRenditionUploads(ctx, personalChannelID, 129); err == nil {
		t.Fatal("unbounded resume")
	}
	encrypted := source
	encrypted.Encrypted = true
	encrypted.PlaintextSize = 33 << 20
	if err := s.PrepareStoredRenditions(ctx, encrypted, bytes.NewReader(raw)); !errors.Is(err, thumbnail.ErrTooLarge) {
		t.Fatal("oversized decrypted source admitted")
	}
	encrypted.PlaintextSize = int64(len(raw))
	s.RequireEncryptionKey = func(bool) ([]byte, error) { return bytes.Repeat([]byte{1}, 32), nil }
	if err := s.PrepareStoredRenditions(ctx, encrypted, bytes.NewReader(raw)); err == nil {
		t.Fatal("unauthenticated original admitted")
	}
}
