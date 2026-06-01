package folder

import (
	"database/sql"
	"testing"

	"TDrive/backend/projection"

	_ "modernc.org/sqlite"
)

const testChannelID int64 = 424242

func newTestService(t *testing.T) (*Service, *sql.DB) {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := projection.MigratePersonalChannel(db, testChannelID); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	var msgID int64
	svc := &Service{
		DB: db,
		EmitOp: func(channelID int64, op projection.Op) error {
			msgID++
			header := projection.Format(op)
			_, err := projection.ProjectFromOp(db, channelID, msgID, op, 1, header)
			return err
		},
	}
	return svc, db
}

func TestCreateFolderProjectsMkdir(t *testing.T) {
	svc, db := newTestService(t)

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
	svc, db := newTestService(t)

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

	if err := svc.Delete(testChannelID, parent.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if projection.FolderExists(db, testChannelID, parent.ID) {
		t.Fatalf("deleted folder still exists")
	}
}
