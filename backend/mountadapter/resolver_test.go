package mountadapter

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"TDrive/backend/mountdav"
	"TDrive/backend/mountfs"
	"TDrive/backend/projection"

	_ "modernc.org/sqlite"
)

func TestProjectionResolverWalksPortableCurrentNamespace(t *testing.T) {
	db := newProjectionDB(t)
	project(t, db, 10, projection.Op{
		Type: projection.OpFolderCommit, ProtocolVersion: 1, OpID: "mkdir-docs",
		Obj: "d:docs", Parent: projection.RootParent, Name: "Docs",
	})
	project(t, db, 20, projection.Op{Type: projection.OpFilePart, UploadUUID: "upload-1", PartIndex: 0, FileSize: 5})
	project(t, db, 21, projection.Op{
		Type: projection.OpFileCommit, ProtocolVersion: 1, OpID: "put-report",
		Parent: "d:docs", Name: "Report.txt", UploadUUID: "upload-1", PartCount: 1,
		FileSize: 5, PlaintextSize: 5, ContentHash: "abc",
	})

	resolver, err := NewProjectionResolver(db, testDriveID)
	if err != nil {
		t.Fatalf("NewProjectionResolver: %v", err)
	}
	root, found, err := resolver.Resolve(context.Background(), "/")
	if err != nil || !found || root.Kind != mountfs.KindDirectory || root.ObjectID != projection.RootParent {
		t.Fatalf("root = %+v, %v, %v", root, found, err)
	}
	folder, folderFound, folderErr := resolver.Resolve(context.Background(), "/docs")
	if folderErr != nil || !folderFound {
		t.Fatalf("Resolve folder = %+v, %v, %v", folder, folderFound, folderErr)
	}
	file, found, err := resolver.Resolve(context.Background(), "/docs/REPORT.txt")
	if err != nil || !found {
		t.Fatalf("Resolve file = %+v, %v, %v", file, found, err)
	}
	if file.ObjectID != "f:21" || file.ParentID != "d:docs" || file.Revision != 1 || file.Size != 5 || file.ContentHash != "abc" {
		t.Fatalf("file = %+v", file)
	}
	if _, found, err := resolver.Resolve(context.Background(), "/Docs/missing"); err != nil || found {
		t.Fatalf("missing = %v, %v", found, err)
	}
}

func TestProjectionResolverHonorsCancellationAndValidation(t *testing.T) {
	db := newProjectionDB(t)
	if _, err := NewProjectionResolver(nil, testDriveID); err == nil {
		t.Fatal("nil DB accepted")
	}
	if _, err := NewProjectionResolver(db, 0); err == nil {
		t.Fatal("zero drive accepted")
	}
	resolver, err := NewProjectionResolver(db, testDriveID)
	if err != nil {
		t.Fatal(err)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := resolver.Resolve(canceled, "/"); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled error = %v", err)
	}
	for _, value := range []string{"relative", "/a//b", "/a/../b", "/a\\b"} {
		if _, _, err := resolver.Resolve(context.Background(), value); !errors.Is(err, mountdav.ErrWriteInvalid) {
			t.Fatalf("path %q error = %v", value, err)
		}
	}
}

func TestProjectionResolverRoundTripsLegacyCrossTypeCollisionAlias(t *testing.T) {
	db := newProjectionDB(t)
	project(t, db, 10, projection.Op{
		Type: projection.OpMkdir, Obj: "d:report", Parent: projection.RootParent, Name: "Report",
	})
	project(t, db, 11, projection.Op{
		Type: projection.OpFileUpload, Parent: projection.RootParent, Name: "report", FileSize: 4,
	})

	fileDirent, found, err := projection.DirentByID(db, testDriveID, "f:11")
	if err != nil || !found {
		t.Fatalf("DirentByID = %+v, found=%v, err=%v", fileDirent, found, err)
	}
	if fileDirent.DisplayName == "report" {
		t.Fatalf("legacy collision was not assigned a display alias: %+v", fileDirent)
	}
	legacyFile, found, err := projection.FileByID(db, testDriveID, 11)
	if err != nil || !found || legacyFile.Name != "report" {
		t.Fatalf("legacy file = %+v, found=%v, err=%v", legacyFile, found, err)
	}

	resolver, err := NewProjectionResolver(db, testDriveID)
	if err != nil {
		t.Fatalf("NewProjectionResolver: %v", err)
	}
	resolved, found, err := resolver.Resolve(context.Background(), "/"+fileDirent.DisplayName)
	if err != nil || !found || resolved.ObjectID != "f:11" || resolved.Name != fileDirent.DisplayName {
		t.Fatalf("Resolve alias = %+v, found=%v, err=%v", resolved, found, err)
	}
}

func newProjectionDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	if err := projection.MigratePersonalChannel(db, testDriveID); err != nil {
		t.Fatalf("MigratePersonalChannel: %v", err)
	}
	return db
}

func project(t *testing.T, db *sql.DB, msgID int64, op projection.Op) {
	t.Helper()
	header := projection.Format(op)
	if _, err := projection.ProjectFromOp(db, testDriveID, msgID, op, 1, header); err != nil {
		t.Fatalf("ProjectFromOp(%s): %v", op.Type, err)
	}
	if op.OpID != "" {
		outcome, found, err := projection.ProjectionOperationByID(db, testDriveID, op.OpID)
		if err != nil || !found || outcome.Outcome != projection.OperationApplied {
			t.Fatalf("projection outcome for %s = %+v, found=%v, err=%v", op.Type, outcome, found, err)
		}
	}
}
