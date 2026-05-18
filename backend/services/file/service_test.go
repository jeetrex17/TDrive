package file

import (
	"context"
	"database/sql"
	"errors"
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

func project(t *testing.T, db *sql.DB, channelID int64, msgID int64, actorID int64, op projection.Op) {
	t.Helper()
	header := projection.Format(op)
	if _, err := projection.ProjectFromOp(db, channelID, msgID, op, actorID, header); err != nil {
		t.Fatalf("project %s msg=%d: %v", op.Type, msgID, err)
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
