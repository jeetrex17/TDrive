package file

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

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
		RequireEncryptionKey: func(encrypted bool) error {
			return nil
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

func project(t *testing.T, db *sql.DB, channelID int64, msgID int64, actorID int64, op projection.Op) {
	t.Helper()
	header := projection.Format(op)
	if _, err := projection.ProjectFromOp(db, channelID, msgID, op, actorID, header); err != nil {
		t.Fatalf("project %s msg=%d: %v", op.Type, msgID, err)
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

func TestUploadEncryptedUsesCiphertextAndPlaintextMetadata(t *testing.T) {
	svc, db, fakeTG, _ := newTestService(t)
	path := writeTempFile(t, "plain")
	svc.MasterKeyForUpload = func(channelID int64, wantEncrypted bool) ([]byte, error) {
		if !wantEncrypted {
			return nil, nil
		}
		return []byte("master-key"), nil
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
}

func TestUploadEncryptedRequiresPasswordBeforeSend(t *testing.T) {
	svc, _, fakeTG, _ := newTestService(t)
	path := writeTempFile(t, "plain")
	svc.MasterKeyForUpload = func(channelID int64, wantEncrypted bool) ([]byte, error) {
		return nil, errNeedPassword
	}

	if _, err := svc.Upload(context.Background(), personalChannelID, []string{path}, []string{""}, true); err == nil {
		t.Fatalf("upload unexpectedly succeeded")
	}
	if sent := fakeTG.SentFiles(); len(sent) != 0 {
		t.Fatalf("sent files = %+v, want none", sent)
	}
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
	if err := svc.Move(personalChannelID, 50, "d:docs"); err != nil {
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

func TestSharedRenameAndDeleteRequireUploader(t *testing.T) {
	svc, db, fakeTG, actor := newTestService(t)
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
	if err := svc.Delete(context.Background(), sharedChannelID, 60); err == nil {
		t.Fatalf("delete by non-uploader unexpectedly succeeded")
	}

	*actor = 9
	if err := svc.Rename(context.Background(), sharedChannelID, 60, "good.txt"); err != nil {
		t.Fatalf("rename by uploader: %v", err)
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
	svc.RequireEncryptionKey = func(encrypted bool) error {
		if encrypted {
			return errNeedPassword
		}
		return nil
	}

	if err := svc.Delete(context.Background(), personalChannelID, 70); !errors.Is(err, errNeedPassword) {
		t.Fatalf("delete err = %v, want password error", err)
	}
	if !projection.FileExists(db, personalChannelID, 70) {
		t.Fatalf("file was tombstoned despite missing password")
	}
}
