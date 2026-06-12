package read

import (
	"context"
	"database/sql"
	"testing"

	"TDrive/backend/projection"
	"TDrive/backend/tgclient"

	_ "modernc.org/sqlite"
)

const testChannelID int64 = 515151

type testPeerResolver struct {
	peer tgclient.InputPeer
}

func (r testPeerResolver) ResolvePeer(ctx context.Context, channelID int64) (tgclient.InputPeer, error) {
	return r.peer, nil
}

func newTestService(t *testing.T) (*Service, *sql.DB, *tgclient.Fake) {
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
	fakeTG.SeedChannel(peer, "Read Test")
	svc := &Service{
		DB:    db,
		TG:    fakeTG,
		Peers: testPeerResolver{peer: peer},
	}
	return svc, db, fakeTG
}

func project(t *testing.T, db *sql.DB, msgID int64, op projection.Op) {
	t.Helper()
	header := projection.Format(op)
	if _, err := projection.ProjectFromOp(db, testChannelID, msgID, op, 7, header); err != nil {
		t.Fatalf("project %s msg=%d: %v", op.Type, msgID, err)
	}
}

func TestFolderContentsStorageIDsAndSize(t *testing.T) {
	svc, db, _ := newTestService(t)

	project(t, db, 10, projection.Op{
		Type:   projection.OpMkdir,
		Obj:    "d:docs",
		Parent: projection.RootParent,
		Name:   "Docs",
	})
	project(t, db, 20, projection.Op{
		Type:           projection.OpFileUpload,
		Parent:         "d:docs",
		Name:           "notes.txt",
		FileSize:       12,
		FileUploadTime: 111,
	})
	project(t, db, 21, projection.Op{
		Type:           projection.OpFileUpload,
		Parent:         projection.RootParent,
		Name:           "root.txt",
		FileSize:       8,
		FileUploadTime: 112,
	})

	root, err := svc.FolderContents(testChannelID, projection.RootParent)
	if err != nil {
		t.Fatalf("folder contents: %v", err)
	}
	if len(root.Folders) != 1 || root.Folders[0].Name != "Docs" {
		t.Fatalf("root folders = %+v", root.Folders)
	}
	if len(root.Files) != 1 || root.Files[0].Name != "root.txt" {
		t.Fatalf("root files = %+v", root.Files)
	}

	used, err := svc.StorageUsed(testChannelID)
	if err != nil {
		t.Fatalf("storage used: %v", err)
	}
	if used != 20 {
		t.Fatalf("storage used = %d, want 20", used)
	}

	ids, err := svc.AllFileMsgIDs(testChannelID)
	if err != nil {
		t.Fatalf("all ids: %v", err)
	}
	if len(ids) != 2 {
		t.Fatalf("ids = %+v, want 2 ids", ids)
	}

	size, err := svc.FolderSize(testChannelID, "d:docs")
	if err != nil {
		t.Fatalf("folder size: %v", err)
	}
	if size != 12 {
		t.Fatalf("folder size = %d, want 12", size)
	}
}

func TestSearchBuildsFolderPaths(t *testing.T) {
	svc, db, _ := newTestService(t)

	project(t, db, 10, projection.Op{Type: projection.OpMkdir, Obj: "d:a", Parent: "", Name: "A"})
	project(t, db, 11, projection.Op{Type: projection.OpMkdir, Obj: "d:b", Parent: "d:a", Name: "B"})
	project(t, db, 20, projection.Op{
		Type:           projection.OpFileUpload,
		Parent:         "d:b",
		Name:           "paper.pdf",
		FileSize:       99,
		FileUploadTime: 222,
	})

	results, err := svc.Search(testChannelID, "paper", 20)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("results = %+v, want 1", results)
	}
	if results[0].Path != "My Drive / A / B" {
		t.Fatalf("path = %q, want nested path", results[0].Path)
	}
}

func TestOrphanedFiles(t *testing.T) {
	svc, db, _ := newTestService(t)

	project(t, db, 10, projection.Op{Type: projection.OpMkdir, Obj: "d:a", Parent: "", Name: "A"})
	project(t, db, 11, projection.Op{Type: projection.OpMkdir, Obj: "d:b", Parent: "d:a", Name: "B"})
	project(t, db, 20, projection.Op{
		Type:           projection.OpFileUpload,
		Parent:         "d:b",
		Name:           "lost.txt",
		FileSize:       3,
		FileUploadTime: 333,
	})
	project(t, db, 30, projection.Op{Type: projection.OpRmdir, Obj: "d:a"})

	orphans, err := svc.OrphanedFiles(testChannelID)
	if err != nil {
		t.Fatalf("orphans: %v", err)
	}
	if len(orphans) != 1 || orphans[0].Name != "lost.txt" {
		t.Fatalf("orphans = %+v", orphans)
	}
}

func TestTelegramRootFilesUsesTGHistory(t *testing.T) {
	svc, _, fakeTG := newTestService(t)
	fakeTG.SeedHistory(
		tgclient.HistoryMessage{
			MsgID:              50,
			Date:               500,
			HasMedia:           true,
			MediaSize:          123,
			DocumentName:       "legacy.bin",
			DocumentAccessHash: 777,
		},
		tgclient.HistoryMessage{
			MsgID:    51,
			Date:     501,
			HasMedia: false,
			Text:     "not a file",
		},
	)

	files, err := svc.TelegramRootFiles(context.Background(), testChannelID)
	if err != nil {
		t.Fatalf("telegram root files: %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("files = %+v, want 1", files)
	}
	if files[0].ID != 50 || files[0].Name != "legacy.bin" || files[0].Size != 123 || files[0].AccessHash != 777 {
		t.Fatalf("bad file: %+v", files[0])
	}
}

func TestTelegramRootFilesSkipsMultipartParts(t *testing.T) {
	svc, _, fakeTG := newTestService(t)
	partCaption := projection.Format(projection.Op{
		Type: projection.OpFilePart, UploadUUID: "u1", PartIndex: 0, FileSize: 1024,
	})
	fakeTG.SeedHistory(
		// A multipart part document: has media, but a t=part header.
		tgclient.HistoryMessage{
			MsgID: 60, Date: 600, HasMedia: true, MediaSize: 1024,
			DocumentName: "part-00000", Text: partCaption,
		},
		// A genuine loose document with no TDrive header stays visible.
		tgclient.HistoryMessage{
			MsgID: 61, Date: 601, HasMedia: true, MediaSize: 50,
			DocumentName: "real.bin",
		},
	)

	files, err := svc.TelegramRootFiles(context.Background(), testChannelID)
	if err != nil {
		t.Fatalf("telegram root files: %v", err)
	}
	if len(files) != 1 || files[0].Name != "real.bin" {
		t.Fatalf("files = %+v, want only real.bin (multipart parts skipped)", files)
	}
}
