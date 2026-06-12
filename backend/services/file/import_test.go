package file

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"TDrive/backend/projection"
)

// testFolderCreator returns a CreateFolderFunc that projects an mkdir op (so
// NextFreeFolderName sees created folders) and hands back a unique id.
func testFolderCreator(db *sql.DB) CreateFolderFunc {
	var n, msgID int64 = 0, 50000
	return func(channelID int64, name, parentID string) (string, error) {
		n++
		id := fmt.Sprintf("%simp%d", projection.FolderIDPrefix, n)
		op := projection.Op{Type: projection.OpMkdir, Obj: id, Parent: parentID, Name: name}
		msgID++
		tx, err := db.Begin()
		if err != nil {
			return "", err
		}
		if err := projection.ApplyOp(tx, channelID, msgID, op, 7); err != nil {
			_ = tx.Rollback()
			return "", err
		}
		if err := tx.Commit(); err != nil {
			return "", err
		}
		return id, nil
	}
}

func mkfile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func countLiveFiles(t *testing.T, db *sql.DB, channelID int64) int {
	t.Helper()
	var n int
	if err := db.QueryRow("SELECT COUNT(*) FROM files WHERE channel_id=? AND tombstoned=0", channelID).Scan(&n); err != nil {
		t.Fatalf("count files: %v", err)
	}
	return n
}

func childFolderNames(t *testing.T, db *sql.DB, channelID int64, parentID string) []string {
	t.Helper()
	rows, err := db.Query("SELECT name FROM folders WHERE channel_id=? AND parent_id=? AND tombstoned=0 ORDER BY name", channelID, parentID)
	if err != nil {
		t.Fatalf("query folders: %v", err)
	}
	defer rows.Close()
	var names []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			t.Fatal(err)
		}
		names = append(names, n)
	}
	return names
}

func folderIDByName(t *testing.T, db *sql.DB, channelID int64, parentID, name string) string {
	t.Helper()
	var id string
	err := db.QueryRow("SELECT id FROM folders WHERE channel_id=? AND parent_id=? AND name=? AND tombstoned=0", channelID, parentID, name).Scan(&id)
	if err != nil {
		t.Fatalf("folder %q under %q not found: %v", name, parentID, err)
	}
	return id
}

func fileParentID(t *testing.T, db *sql.DB, channelID int64, name string) string {
	t.Helper()
	var parent string
	err := db.QueryRow("SELECT parent_id FROM files WHERE channel_id=? AND name=? AND tombstoned=0", channelID, name).Scan(&parent)
	if err != nil {
		t.Fatalf("file %q not found: %v", name, err)
	}
	return parent
}

func TestPlanImportCountsTreeAndArchive(t *testing.T) {
	svc, _, _, _ := newTestService(t)

	root := filepath.Join(t.TempDir(), "Trip")
	mkfile(t, filepath.Join(root, "a.txt"), "hello")
	mkfile(t, filepath.Join(root, "sub", "b.txt"), "world")
	mkfile(t, filepath.Join(root, ".DS_Store"), "junk")

	plan := svc.PlanImport([]string{root}, false, false)
	if plan.Files != 2 {
		t.Errorf("plan.Files = %d, want 2 (junk excluded)", plan.Files)
	}
	if plan.Folders != 2 { // Trip + sub
		t.Errorf("plan.Folders = %d, want 2", plan.Folders)
	}
	if plan.Bytes != 10 {
		t.Errorf("plan.Bytes = %d, want 10", plan.Bytes)
	}

	zip := buildZip(t, map[string]string{"doc.txt": "x", "nested/inner.txt": "yy"}, nil, nil)
	plan = svc.PlanImport([]string{zip}, false, true)
	if plan.Archives != 1 {
		t.Errorf("plan.Archives = %d, want 1", plan.Archives)
	}
	if plan.Files != 2 {
		t.Errorf("archive plan.Files = %d, want 2", plan.Files)
	}
	if plan.Folders < 2 { // archive-named folder + nested
		t.Errorf("archive plan.Folders = %d, want >= 2", plan.Folders)
	}
}

func TestPlanImportFlagsOversize(t *testing.T) {
	svc, _, _, _ := newTestService(t)
	svc.MaxUploadBytes = 3

	root := filepath.Join(t.TempDir(), "Big")
	mkfile(t, filepath.Join(root, "a.txt"), "hello") // 5 bytes, over 3
	mkfile(t, filepath.Join(root, "b.txt"), "world") // 5 bytes, over 3

	plan := svc.PlanImport([]string{root}, false, false)
	if plan.Oversize != 2 {
		t.Errorf("plan.Oversize = %d, want 2", plan.Oversize)
	}
	if plan.Files != 0 {
		t.Errorf("plan.Files = %d, want 0 (all oversize)", plan.Files)
	}
	if plan.MaxBytes != 3 {
		t.Errorf("plan.MaxBytes = %d, want 3", plan.MaxBytes)
	}
}

func TestPlanImportCorruptArchiveDoesNotCountExtractFolder(t *testing.T) {
	svc, _, _, _ := newTestService(t)
	p := filepath.Join(t.TempDir(), "bad.zip")
	if err := os.WriteFile(p, []byte("not a zip"), 0o644); err != nil {
		t.Fatal(err)
	}

	plan := svc.PlanImport([]string{p}, false, true)
	if plan.Archives != 1 {
		t.Errorf("plan.Archives = %d, want 1", plan.Archives)
	}
	if plan.Folders != 0 {
		t.Errorf("plan.Folders = %d, want 0 for corrupt archive fallback", plan.Folders)
	}
	if plan.Files != 1 {
		t.Errorf("plan.Files = %d, want fallback archive upload", plan.Files)
	}
	if len(plan.Errors) != 1 {
		t.Errorf("plan.Errors = %d, want 1", len(plan.Errors))
	}
}

func TestRunImportFolderTree(t *testing.T) {
	svc, db, _, _ := newTestService(t)
	svc.CreateFolder = testFolderCreator(db)

	root := filepath.Join(t.TempDir(), "Photos")
	mkfile(t, filepath.Join(root, "a.txt"), "a")
	mkfile(t, filepath.Join(root, "sub", "b.txt"), "b")
	mkfile(t, filepath.Join(root, "sub", "deep", "c.txt"), "c")
	mkfile(t, filepath.Join(root, ".DS_Store"), "junk")

	if err := svc.RunImport(context.Background(), personalChannelID, []string{root}, "", false, false); err != nil {
		t.Fatalf("RunImport: %v", err)
	}

	if n := countLiveFiles(t, db, personalChannelID); n != 3 {
		t.Fatalf("uploaded files = %d, want 3 (junk skipped)", n)
	}
	photos := folderIDByName(t, db, personalChannelID, "", "Photos")
	sub := folderIDByName(t, db, personalChannelID, photos, "sub")
	deep := folderIDByName(t, db, personalChannelID, sub, "deep")

	if got := fileParentID(t, db, personalChannelID, "a.txt"); got != photos {
		t.Errorf("a.txt parent = %q, want Photos %q", got, photos)
	}
	if got := fileParentID(t, db, personalChannelID, "b.txt"); got != sub {
		t.Errorf("b.txt parent = %q, want sub %q", got, sub)
	}
	if got := fileParentID(t, db, personalChannelID, "c.txt"); got != deep {
		t.Errorf("c.txt parent = %q, want deep %q", got, deep)
	}
}

func TestRunImportRejectsMissingParentBeforeCreatingFolders(t *testing.T) {
	svc, db, _, _ := newTestService(t)
	svc.CreateFolder = testFolderCreator(db)

	root := filepath.Join(t.TempDir(), "Photos")
	mkfile(t, filepath.Join(root, "a.txt"), "a")

	err := svc.RunImport(context.Background(), personalChannelID, []string{root}, "d:missing", false, false)
	if err == nil {
		t.Fatal("RunImport should reject a missing parent")
	}
	if names := childFolderNames(t, db, personalChannelID, ""); len(names) != 0 {
		t.Fatalf("created folders despite missing parent: %v", names)
	}
}

func TestRunImportChecksEncryptionBeforeCreatingFolders(t *testing.T) {
	svc, db, _, _ := newTestService(t)
	svc.CreateFolder = testFolderCreator(db)
	svc.MasterKeyForUpload = func(channelID int64, wantEncrypted bool) ([]byte, error) {
		if wantEncrypted {
			return nil, fmt.Errorf("missing key")
		}
		return nil, nil
	}

	root := filepath.Join(t.TempDir(), "Photos")
	mkfile(t, filepath.Join(root, "a.txt"), "a")

	err := svc.RunImport(context.Background(), personalChannelID, []string{root}, "", true, false)
	if err == nil {
		t.Fatal("RunImport should reject missing encryption key")
	}
	if names := childFolderNames(t, db, personalChannelID, ""); len(names) != 0 {
		t.Fatalf("created folders despite encryption preflight failure: %v", names)
	}
}

func TestRunImportNameCollisionSuffixes(t *testing.T) {
	svc, db, _, _ := newTestService(t)
	create := testFolderCreator(db)
	svc.CreateFolder = create

	// A folder named "Photos" already exists at the root.
	if _, err := create(personalChannelID, "Photos", ""); err != nil {
		t.Fatal(err)
	}

	root := filepath.Join(t.TempDir(), "Photos")
	mkfile(t, filepath.Join(root, "x.txt"), "x")
	if err := svc.RunImport(context.Background(), personalChannelID, []string{root}, "", false, false); err != nil {
		t.Fatalf("RunImport: %v", err)
	}

	names := childFolderNames(t, db, personalChannelID, "")
	if !slices.Contains(names, "Photos") || !slices.Contains(names, "Photos (2)") {
		t.Errorf("root folders = %v, want both Photos and Photos (2)", names)
	}
}

func TestRunImportSkipsOversize(t *testing.T) {
	svc, db, _, _ := newTestService(t)
	svc.CreateFolder = testFolderCreator(db)
	svc.MaxUploadBytes = 10

	root := filepath.Join(t.TempDir(), "Mix")
	mkfile(t, filepath.Join(root, "small.txt"), "hello")                  // 5 bytes
	mkfile(t, filepath.Join(root, "big.txt"), "this is over ten bytes!!") // > 10

	if err := svc.RunImport(context.Background(), personalChannelID, []string{root}, "", false, false); err != nil {
		t.Fatalf("RunImport: %v", err)
	}
	if n := countLiveFiles(t, db, personalChannelID); n != 1 {
		t.Fatalf("uploaded files = %d, want 1 (big skipped)", n)
	}
	if got := fileParentID(t, db, personalChannelID, "small.txt"); got == "" {
		t.Error("small.txt should have been uploaded")
	}
}

func TestRunImportArchiveExtract(t *testing.T) {
	svc, db, _, _ := newTestService(t)
	svc.CreateFolder = testFolderCreator(db)

	zip := buildZip(t, map[string]string{"doc.txt": "x", "nested/inner.txt": "yy"}, nil, nil)
	if err := svc.RunImport(context.Background(), personalChannelID, []string{zip}, "", false, true); err != nil {
		t.Fatalf("RunImport: %v", err)
	}

	if n := countLiveFiles(t, db, personalChannelID); n != 2 {
		t.Fatalf("uploaded files = %d, want 2", n)
	}
	// buildZip names the archive "a.zip", so it imports into a folder "a".
	top := folderIDByName(t, db, personalChannelID, "", "a")
	nested := folderIDByName(t, db, personalChannelID, top, "nested")
	if got := fileParentID(t, db, personalChannelID, "doc.txt"); got != top {
		t.Errorf("doc.txt parent = %q, want archive folder %q", got, top)
	}
	if got := fileParentID(t, db, personalChannelID, "inner.txt"); got != nested {
		t.Errorf("inner.txt parent = %q, want nested %q", got, nested)
	}
}

func TestRunImportArchiveCollapsesDuplicateRootAndSkipsMacOSMetadata(t *testing.T) {
	svc, db, _, _ := newTestService(t)
	svc.CreateFolder = testFolderCreator(db)

	zip := buildZip(t, map[string]string{
		"a/doc.txt":             "x",
		"a/nested/inner.txt":    "yy",
		"__MACOSX/a/._doc.txt":  "metadata",
		"__MACOSX/a/hidden.txt": "metadata",
		"a/__MACOSX/hidden.txt": "metadata",
		"a/nested/.DS_Store":    "metadata",
		"a/nested/._inner.txt":  "metadata",
	}, nil, nil)
	plan := svc.PlanImport([]string{zip}, false, true)
	if plan.Files != 2 {
		t.Fatalf("plan.Files = %d, want 2 real files", plan.Files)
	}
	if plan.Folders != 2 { // archive folder + nested, not archive/a/nested
		t.Fatalf("plan.Folders = %d, want 2 after duplicate root collapse", plan.Folders)
	}

	if err := svc.RunImport(context.Background(), personalChannelID, []string{zip}, "", false, true); err != nil {
		t.Fatalf("RunImport: %v", err)
	}

	top := folderIDByName(t, db, personalChannelID, "", "a")
	children := childFolderNames(t, db, personalChannelID, top)
	if slices.Contains(children, "a") {
		t.Fatalf("duplicate archive root folder was created under archive folder: %v", children)
	}
	if !slices.Contains(children, "nested") {
		t.Fatalf("nested folder missing after archive import: %v", children)
	}
	if slices.Contains(children, "__MACOSX") {
		t.Fatalf("__MACOSX folder should be skipped: %v", children)
	}
	if n := countLiveFiles(t, db, personalChannelID); n != 2 {
		t.Fatalf("uploaded files = %d, want 2 real files", n)
	}
}

func TestRunImportArchiveSkipsOversizeEntry(t *testing.T) {
	svc, db, _, _ := newTestService(t)
	svc.CreateFolder = testFolderCreator(db)
	svc.MaxUploadBytes = 10

	// big.txt exceeds the cap and must be skipped during extraction/upload;
	// small.txt is imported normally.
	zip := buildZip(t, map[string]string{
		"small.txt": "hi",
		"big.txt":   "this content is definitely well over ten bytes",
	}, nil, nil)

	if err := svc.RunImport(context.Background(), personalChannelID, []string{zip}, "", false, true); err != nil {
		t.Fatalf("RunImport: %v", err)
	}
	if n := countLiveFiles(t, db, personalChannelID); n != 1 {
		t.Fatalf("uploaded files = %d, want 1 (oversize archive entry skipped)", n)
	}
	if got := fileParentID(t, db, personalChannelID, "small.txt"); got == "" {
		t.Error("small.txt should have been imported")
	}
}
