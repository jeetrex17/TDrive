package file

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	tdcrypto "TDrive/backend/crypto"
	"TDrive/backend/projection"
	"TDrive/backend/tgclient"

	_ "modernc.org/sqlite"
)

const (
	personalChannelID int64 = 616161
	sharedChannelID   int64 = 717171
)

var errNeedPassword = errors.New("password required")

type testPeerResolver struct {
	peer tgclient.InputPeer
}

func (r testPeerResolver) ResolvePeer(ctx context.Context, channelID int64) (tgclient.InputPeer, error) {
	return r.peer, nil
}

type eventRecorder struct {
	mu     sync.Mutex
	events []string
}

func (r *eventRecorder) Emit(name string, args ...any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, name)
}

func (r *eventRecorder) Has(name string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, ev := range r.events {
		if ev == name {
			return true
		}
	}
	return false
}

func newTestService(t *testing.T) (*Service, *sql.DB, *tgclient.Fake, *int64) {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	if err := projection.MigratePersonalChannel(db, personalChannelID); err != nil {
		t.Fatalf("migrate personal: %v", err)
	}
	if err := projection.InsertChannel(db, projection.Channel{
		ChannelID:            sharedChannelID,
		AccessHash:           88,
		Title:                "Shared",
		Kind:                 projection.KindShared,
		PersonalBackfillDone: true,
	}); err != nil {
		t.Fatalf("insert shared: %v", err)
	}

	fakeTG := tgclient.NewFake(7)
	peer := tgclient.InputPeer{ChannelID: personalChannelID, AccessHash: 99}
	fakeTG.SeedChannel(peer, "Personal")

	actor := int64(7)
	msgID := int64(1000)
	svc := &Service{
		DB:    db,
		TG:    fakeTG,
		Peers: testPeerResolver{peer: peer},
		EmitOp: func(channelID int64, op projection.Op) (int64, error) {
			msgID++
			header := projection.Format(op)
			_, err := projection.ProjectFromOp(db, channelID, msgID, op, actor, header)
			return msgID, err
		},
		ActorID: func(ctx context.Context) (int64, error) {
			return actor, nil
		},
		RequireEncryptionKey: func(encrypted bool) ([]byte, error) {
			return nil, nil
		},
		Now: func() time.Time {
			return time.Unix(1234, 0)
		},
	}
	return svc, db, fakeTG, &actor
}

func writeTempFile(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "upload.txt")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write temp file: %v", err)
	}
	return path
}

func writeTempNamedFile(t *testing.T, name string, body []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatalf("write temp file: %v", err)
	}
	return path
}

var tinyPNG = []byte{
	0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a,
	0x00, 0x00, 0x00, 0x0d, 0x49, 0x48, 0x44, 0x52,
	0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01,
	0x08, 0x06, 0x00, 0x00, 0x00, 0x1f, 0x15, 0xc4,
	0x89,
}

func project(t *testing.T, db *sql.DB, channelID int64, msgID int64, actorID int64, op projection.Op) {
	t.Helper()
	header := projection.Format(op)
	if _, err := projection.ProjectFromOp(db, channelID, msgID, op, actorID, header); err != nil {
		t.Fatalf("project %s msg=%d: %v", op.Type, msgID, err)
	}
}

func assertKeyZeroed(t *testing.T, key []byte) {
	t.Helper()
	for i, b := range key {
		if b != 0 {
			t.Fatalf("key byte %d = %#x, want all owned key bytes cleared", i, b)
		}
	}
}

func TestPreviewFilePlainReturnsBase64(t *testing.T) {
	svc, _, _, _ := newTestService(t)
	path := writeTempNamedFile(t, "image.png", tinyPNG)
	files, err := svc.Upload(context.Background(), personalChannelID, []string{path}, []string{""}, false)
	if err != nil {
		t.Fatalf("upload: %v", err)
	}

	payload, err := svc.PreviewFile(context.Background(), personalChannelID, files[0].MsgID)
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	if payload.MimeType != "image/png" {
		t.Fatalf("mime = %q, want image/png", payload.MimeType)
	}
	got, err := base64.StdEncoding.DecodeString(payload.DataBase64)
	if err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	if !bytes.Equal(got, tinyPNG) {
		t.Fatalf("preview bytes = %x, want %x", got, tinyPNG)
	}
}

func TestPreviewFileEncryptedDecrypts(t *testing.T) {
	svc, _, _, _ := newTestService(t)
	masterKey := bytes.Repeat([]byte{9}, 32)
	svc.MasterKeyForUpload = func(channelID int64, wantEncrypted bool) ([]byte, error) {
		if !wantEncrypted {
			return nil, nil
		}
		return append([]byte(nil), masterKey...), nil
	}
	svc.WriteCiphertextTemp = func(plain io.Reader, plaintextSize int64, masterKey []byte) (*os.File, error) {
		tmp, err := os.CreateTemp("", "tdrive-test-cipher-*")
		if err != nil {
			return nil, err
		}
		if err := tdcrypto.EncryptStream(plain, tmp, masterKey, plaintextSize); err != nil {
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
	var previewKey []byte
	svc.RequireEncryptionKey = func(encrypted bool) ([]byte, error) {
		if encrypted {
			previewKey = append([]byte(nil), masterKey...)
			return previewKey, nil
		}
		return nil, nil
	}

	path := writeTempNamedFile(t, "secret.png", tinyPNG)
	files, err := svc.Upload(context.Background(), personalChannelID, []string{path}, []string{""}, true)
	if err != nil {
		t.Fatalf("upload: %v", err)
	}

	payload, err := svc.PreviewFile(context.Background(), personalChannelID, files[0].MsgID)
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	got, err := base64.StdEncoding.DecodeString(payload.DataBase64)
	if err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	if !bytes.Equal(got, tinyPNG) {
		t.Fatalf("preview bytes = %x, want %x", got, tinyPNG)
	}
	assertKeyZeroed(t, previewKey)
}

func TestPreviewFileEncryptedRequiresPassword(t *testing.T) {
	svc, _, _, _ := newTestService(t)
	masterKey := bytes.Repeat([]byte{8}, 32)
	svc.MasterKeyForUpload = func(channelID int64, wantEncrypted bool) ([]byte, error) {
		if !wantEncrypted {
			return nil, nil
		}
		return append([]byte(nil), masterKey...), nil
	}
	svc.WriteCiphertextTemp = func(plain io.Reader, plaintextSize int64, masterKey []byte) (*os.File, error) {
		tmp, err := os.CreateTemp("", "tdrive-test-cipher-*")
		if err != nil {
			return nil, err
		}
		if err := tdcrypto.EncryptStream(plain, tmp, masterKey, plaintextSize); err != nil {
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

	path := writeTempNamedFile(t, "locked.png", tinyPNG)
	files, err := svc.Upload(context.Background(), personalChannelID, []string{path}, []string{""}, true)
	if err != nil {
		t.Fatalf("upload: %v", err)
	}
	failedKey := bytes.Repeat([]byte{0x3c}, 32)
	svc.RequireEncryptionKey = func(encrypted bool) ([]byte, error) {
		if encrypted {
			return failedKey, errNeedPassword
		}
		return nil, nil
	}

	payload, err := svc.PreviewFile(context.Background(), personalChannelID, files[0].MsgID)
	if !errors.Is(err, errPreviewEncryptionPasswordRequired) {
		t.Fatalf("preview err = %v, want password required", err)
	}
	if payload.DataBase64 != "" || payload.MimeType != "" {
		t.Fatalf("payload = %+v, want empty", payload)
	}
	assertKeyZeroed(t, failedKey)
}

func TestPreviewFileTooLargeUsesPlaintextSize(t *testing.T) {
	svc, db, fakeTG, _ := newTestService(t)
	project(t, db, personalChannelID, 81, 7, projection.Op{
		Type:              projection.OpFileUpload,
		Parent:            "",
		Name:              "huge.png",
		FileSize:          1,
		FileUploadTime:    1,
		Encrypted:         true,
		PlaintextSize:     7_864_321,
		EncryptionVersion: 1,
	})
	fakeTG.SeedHistory(tgclient.HistoryMessage{
		MsgID:        81,
		HasMedia:     true,
		MediaSize:    1,
		DocumentName: "huge.png",
	})
	svc.RequireEncryptionKey = func(encrypted bool) ([]byte, error) {
		t.Fatalf("RequireEncryptionKey called; budget should fail first")
		return nil, nil
	}

	if _, err := svc.PreviewFile(context.Background(), personalChannelID, 81); !errors.Is(err, errPreviewTooLarge) {
		t.Fatalf("preview err = %v, want too large", err)
	}
}

func TestUploadPlainFileSendsAndProjects(t *testing.T) {
	svc, db, fakeTG, _ := newTestService(t)
	events := &eventRecorder{}
	svc.Events = events
	path := writeTempFile(t, "hello")

	files, err := svc.Upload(context.Background(), personalChannelID, []string{path}, []string{""}, false)
	if err != nil {
		t.Fatalf("upload: %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("files = %+v, want one", files)
	}
	if files[0].Name != "upload.txt" || files[0].Size != 5 || files[0].ParentID != "" {
		t.Fatalf("bad metadata: %+v", files[0])
	}
	if !projection.FileExists(db, personalChannelID, int64(files[0].MsgID)) {
		t.Fatalf("projected file missing")
	}
	sent := fakeTG.SentFiles()
	if len(sent) != 1 {
		t.Fatalf("sent files = %+v, want one", sent)
	}
	if _, err := projection.Parse(sent[0].Caption); err != nil {
		t.Fatalf("caption does not parse: %v", err)
	}
	if !events.Has("upload_start") || !events.Has("upload_complete") || !events.Has("upload_progress") {
		t.Fatalf("events = %+v, missing upload lifecycle event", events.events)
	}
}

func TestUploadRejectsMissingParentBeforeSend(t *testing.T) {
	svc, _, fakeTG, _ := newTestService(t)
	events := &eventRecorder{}
	svc.Events = events
	path := writeTempFile(t, "hello")

	files, err := svc.Upload(context.Background(), personalChannelID, []string{path}, []string{"d:missing"}, false)
	if err == nil {
		t.Fatal("upload should reject a missing parent")
	}
	if len(files) != 0 {
		t.Fatalf("files = %+v, want none", files)
	}
	if sent := fakeTG.SentFiles(); len(sent) != 0 {
		t.Fatalf("sent files = %+v, want none", sent)
	}
	if !events.Has("upload_start") || !events.Has("upload_error") {
		t.Fatalf("events = %+v, want visible failed upload lifecycle", events.events)
	}
}

func TestUploadEncryptedUsesCiphertextAndPlaintextMetadata(t *testing.T) {
	svc, db, fakeTG, _ := newTestService(t)
	path := writeTempFile(t, "plain")
	uploadKey := []byte("master-key")
	svc.MasterKeyForUpload = func(channelID int64, wantEncrypted bool) ([]byte, error) {
		if !wantEncrypted {
			return nil, nil
		}
		return uploadKey, nil
	}
	svc.WriteCiphertextTemp = func(plain io.Reader, plaintextSize int64, masterKey []byte) (*os.File, error) {
		tmp, err := os.CreateTemp("", "tdrive-test-cipher-*")
		if err != nil {
			return nil, err
		}
		if _, err := tmp.Write([]byte("ciphertext")); err != nil {
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

	files, err := svc.Upload(context.Background(), personalChannelID, []string{path}, []string{""}, true)
	if err != nil {
		t.Fatalf("upload: %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("files = %+v, want one", files)
	}
	if !files[0].Encrypted || files[0].PlaintextSize != 5 || files[0].Size != 10 {
		t.Fatalf("bad encrypted metadata: %+v", files[0])
	}
	encrypted, plaintextSize, _, err := projection.FileEncryptionMeta(db, personalChannelID, int64(files[0].MsgID))
	if err != nil {
		t.Fatalf("encryption meta: %v", err)
	}
	if !encrypted || plaintextSize != 5 {
		t.Fatalf("db encrypted=%v plaintext=%d, want encrypted plaintext=5", encrypted, plaintextSize)
	}
	if sent := fakeTG.SentFiles(); len(sent) != 1 || sent[0].Size != 10 {
		t.Fatalf("sent files = %+v, want ciphertext size 10", sent)
	}
	assertKeyZeroed(t, uploadKey)
}

func TestUploadEncryptedRequiresPasswordBeforeSend(t *testing.T) {
	svc, _, fakeTG, _ := newTestService(t)
	path := writeTempFile(t, "plain")
	failedKey := bytes.Repeat([]byte{0x5a}, 32)
	svc.MasterKeyForUpload = func(channelID int64, wantEncrypted bool) ([]byte, error) {
		return failedKey, errNeedPassword
	}

	if _, err := svc.Upload(context.Background(), personalChannelID, []string{path}, []string{""}, true); err == nil {
		t.Fatalf("upload unexpectedly succeeded")
	}
	if sent := fakeTG.SentFiles(); len(sent) != 0 {
		t.Fatalf("sent files = %+v, want none", sent)
	}
	assertKeyZeroed(t, failedKey)
}

func TestDownloadPlainFileWritesBytes(t *testing.T) {
	svc, _, _, _ := newTestService(t)
	path := writeTempFile(t, "hello")
	files, err := svc.Upload(context.Background(), personalChannelID, []string{path}, []string{""}, false)
	if err != nil {
		t.Fatalf("upload: %v", err)
	}

	savePath := filepath.Join(t.TempDir(), "download.txt")
	result := svc.Download(context.Background(), personalChannelID, files[0].MsgID, files[0].MsgID, func(defaultName string) (string, error) {
		if defaultName != "upload.txt" {
			t.Fatalf("defaultName = %q, want upload.txt", defaultName)
		}
		return savePath, nil
	})
	if result.Status != "success" {
		t.Fatalf("download = %+v", result)
	}
	got, err := os.ReadFile(savePath)
	if err != nil {
		t.Fatalf("read downloaded file: %v", err)
	}
	if string(got) != "hello" {
		t.Fatalf("downloaded bytes = %q, want hello", string(got))
	}
}

func TestReplaceDownloadedFileReplacesExistingFile(t *testing.T) {
	dir := t.TempDir()
	tmpPath := filepath.Join(dir, "download.tmp")
	savePath := filepath.Join(dir, "download.txt")
	if err := os.WriteFile(tmpPath, []byte("new"), 0o600); err != nil {
		t.Fatalf("write tmp: %v", err)
	}
	if err := os.WriteFile(savePath, []byte("old"), 0o600); err != nil {
		t.Fatalf("write existing: %v", err)
	}

	if err := replaceDownloadedFile(tmpPath, savePath); err != nil {
		t.Fatalf("replaceDownloadedFile: %v", err)
	}
	got, err := os.ReadFile(savePath)
	if err != nil {
		t.Fatalf("read replaced file: %v", err)
	}
	if string(got) != "new" {
		t.Fatalf("replaced file = %q, want new", string(got))
	}
	if _, err := os.Stat(tmpPath); !os.IsNotExist(err) {
		t.Fatalf("tmp still exists or stat failed unexpectedly: %v", err)
	}
}

func TestDownloadEncryptedFileDecrypts(t *testing.T) {
	svc, _, _, _ := newTestService(t)
	masterKey := bytes.Repeat([]byte{7}, 32)
	svc.MasterKeyForUpload = func(channelID int64, wantEncrypted bool) ([]byte, error) {
		if !wantEncrypted {
			return nil, nil
		}
		return append([]byte(nil), masterKey...), nil
	}
	svc.WriteCiphertextTemp = func(plain io.Reader, plaintextSize int64, masterKey []byte) (*os.File, error) {
		tmp, err := os.CreateTemp("", "tdrive-test-cipher-*")
		if err != nil {
			return nil, err
		}
		if err := tdcrypto.EncryptStream(plain, tmp, masterKey, plaintextSize); err != nil {
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
	var downloadKey []byte
	svc.RequireEncryptionKey = func(encrypted bool) ([]byte, error) {
		if encrypted {
			downloadKey = append([]byte(nil), masterKey...)
			return downloadKey, nil
		}
		return nil, nil
	}

	path := writeTempFile(t, "secret")
	files, err := svc.Upload(context.Background(), personalChannelID, []string{path}, []string{""}, true)
	if err != nil {
		t.Fatalf("upload: %v", err)
	}

	savePath := filepath.Join(t.TempDir(), "secret.out")
	result := svc.Download(context.Background(), personalChannelID, files[0].MsgID, files[0].MsgID, func(defaultName string) (string, error) {
		return savePath, nil
	})
	if result.Status != "success" {
		t.Fatalf("download = %+v", result)
	}
	got, err := os.ReadFile(savePath)
	if err != nil {
		t.Fatalf("read downloaded file: %v", err)
	}
	if string(got) != "secret" {
		t.Fatalf("downloaded bytes = %q, want secret", string(got))
	}
	assertKeyZeroed(t, downloadKey)
}

func TestDownloadEncryptedDecryptFailurePreservesExistingFile(t *testing.T) {
	svc, _, _, _ := newTestService(t)
	masterKey := bytes.Repeat([]byte{7}, 32)
	wrongKey := bytes.Repeat([]byte{8}, 32)
	svc.MasterKeyForUpload = func(channelID int64, wantEncrypted bool) ([]byte, error) {
		if !wantEncrypted {
			return nil, nil
		}
		return append([]byte(nil), masterKey...), nil
	}
	svc.WriteCiphertextTemp = func(plain io.Reader, plaintextSize int64, masterKey []byte) (*os.File, error) {
		tmp, err := os.CreateTemp("", "tdrive-test-cipher-*")
		if err != nil {
			return nil, err
		}
		if err := tdcrypto.EncryptStream(plain, tmp, masterKey, plaintextSize); err != nil {
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
	var downloadKey []byte
	svc.RequireEncryptionKey = func(encrypted bool) ([]byte, error) {
		if encrypted {
			downloadKey = append([]byte(nil), wrongKey...)
			return downloadKey, nil
		}
		return nil, nil
	}

	path := writeTempFile(t, "secret")
	files, err := svc.Upload(context.Background(), personalChannelID, []string{path}, []string{""}, true)
	if err != nil {
		t.Fatalf("upload: %v", err)
	}

	savePath := filepath.Join(t.TempDir(), "existing.txt")
	if err := os.WriteFile(savePath, []byte("keep me"), 0o600); err != nil {
		t.Fatalf("write existing: %v", err)
	}
	result := svc.Download(context.Background(), personalChannelID, files[0].MsgID, files[0].MsgID, func(defaultName string) (string, error) {
		return savePath, nil
	})
	if result.Status != "error" || !strings.Contains(result.Message, "Decrypt failed") {
		t.Fatalf("download = %+v, want decrypt error", result)
	}
	got, err := os.ReadFile(savePath)
	if err != nil {
		t.Fatalf("read existing: %v", err)
	}
	if string(got) != "keep me" {
		t.Fatalf("existing file = %q, want preserved contents", string(got))
	}
	assertKeyZeroed(t, downloadKey)
}

func TestDownloadEncryptedRequiresPassword(t *testing.T) {
	svc, db, fakeTG, _ := newTestService(t)
	project(t, db, personalChannelID, 80, 7, projection.Op{
		Type:              projection.OpFileUpload,
		Parent:            "",
		Name:              "locked.bin",
		FileSize:          4,
		FileUploadTime:    1,
		Encrypted:         true,
		PlaintextSize:     4,
		EncryptionVersion: 1,
	})
	fakeTG.SeedHistory(tgclient.HistoryMessage{
		MsgID:        80,
		HasMedia:     true,
		MediaSize:    4,
		DocumentName: "locked.bin",
	})
	failedKey := bytes.Repeat([]byte{0x6b}, 32)
	svc.RequireEncryptionKey = func(encrypted bool) ([]byte, error) {
		if encrypted {
			return failedKey, errNeedPassword
		}
		return nil, nil
	}

	called := false
	result := svc.Download(context.Background(), personalChannelID, 80, 80, func(defaultName string) (string, error) {
		called = true
		return filepath.Join(t.TempDir(), "should-not-exist"), nil
	})
	if result.Status != "error" || !strings.Contains(result.Message, errNeedPassword.Error()) {
		t.Fatalf("download = %+v, want password error", result)
	}
	if called {
		t.Fatalf("chooseSavePath was called before password check")
	}
	assertKeyZeroed(t, failedKey)
}

func TestMetaPublishesLegacyMetadata(t *testing.T) {
	svc, db, _, _ := newTestService(t)
	project(t, db, personalChannelID, 10, 7, projection.Op{
		Type:   projection.OpMkdir,
		Obj:    "d:docs",
		Parent: "",
		Name:   "Docs",
	})

	if err := svc.Meta(personalChannelID, 42, " legacy.txt ", 12, "d:docs"); err != nil {
		t.Fatalf("meta: %v", err)
	}
	if !projection.FileExists(db, personalChannelID, 42) {
		t.Fatalf("file was not projected")
	}
	parent, err := projection.FileParent(db, personalChannelID, 42)
	if err != nil {
		t.Fatalf("file parent: %v", err)
	}
	if parent != "d:docs" {
		t.Fatalf("parent = %q, want d:docs", parent)
	}
}

func TestRenameAndMoveFile(t *testing.T) {
	svc, db, _, _ := newTestService(t)
	project(t, db, personalChannelID, 10, 7, projection.Op{
		Type:   projection.OpMkdir,
		Obj:    "d:docs",
		Parent: "",
		Name:   "Docs",
	})
	project(t, db, personalChannelID, 50, 7, projection.Op{
		Type:           projection.OpFileUpload,
		Parent:         "",
		Name:           "old.txt",
		FileSize:       1,
		FileUploadTime: 1,
	})

	if err := svc.Rename(context.Background(), personalChannelID, 50, "new.txt"); err != nil {
		t.Fatalf("rename: %v", err)
	}
	if err := svc.Move(context.Background(), personalChannelID, 50, "d:docs"); err != nil {
		t.Fatalf("move: %v", err)
	}
	parent, err := projection.FileParent(db, personalChannelID, 50)
	if err != nil {
		t.Fatalf("file parent: %v", err)
	}
	if parent != "d:docs" {
		t.Fatalf("parent = %q, want d:docs", parent)
	}
}

func TestSharedRenameMoveAndDeleteRequireUploader(t *testing.T) {
	svc, db, fakeTG, actor := newTestService(t)
	project(t, db, sharedChannelID, 11, 9, projection.Op{
		Type:   projection.OpMkdir,
		Obj:    "d:shared-docs",
		Parent: "",
		Name:   "Docs",
	})
	project(t, db, sharedChannelID, 60, 9, projection.Op{
		Type:           projection.OpFileUpload,
		Parent:         "",
		Name:           "owner.txt",
		FileSize:       1,
		FileUploadTime: 1,
	})

	*actor = 7
	if err := svc.Rename(context.Background(), sharedChannelID, 60, "bad.txt"); err == nil {
		t.Fatalf("rename by non-uploader unexpectedly succeeded")
	}
	if err := svc.Move(context.Background(), sharedChannelID, 60, "d:shared-docs"); err == nil {
		t.Fatalf("move by non-uploader unexpectedly succeeded")
	}
	if err := svc.Delete(context.Background(), sharedChannelID, 60); err == nil {
		t.Fatalf("delete by non-uploader unexpectedly succeeded")
	}

	*actor = 9
	if err := svc.Rename(context.Background(), sharedChannelID, 60, "good.txt"); err != nil {
		t.Fatalf("rename by uploader: %v", err)
	}
	if err := svc.Move(context.Background(), sharedChannelID, 60, "d:shared-docs"); err != nil {
		t.Fatalf("move by uploader: %v", err)
	}
	if err := svc.Delete(context.Background(), sharedChannelID, 60); err != nil {
		t.Fatalf("delete by uploader: %v", err)
	}
	if projection.FileExists(db, sharedChannelID, 60) {
		t.Fatalf("file still visible after tomb")
	}
	if batches := fakeTG.DeletedBatches(); len(batches) != 1 || len(batches[0]) != 1 || batches[0][0] != 60 {
		t.Fatalf("deleted batches = %+v", batches)
	}
}

func TestDeleteEncryptedFileRequiresPassword(t *testing.T) {
	svc, db, _, _ := newTestService(t)
	project(t, db, personalChannelID, 70, 7, projection.Op{
		Type:              projection.OpFileUpload,
		Parent:            "",
		Name:              "secret.bin",
		FileSize:          20,
		FileUploadTime:    1,
		Encrypted:         true,
		PlaintextSize:     10,
		EncryptionVersion: 1,
	})
	svc.RequireEncryptionKey = func(encrypted bool) ([]byte, error) {
		if encrypted {
			return nil, errNeedPassword
		}
		return nil, nil
	}

	if err := svc.Delete(context.Background(), personalChannelID, 70); !errors.Is(err, errNeedPassword) {
		t.Fatalf("delete err = %v, want password error", err)
	}
	if !projection.FileExists(db, personalChannelID, 70) {
		t.Fatalf("file was tombstoned despite missing password")
	}
}

func TestRequireEncryptedFileKeyClearsOwnedKey(t *testing.T) {
	svc, db, _, _ := newTestService(t)
	project(t, db, personalChannelID, 71, 7, projection.Op{
		Type:              projection.OpFileUpload,
		Parent:            "",
		Name:              "owned-key.bin",
		FileSize:          20,
		FileUploadTime:    1,
		Encrypted:         true,
		PlaintextSize:     10,
		EncryptionVersion: 1,
	})

	for _, tc := range []struct {
		name string
		err  error
	}{
		{name: "success"},
		{name: "provider error", err: errNeedPassword},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ownedKey := bytes.Repeat([]byte{0x73}, 32)
			svc.RequireEncryptionKey = func(encrypted bool) ([]byte, error) {
				if !encrypted {
					t.Fatal("encrypted mutation gate requested a plaintext key")
				}
				return ownedKey, tc.err
			}

			err := svc.requireEncryptedFileKey(personalChannelID, 71)
			if !errors.Is(err, tc.err) {
				t.Fatalf("requireEncryptedFileKey() error = %v, want %v", err, tc.err)
			}
			assertKeyZeroed(t, ownedKey)
		})
	}
}
