package file

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	tdcrypto "TDrive/backend/crypto"
	"TDrive/backend/projection"
	"TDrive/backend/tgclient"
	"TDrive/backend/thumbnail"
)

type countedPreparationClient struct {
	*tgclient.Fake
	downloads int
	failAfter int
}

func (c *countedPreparationClient) DownloadFile(ctx context.Context, peer tgclient.InputPeer, id int64, w io.Writer, progress func(int64, int64)) error {
	c.downloads++
	if c.failAfter > 0 {
		_, _ = w.Write(make([]byte, c.failAfter))
		return io.ErrUnexpectedEOF
	}
	return c.Fake.DownloadFile(ctx, peer, id, w, progress)
}
func preparationOriginal(t *testing.T, s *Service, raw []byte, encrypted bool) projection.File {
	t.Helper()
	ctx := context.Background()
	peer, _ := s.Peers.ResolvePeer(ctx, personalChannelID)
	payload := raw
	if encrypted {
		key, err := s.requireEncryptionKey(true)
		if err != nil {
			t.Fatal(err)
		}
		var cipher bytes.Buffer
		if err := tdcrypto.EncryptStream(bytes.NewReader(raw), &cipher, key, int64(len(raw))); err != nil {
			t.Fatal(err)
		}
		clear(key)
		payload = cipher.Bytes()
	}
	op := projection.Op{Type: projection.OpFileUpload, Name: "photo.jpg", FileSize: int64(len(payload)), PlaintextSize: int64(len(raw)), Encrypted: encrypted, EncryptionVersion: 1}
	result, err := s.TG.SendFile(ctx, peer, bytes.NewReader(payload), op.Name, projection.Format(op), op.FileSize, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := projection.ProjectFromOp(s.DB, personalChannelID, result.MsgID, op, 7, projection.Format(op)); err != nil {
		t.Fatal(err)
	}
	source, found, err := projection.FileByID(s.DB, personalChannelID, result.MsgID)
	if err != nil || !found {
		t.Fatalf("source %v", err)
	}
	return source
}
func TestRemotePreparationDownloadsOnceAndResumesFromDurableImages(t *testing.T) {
	for _, encrypted := range []bool{false, true} {
		t.Run(map[bool]string{false: "plain", true: "encrypted"}[encrypted], func(t *testing.T) {
			s, _, fake, _ := newTestService(t)
			counter := &countedPreparationClient{Fake: fake}
			s.TG = counter
			s.RequireEncryptionKey = func(want bool) ([]byte, error) {
				if want {
					return bytes.Repeat([]byte{4}, 32), nil
				}
				return nil, nil
			}
			source := preparationOriginal(t, s, tinyRenditionJPEG(t), encrypted)
			got, err := s.PrepareRemoteRenditions(context.Background(), personalChannelID, source.MsgID)
			if err != nil || got != source.Size {
				t.Fatalf("prepare bytes=%d err=%v", got, err)
			}
			got, err = s.PrepareRemoteRenditions(context.Background(), personalChannelID, source.MsgID)
			if err != nil || got != 0 || counter.downloads != 1 {
				t.Fatalf("resume bytes=%d calls=%d err=%v", got, counter.downloads, err)
			}
			if err := projection.RebuildProjection(s.DB, personalChannelID); err != nil {
				t.Fatal(err)
			}
			if _, err := projection.CurrentFileRendition(context.Background(), s.DB, personalChannelID, source.MsgID, "preview"); err != nil {
				t.Fatal(err)
			}
		})
	}
}
func TestRemotePreparationRejectsLargeSourceAndAccountsInterruptedBytes(t *testing.T) {
	s, db, fake, _ := newTestService(t)
	counter := &countedPreparationClient{Fake: fake}
	s.TG = counter
	source := preparationOriginal(t, s, tinyRenditionJPEG(t), false)
	if _, err := db.Exec(`UPDATE files SET size=? WHERE channel_id=? AND msg_id=?`, 31<<20, personalChannelID, source.MsgID); err != nil {
		t.Fatal(err)
	}
	if n, err := s.PrepareRemoteRenditions(context.Background(), personalChannelID, source.MsgID); !errors.Is(err, thumbnail.ErrTooLarge) || n != 0 || counter.downloads != 0 {
		t.Fatalf("large original downloaded: %d %v", n, err)
	}
	if _, err := db.Exec(`UPDATE files SET size=? WHERE channel_id=? AND msg_id=?`, source.Size, personalChannelID, source.MsgID); err != nil {
		t.Fatal(err)
	}
	counter.failAfter = 20
	if n, err := s.PrepareRemoteRenditions(context.Background(), personalChannelID, source.MsgID); !errors.Is(err, io.ErrUnexpectedEOF) || n != 20 {
		t.Fatalf("partial accounting: %d %v", n, err)
	}
}
func TestVisiblePhotoUploadPublishesBothRenditions(t *testing.T) {
	s, db, _, _ := newTestService(t)
	path := filepath.Join(t.TempDir(), "photo.jpg")
	if err := os.WriteFile(path, tinyRenditionJPEG(t), 0600); err != nil {
		t.Fatal(err)
	}
	files, err := s.Upload(context.Background(), personalChannelID, []string{path}, []string{""}, false)
	if err != nil || len(files) != 1 {
		t.Fatalf("upload %+v %v", files, err)
	}
	for _, kind := range []string{"thumbnail", "preview"} {
		if _, err := projection.CurrentFileRendition(context.Background(), db, personalChannelID, int64(files[0].MsgID), kind); err != nil {
			t.Fatalf("uploaded %s unavailable: %v", kind, err)
		}
	}
}
func TestPreparationWritersEnforceByteBudget(t *testing.T) {
	var b bytes.Buffer
	w := preparationWriter{dst: &b, remaining: 4}
	if n, err := w.Write([]byte("12345")); err == nil || n != 0 || b.Len() != 0 {
		t.Fatal("preparation exceeded budget")
	}
	bounded := renditionBoundedWriter{buffer: &b, remaining: 4}
	if _, err := bounded.Write([]byte("12345")); !errors.Is(err, thumbnail.ErrTooLarge) {
		t.Fatal("decryption exceeded budget")
	}
}

type replacingPhotoClient struct {
	*tgclient.Fake
	path        string
	replacement []byte
	mutated     bool
}

func (c *replacingPhotoClient) SendFileWithRandomID(ctx context.Context, peer tgclient.InputPeer, reader io.Reader, name, caption string, size int64, progress func(int64, int64), randomID int64) (tgclient.SendFileResult, error) {
	if !c.mutated {
		c.mutated = true
		if err := os.WriteFile(c.path, c.replacement, 0600); err != nil {
			return tgclient.SendFileResult{}, err
		}
	}
	return c.Fake.SendFileWithRandomID(ctx, peer, reader, name, caption, size, progress, randomID)
}
func TestPhotoSnapshotPreventsChangedPathFromRebindingDerivative(t *testing.T) {
	s, db, fake, _ := newTestService(t)
	raw := tinyRenditionJPEG(t)
	path := filepath.Join(t.TempDir(), "photo.jpg")
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	s.TG = &replacingPhotoClient{Fake: fake, path: path, replacement: []byte("different bytes while Telegram is sending")}
	uploaded, err := s.Upload(context.Background(), personalChannelID, []string{path}, []string{""}, false)
	if err != nil || len(uploaded) != 1 {
		t.Fatalf("upload failed %+v %v", uploaded, err)
	}
	peer, _ := s.Peers.ResolvePeer(context.Background(), personalChannelID)
	var original bytes.Buffer
	if err := fake.DownloadFile(context.Background(), peer, int64(uploaded[0].MsgID), &original, nil); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(original.Bytes(), raw) {
		t.Fatal("original did not use frozen bytes")
	}
	if _, err := projection.CurrentFileRendition(context.Background(), db, personalChannelID, int64(uploaded[0].MsgID), "preview"); err != nil {
		t.Fatalf("derivative read changed path instead of frozen source: %v", err)
	}
}

func TestPreparationRechecksReplacementAgainstAdmittedByteBudget(t *testing.T) {
	s, db, fake, _ := newTestService(t)
	counter := &countedPreparationClient{Fake: fake}
	s.TG = counter
	source := preparationOriginal(t, s, tinyRenditionJPEG(t), false)
	if _, err := db.Exec(`UPDATE files SET size=? WHERE channel_id=? AND msg_id=?`, source.Size+1, personalChannelID, source.MsgID); err != nil {
		t.Fatal(err)
	}
	n, err := s.PrepareRemoteRenditionsWithinBudget(context.Background(), personalChannelID, source.MsgID, source.Size)
	if !errors.Is(err, ErrRenditionPreparationBudget) || n != 0 || counter.downloads != 0 {
		t.Fatalf("replacement bypassed admitted bytes: %d %d %v", n, counter.downloads, err)
	}
}

func TestPreparationPreflightDoesNotDownloadAndRequiresUnlockedKey(t *testing.T) {
	s, _, fake, _ := newTestService(t)
	counter := &countedPreparationClient{Fake: fake}
	s.TG = counter
	s.RequireEncryptionKey = func(encrypted bool) ([]byte, error) {
		if encrypted {
			return bytes.Repeat([]byte{4}, 32), nil
		}
		return nil, nil
	}
	source := preparationOriginal(t, s, tinyRenditionJPEG(t), true)
	key := bytes.Repeat([]byte{4}, 32)
	s.RequireEncryptionKey = func(bool) ([]byte, error) { return key, nil }
	if err := s.ValidateRenditionPreparation(context.Background(), personalChannelID, source.MsgID); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(key, make([]byte, 32)) {
		t.Fatal("preflight retained owned key")
	}
	locked := errors.New("encryption password required")
	s.RequireEncryptionKey = func(bool) ([]byte, error) { return nil, locked }
	if err := s.ValidateRenditionPreparation(context.Background(), personalChannelID, source.MsgID); !errors.Is(err, locked) {
		t.Fatalf("locked preflight: %v", err)
	}
	if counter.downloads != 0 {
		t.Fatal("preflight downloaded original")
	}
}

func TestRemotePreparationSupportsMultipartPhotoIdentity(t *testing.T) {
	s, db, fake, _ := newTestService(t)
	ctx := context.Background()
	raw := tinyRenditionJPEG(t)
	peer, _ := s.Peers.ResolvePeer(ctx, personalChannelID)
	uuid := "legacy-photo-parts"
	for i, part := range [][]byte{raw[:len(raw)/2], raw[len(raw)/2:]} {
		op := projection.Op{Type: projection.OpFilePart, UploadUUID: uuid, PartIndex: i, FileSize: int64(len(part))}
		receipt, err := fake.SendFile(ctx, peer, bytes.NewReader(part), "part.bin", projection.Format(op), op.FileSize, nil)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := projection.ProjectFromOp(db, personalChannelID, receipt.MsgID, op, 7, projection.Format(op)); err != nil {
			t.Fatal(err)
		}
	}
	manifest := projection.Op{Type: projection.OpFileManifest, UploadUUID: uuid, PartCount: 2, Name: "photo.jpg", FileSize: int64(len(raw))}
	id, err := fake.SendControl(ctx, peer, projection.Format(manifest), true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := projection.ProjectFromOp(db, personalChannelID, id, manifest, 7, projection.Format(manifest)); err != nil {
		t.Fatal(err)
	}
	n, err := s.PrepareRemoteRenditions(ctx, personalChannelID, id)
	if err != nil || n != int64(len(raw)) {
		t.Fatalf("multipart preparation %d %v", n, err)
	}
	ref, err := projection.CurrentFileRendition(ctx, db, personalChannelID, id, "preview")
	if err != nil || ref.UploadUUID != uuid || ref.ContentMsgID != 0 {
		t.Fatalf("multipart preview binding %+v %v", ref, err)
	}
}
