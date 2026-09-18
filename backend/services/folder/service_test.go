package folder

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"TDrive/backend/projection"

	_ "modernc.org/sqlite"
)

const testChannelID int64 = 424242

// newTestService wires the service to an in-memory projection. TrashObject
// stands in for the file service's publisher and does exactly what it does:
// read the live dirent and project the trash operation it anchors on.
func newTestService(t *testing.T) (*Service, *sql.DB, *int64) {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := projection.MigratePersonalChannel(db, testChannelID); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	actor := int64(7)
	var msgID int64
	project := func(channelID int64, op projection.Op) error {
		msgID++
		header := projection.Format(op)
		if _, err := projection.ProjectFromOp(db, channelID, msgID, op, actor, header); err != nil {
			return err
		}
		if op.OpID == "" {
			return nil
		}
		return projection.ConfirmWritableOperation(db, channelID, op.OpID)
	}
	svc := &Service{
		DB: db,
		EmitOp: func(channelID int64, op projection.Op) error {
			return project(channelID, op)
		},
		ActorID: func(ctx context.Context) (int64, error) {
			return actor, nil
		},
		RequireEncryptionKey: func(encrypted bool) ([]byte, error) {
			return nil, nil
		},
		TrashObject: func(ctx context.Context, channelID int64, objectID string) error {
			entry, found, err := projection.DirentByID(db, channelID, objectID)
			if err != nil {
				return err
			}
			if !found || entry.Tombstoned {
				return errors.New("item not found")
			}
			return project(channelID, projection.TrashOp(entry, time.Unix(1000, 0), projection.DefaultTrashRetention))
		},
	}
	return svc, db, &actor
}

func assertTrashEntry(t *testing.T, db *sql.DB, channelID int64, objectID, name string) {
	t.Helper()
	entry, err := projection.TrashEntryByID(db, channelID, objectID)
	if err != nil {
		t.Fatalf("trash entry for %s: %v", objectID, err)
	}
	if entry.OriginalName != name {
		t.Fatalf("trash entry name = %q, want %q", entry.OriginalName, name)
	}
	if entry.PurgeAfter <= entry.DeletedAt {
		t.Fatalf("purge_after %d must be after deleted_at %d", entry.PurgeAfter, entry.DeletedAt)
	}
}

func TestCreateFolderProjectsMkdir(t *testing.T) {
	svc, db, _ := newTestService(t)

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

func TestDeleteFolderPublishFailureLeavesProjectionUntouched(t *testing.T) {
	svc, db, _ := newTestService(t)

	folder, err := svc.Create(testChannelID, "Parent", "")
	if err != nil {
		t.Fatalf("create folder: %v", err)
	}
	if err := svc.EmitOp(testChannelID, projection.Op{
		Type:           projection.OpFileUpload,
		Parent:         folder.ID,
		Name:           "keep.txt",
		FileSize:       10,
		FileUploadTime: 1,
	}); err != nil {
		t.Fatalf("seed file: %v", err)
	}
	var fileID int64
	if err := db.QueryRow(`
		SELECT msg_id FROM files
		WHERE channel_id = ? AND parent_id = ? AND name = 'keep.txt'
	`, testChannelID, folder.ID).Scan(&fileID); err != nil {
		t.Fatalf("scan file id: %v", err)
	}

	publishErr := errors.New("injected publish failure")
	svc.TrashObject = func(ctx context.Context, channelID int64, objectID string) error {
		return publishErr
	}

	if err := svc.Delete(context.Background(), testChannelID, folder.ID); !errors.Is(err, publishErr) {
		t.Fatalf("delete err = %v, want %v", err, publishErr)
	}
	if !projection.FolderExists(db, testChannelID, folder.ID) {
		t.Fatalf("folder was locally tombstoned despite publish failure")
	}
	if !projection.FileExists(db, testChannelID, fileID) {
		t.Fatalf("file was locally tombstoned despite publish failure")
	}
}

func TestRenameMoveAndDeleteFolder(t *testing.T) {
	svc, db, _ := newTestService(t)

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
	assertTrashEntry(t, db, testChannelID, parent.ID, "Parent")
	// Only the deleted root is restorable on its own; the subtree comes back
	// with it, so recording an entry per member would offer partial restores
	// the trash model does not have.
	if _, err := projection.TrashEntryByID(db, testChannelID, child.ID); !errors.Is(err, projection.ErrTrashEntryNotFound) {
		t.Fatalf("descendant trash entry err = %v, want %v", err, projection.ErrTrashEntryNotFound)
	}
}

func TestDeleteFolderKeepsMultipartBodiesRestorable(t *testing.T) {
	svc, db, _ := newTestService(t)

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

	// A trashed file is still restorable, so its bodies and the pointers to
	// them must survive the delete and stay out of the orphan sweep.
	if left, _ := projection.MultipartParts(db, testChannelID, manifestID); len(left) != 3 {
		t.Fatalf("file_parts after delete = %d, want 3", len(left))
	}
	orphans, err := projection.OrphanPartMessages(db, testChannelID)
	if err != nil {
		t.Fatalf("orphan parts: %v", err)
	}
	if len(orphans) != 0 {
		t.Fatalf("orphan sweep would delete restorable bodies: %v", orphans)
	}
}

func TestDeleteFolderRequiresOwnershipForSharedDescendantFiles(t *testing.T) {
	svc, db, actor := newTestService(t)
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
	*actor = 9
	if err := svc.Delete(context.Background(), sharedChannelID, folder.ID); err != nil {
		t.Fatalf("delete by uploader: %v", err)
	}
	if projection.FolderExists(db, sharedChannelID, folder.ID) {
		t.Fatalf("folder still exists after uploader delete")
	}
}

func TestDeleteFolderRequiresPasswordForEncryptedDescendantFiles(t *testing.T) {
	svc, db, _ := newTestService(t)
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
	ownedKey := []byte("folder-mutation-owned-key")
	svc.RequireEncryptionKey = func(encrypted bool) ([]byte, error) {
		if encrypted {
			return ownedKey, needPassword
		}
		return nil, nil
	}

	if err := svc.Delete(context.Background(), testChannelID, folder.ID); !errors.Is(err, needPassword) {
		t.Fatalf("delete err = %v, want password error", err)
	}
	if !projection.FolderExists(db, testChannelID, folder.ID) {
		t.Fatalf("folder was deleted despite missing password")
	}
	for i, b := range ownedKey {
		if b != 0 {
			t.Fatalf("owned key byte %d = %#x, want cleared", i, b)
		}
	}
}
