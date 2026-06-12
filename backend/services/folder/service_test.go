package folder

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

const testChannelID int64 = 424242

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
	if err := projection.MigratePersonalChannel(db, testChannelID); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	fakeTG := tgclient.NewFake(7)
	peer := tgclient.InputPeer{ChannelID: testChannelID, AccessHash: 99}
	fakeTG.SeedChannel(peer, "Personal")
	actor := int64(7)
	var msgID int64
	svc := &Service{
		DB:    db,
		TG:    fakeTG,
		Peers: testPeerResolver{peer: peer},
		EmitOp: func(channelID int64, op projection.Op) error {
			msgID++
			header := projection.Format(op)
			_, err := projection.ProjectFromOp(db, channelID, msgID, op, actor, header)
			return err
		},
		ActorID: func(ctx context.Context) (int64, error) {
			return actor, nil
		},
		RequireEncryptionKey: func(encrypted bool) ([]byte, error) {
			return nil, nil
		},
	}
	return svc, db, fakeTG, &actor
}

func TestCreateFolderProjectsMkdir(t *testing.T) {
	svc, db, _, _ := newTestService(t)

	got, err := svc.Create(testChannelID, "Photos", "")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if !projection.IsFolderID(got.ID) || got.Name != "Photos" || got.ParentID != projection.RootParent {
		t.Fatalf("bad folder: %+v", got)
	}
	if !projection.FolderExists(db, testChannelID, got.ID) {
		t.Fatalf("folder was not projected")
	}

	if _, err := svc.Create(testChannelID, "Photos", ""); err == nil {
		t.Fatalf("duplicate folder name unexpectedly succeeded")
	}
}

func TestRenameMoveAndDeleteFolder(t *testing.T) {
	svc, db, fakeTG, _ := newTestService(t)

	parent, err := svc.Create(testChannelID, "Parent", "")
	if err != nil {
		t.Fatalf("create parent: %v", err)
	}
	child, err := svc.Create(testChannelID, "Child", "")
	if err != nil {
		t.Fatalf("create child: %v", err)
	}

	if err := svc.Rename(testChannelID, child.ID, "Kid"); err != nil {
		t.Fatalf("rename: %v", err)
	}
	folders, _, err := projection.ListFolderContents(db, testChannelID, "")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	foundRename := false
	for _, f := range folders {
		if f.ID == child.ID && f.Name == "Kid" {
			foundRename = true
		}
	}
	if !foundRename {
		t.Fatalf("renamed folder not found in %+v", folders)
	}

	if err := svc.Move(testChannelID, child.ID, parent.ID); err != nil {
		t.Fatalf("move: %v", err)
	}
	gotParent, err := projection.FolderParent(db, testChannelID, child.ID)
	if err != nil {
		t.Fatalf("folder parent: %v", err)
	}
	if gotParent != parent.ID {
		t.Fatalf("parent = %q, want %q", gotParent, parent.ID)
	}
	if err := svc.Move(testChannelID, parent.ID, child.ID); err == nil {
		t.Fatalf("cycle move unexpectedly succeeded")
	}
	if err := svc.EmitOp(testChannelID, projection.Op{
		Type:           projection.OpFileUpload,
		Parent:         child.ID,
		Name:           "deep.txt",
		FileSize:       10,
		FileUploadTime: time.Unix(123, 0).Unix(),
	}); err != nil {
		t.Fatalf("seed child file: %v", err)
	}
	var deepFileID int64
	if err := db.QueryRow(`
		SELECT msg_id FROM files
		WHERE channel_id = ? AND parent_id = ? AND name = 'deep.txt'
	`, testChannelID, child.ID).Scan(&deepFileID); err != nil {
		t.Fatalf("scan deep file id: %v", err)
	}

	if err := svc.Delete(context.Background(), testChannelID, parent.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if projection.FolderExists(db, testChannelID, parent.ID) {
		t.Fatalf("deleted folder still exists")
	}
	if projection.FolderExists(db, testChannelID, child.ID) {
		t.Fatalf("descendant folder still exists")
	}
	if projection.FileExists(db, testChannelID, deepFileID) {
		t.Fatalf("descendant file still visible")
	}
	batches := fakeTG.DeletedBatches()
	if len(batches) != 1 || len(batches[0]) != 1 || batches[0][0] != deepFileID {
		t.Fatalf("deleted batches = %+v, want [[%d]]", batches, deepFileID)
	}
}

func TestDeleteFolderCascadesToMultipartParts(t *testing.T) {
	svc, db, fakeTG, _ := newTestService(t)

	folder, err := svc.Create(testChannelID, "Big", "")
	if err != nil {
		t.Fatalf("create folder: %v", err)
	}

	// Seed a multipart file inside the folder: 3 part ops + a manifest op.
	const uuid = "uft-1"
	for i := 0; i < 3; i++ {
		if err := svc.EmitOp(testChannelID, projection.Op{
			Type: projection.OpFilePart, UploadUUID: uuid, PartIndex: i, FileSize: 100,
		}); err != nil {
			t.Fatalf("emit part %d: %v", i, err)
		}
	}
	if err := svc.EmitOp(testChannelID, projection.Op{
		Type: projection.OpFileManifest, UploadUUID: uuid, Parent: folder.ID,
		Name: "big.bin", FileSize: 300, PartCount: 3, FileUploadTime: time.Unix(123, 0).Unix(),
	}); err != nil {
		t.Fatalf("emit manifest: %v", err)
	}

	var manifestID int64
	if err := db.QueryRow(`SELECT msg_id FROM files WHERE channel_id = ? AND upload_uuid = ?`, testChannelID, uuid).Scan(&manifestID); err != nil {
		t.Fatalf("scan manifest id: %v", err)
	}
	parts, err := projection.MultipartParts(db, testChannelID, manifestID)
	if err != nil || len(parts) != 3 {
		t.Fatalf("parts = %+v (err %v), want 3", parts, err)
	}

	if err := svc.Delete(context.Background(), testChannelID, folder.ID); err != nil {
		t.Fatalf("delete folder: %v", err)
	}

	// The file_parts rows are dropped...
	if left, _ := projection.MultipartParts(db, testChannelID, manifestID); len(left) != 0 {
		t.Fatalf("file_parts after delete = %d, want 0", len(left))
	}
	// ...and the manifest + every part body were deleted from Telegram.
	deleted := map[int64]bool{}
	for _, batch := range fakeTG.DeletedBatches() {
		for _, id := range batch {
			deleted[id] = true
		}
	}
	for _, id := range []int64{manifestID, parts[0].MsgID, parts[1].MsgID, parts[2].MsgID} {
		if !deleted[id] {
			t.Fatalf("msg %d was not deleted from Telegram; deleted=%v", id, deleted)
		}
	}
}

func TestDeleteFolderRequiresOwnershipForSharedDescendantFiles(t *testing.T) {
	svc, db, fakeTG, actor := newTestService(t)
	const sharedChannelID int64 = 717171
	if err := projection.InsertChannel(db, projection.Channel{
		ChannelID:            sharedChannelID,
		AccessHash:           1,
		Title:                "Shared",
		Kind:                 projection.KindShared,
		JoinedAt:             1,
		PersonalBackfillDone: true,
	}); err != nil {
		t.Fatalf("insert shared channel: %v", err)
	}
	fakeTG.SeedChannel(tgclient.InputPeer{ChannelID: sharedChannelID, AccessHash: 1}, "Shared")

	*actor = 9
	folder, err := svc.Create(sharedChannelID, "Shared folder", "")
	if err != nil {
		t.Fatalf("create shared folder: %v", err)
	}
	if err := svc.EmitOp(sharedChannelID, projection.Op{
		Type:           projection.OpFileUpload,
		Parent:         folder.ID,
		Name:           "owned-by-nine.txt",
		FileSize:       1,
		FileUploadTime: 1,
	}); err != nil {
		t.Fatalf("seed shared file: %v", err)
	}

	*actor = 7
	if err := svc.Delete(context.Background(), sharedChannelID, folder.ID); err == nil {
		t.Fatalf("delete by non-uploader unexpectedly succeeded")
	}
	if !projection.FolderExists(db, sharedChannelID, folder.ID) {
		t.Fatalf("folder was deleted by non-uploader")
	}
	if batches := fakeTG.DeletedBatches(); len(batches) != 0 {
		t.Fatalf("body delete happened despite permission failure: %+v", batches)
	}

	*actor = 9
	if err := svc.Delete(context.Background(), sharedChannelID, folder.ID); err != nil {
		t.Fatalf("delete by uploader: %v", err)
	}
	if projection.FolderExists(db, sharedChannelID, folder.ID) {
		t.Fatalf("folder still exists after uploader delete")
	}
}

func TestDeleteFolderRequiresPasswordForEncryptedDescendantFiles(t *testing.T) {
	svc, db, _, _ := newTestService(t)
	folder, err := svc.Create(testChannelID, "Secrets", "")
	if err != nil {
		t.Fatalf("create folder: %v", err)
	}
	if err := svc.EmitOp(testChannelID, projection.Op{
		Type:              projection.OpFileUpload,
		Parent:            folder.ID,
		Name:              "secret.bin",
		FileSize:          20,
		FileUploadTime:    1,
		Encrypted:         true,
		PlaintextSize:     10,
		EncryptionVersion: 1,
	}); err != nil {
		t.Fatalf("seed encrypted file: %v", err)
	}
	needPassword := errors.New("encryption password required")
	svc.RequireEncryptionKey = func(encrypted bool) ([]byte, error) {
		if encrypted {
			return nil, needPassword
		}
		return nil, nil
	}

	if err := svc.Delete(context.Background(), testChannelID, folder.ID); !errors.Is(err, needPassword) {
		t.Fatalf("delete err = %v, want password error", err)
	}
	if !projection.FolderExists(db, testChannelID, folder.ID) {
		t.Fatalf("folder was deleted despite missing password")
	}
}
