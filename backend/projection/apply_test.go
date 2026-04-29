package projection

import (
	"database/sql"
	"errors"
	"testing"

	_ "modernc.org/sqlite"
)

func newTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	if _, err := db.Exec(`PRAGMA foreign_keys=ON;`); err != nil {
		t.Fatalf("pragma: %v", err)
	}
	if err := MigratePersonalChannel(db, testChan); err != nil {
		t.Fatalf("MigratePersonalChannel: %v", err)
	}
	return db
}

func runOp(t *testing.T, db *sql.DB, channelID, msgID int64, op Op) error {
	t.Helper()
	tx, err := db.Begin()
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	if err := ApplyOp(tx, channelID, msgID, op, 0); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

const testChan int64 = 1001

func TestApplyMkdirAtRoot(t *testing.T) {
	db := newTestDB(t)
	if err := runOp(t, db, testChan, 1, Op{Type: OpMkdir, Obj: "d:goa", Parent: RootParent, Name: "Goa"}); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if !FolderExists(db, testChan, "d:goa") {
		t.Fatal("folder not created")
	}
}

func TestApplyMkdirIdempotent(t *testing.T) {
	db := newTestDB(t)
	op := Op{Type: OpMkdir, Obj: "d:goa", Parent: RootParent, Name: "Goa"}
	if err := runOp(t, db, testChan, 1, op); err != nil {
		t.Fatalf("first: %v", err)
	}
	if err := runOp(t, db, testChan, 2, op); err != nil {
		t.Fatalf("second: %v", err)
	}
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM folders WHERE channel_id=? AND id=?`, testChan, "d:goa").Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 1 {
		t.Fatalf("count = %d, want 1", count)
	}
}

func TestApplyMkdirFirstWinsAgainstDifferentNameOrParent(t *testing.T) {
	db := newTestDB(t)
	mustOp(t, db, 1, Op{Type: OpMkdir, Obj: "d:other", Parent: RootParent, Name: "Other"})
	mustOp(t, db, 2, Op{Type: OpMkdir, Obj: "d:goa", Parent: RootParent, Name: "Goa"})

	// A second mkdir for the same object id with different name and parent must
	// be a no-op. Identity changes only flow through rename/move ops.
	mustOp(t, db, 3, Op{Type: OpMkdir, Obj: "d:goa", Parent: "d:other", Name: "Hijack"})

	var (
		name   string
		parent string
	)
	err := db.QueryRow(
		`SELECT name, parent_id FROM folders WHERE channel_id=? AND id=?`,
		testChan, "d:goa",
	).Scan(&name, &parent)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if name != "Goa" {
		t.Fatalf("name = %q want Goa (first mkdir should win)", name)
	}
	if parent != RootParent {
		t.Fatalf("parent = %q want root (first mkdir should win)", parent)
	}
}

func TestApplyMkdirRequiresFolderPrefix(t *testing.T) {
	db := newTestDB(t)
	err := runOp(t, db, testChan, 1, Op{Type: OpMkdir, Obj: "raw", Parent: RootParent, Name: "x"})
	if !errors.Is(err, ErrBadOp) {
		t.Fatalf("err = %v want ErrBadOp", err)
	}
}

func TestApplyMkdirRejectsNonFolderParent(t *testing.T) {
	db := newTestDB(t)
	err := runOp(t, db, testChan, 1, Op{Type: OpMkdir, Obj: "d:x", Parent: "raw", Name: "x"})
	if !errors.Is(err, ErrBadOp) {
		t.Fatalf("err = %v want ErrBadOp", err)
	}
}

func TestApplyFileUpload(t *testing.T) {
	db := newTestDB(t)
	if err := runOp(t, db, testChan, 100, Op{
		Type:           OpFileUpload,
		Parent:         RootParent,
		Name:           "sunset.jpg",
		FileSize:       4321,
		FileUploadTime: 99,
	}); err != nil {
		t.Fatalf("upload: %v", err)
	}
	if !FileExists(db, testChan, 100) {
		t.Fatal("file not registered")
	}
	if got := LookupFileName(db, testChan, 100); got != "sunset.jpg" {
		t.Fatalf("name = %q", got)
	}
}

func TestApplyFileUploadIdempotentOnSameMsgID(t *testing.T) {
	db := newTestDB(t)
	op := Op{Type: OpFileUpload, Parent: RootParent, Name: "x.png", FileSize: 1, FileUploadTime: 1}
	if err := runOp(t, db, testChan, 100, op); err != nil {
		t.Fatalf("first: %v", err)
	}
	if err := runOp(t, db, testChan, 100, op); err != nil {
		t.Fatalf("second: %v", err)
	}
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM files WHERE channel_id=? AND msg_id=?`, testChan, 100).Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 1 {
		t.Fatalf("count = %d, want 1", count)
	}
}

func TestApplyMetaPreservesOriginalUploader(t *testing.T) {
	db := newTestDB(t)

	// f op from actor=5 records the original uploader.
	tx, _ := db.Begin()
	if err := ApplyOp(tx, testChan, 100, Op{Type: OpFileUpload, Parent: RootParent, Name: "x.png"}, 5); err != nil {
		t.Fatalf("upload: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}

	// A later meta op from a different actor (typical "B promotes A's file"
	// case via MsgToTdriveSystem). Uploader must NOT change.
	tx, _ = db.Begin()
	metaOp := Op{Type: OpMeta, Obj: "f:100", Parent: RootParent, Name: "x.png"}
	if err := ApplyOp(tx, testChan, 200, metaOp, 7); err != nil {
		t.Fatalf("meta: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}

	var uploader int64
	if err := db.QueryRow(
		`SELECT uploader_user_id FROM files WHERE channel_id=? AND msg_id=?`,
		testChan, 100,
	).Scan(&uploader); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if uploader != 5 {
		t.Fatalf("uploader = %d, want 5 (meta must not overwrite original)", uploader)
	}
}

func TestApplyMetaFillsMissingUploaderForLegacyRows(t *testing.T) {
	db := newTestDB(t)

	// Simulate a row migrated from before Step 3: uploader_user_id = 0.
	// Backfill emits a meta op with the user's actor id; the upsert should
	// promote the legacy 0 to the meta sender's id.
	if _, err := db.Exec(`
		INSERT INTO files (channel_id, msg_id, name, size, parent_id, upload_time, uploader_user_id, tombstoned)
		VALUES (?, ?, ?, ?, ?, ?, 0, 0)
	`, testChan, 100, "legacy.png", 0, RootParent, 0); err != nil {
		t.Fatalf("seed: %v", err)
	}

	tx, _ := db.Begin()
	metaOp := Op{Type: OpMeta, Obj: "f:100", Parent: RootParent, Name: "legacy.png"}
	if err := ApplyOp(tx, testChan, 200, metaOp, 5); err != nil {
		t.Fatalf("meta: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}

	var uploader int64
	if err := db.QueryRow(
		`SELECT uploader_user_id FROM files WHERE channel_id=? AND msg_id=?`,
		testChan, 100,
	).Scan(&uploader); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if uploader != 5 {
		t.Fatalf("uploader = %d, want 5 (legacy 0 should be filled)", uploader)
	}
}

func TestApplyRenameFile(t *testing.T) {
	db := newTestDB(t)
	if err := runOp(t, db, testChan, 1, Op{Type: OpFileUpload, Parent: RootParent, Name: "old.png"}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := runOp(t, db, testChan, 2, Op{Type: OpRename, Obj: "f:1", Name: "new.png"}); err != nil {
		t.Fatalf("rename: %v", err)
	}
	if got := LookupFileName(db, testChan, 1); got != "new.png" {
		t.Fatalf("name = %q", got)
	}
}

func TestApplyRenameFolder(t *testing.T) {
	db := newTestDB(t)
	if err := runOp(t, db, testChan, 1, Op{Type: OpMkdir, Obj: "d:x", Parent: RootParent, Name: "Old"}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := runOp(t, db, testChan, 2, Op{Type: OpRename, Obj: "d:x", Name: "New"}); err != nil {
		t.Fatalf("rename: %v", err)
	}
	var name string
	if err := db.QueryRow(`SELECT name FROM folders WHERE id=?`, "d:x").Scan(&name); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if name != "New" {
		t.Fatalf("name = %q", name)
	}
}

func TestApplyMoveFile(t *testing.T) {
	db := newTestDB(t)
	mustOp(t, db, 1, Op{Type: OpMkdir, Obj: "d:dest", Parent: RootParent, Name: "Dest"})
	mustOp(t, db, 2, Op{Type: OpFileUpload, Parent: RootParent, Name: "x.png"})
	mustOp(t, db, 3, Op{Type: OpMove, Obj: "f:2", Parent: "d:dest"})

	parent, err := FileParent(db, testChan, 2)
	if err != nil {
		t.Fatalf("parent: %v", err)
	}
	if parent != "d:dest" {
		t.Fatalf("parent = %q", parent)
	}
}

func TestApplyMoveFolderRejectsCycle(t *testing.T) {
	db := newTestDB(t)
	mustOp(t, db, 1, Op{Type: OpMkdir, Obj: "d:a", Parent: RootParent, Name: "a"})
	mustOp(t, db, 2, Op{Type: OpMkdir, Obj: "d:b", Parent: "d:a", Name: "b"})

	tx, _ := db.Begin()
	defer tx.Rollback()
	err := ApplyOp(tx, testChan, 3, Op{Type: OpMove, Obj: "d:a", Parent: "d:b"}, 0)
	if !errors.Is(err, ErrCycleRejected) {
		t.Fatalf("err = %v want ErrCycleRejected", err)
	}
}

func TestApplyMoveFolderRejectsSelfParent(t *testing.T) {
	db := newTestDB(t)
	mustOp(t, db, 1, Op{Type: OpMkdir, Obj: "d:a", Parent: RootParent, Name: "a"})

	tx, _ := db.Begin()
	defer tx.Rollback()
	err := ApplyOp(tx, testChan, 2, Op{Type: OpMove, Obj: "d:a", Parent: "d:a"}, 0)
	if !errors.Is(err, ErrCycleRejected) {
		t.Fatalf("err = %v want ErrCycleRejected", err)
	}
}

func TestApplyMoveAcceptsToRoot(t *testing.T) {
	db := newTestDB(t)
	mustOp(t, db, 1, Op{Type: OpMkdir, Obj: "d:a", Parent: RootParent, Name: "a"})
	mustOp(t, db, 2, Op{Type: OpMkdir, Obj: "d:b", Parent: "d:a", Name: "b"})
	mustOp(t, db, 3, Op{Type: OpMove, Obj: "d:b", Parent: RootParent})

	parent, err := FolderParent(db, testChan, "d:b")
	if err != nil {
		t.Fatalf("parent: %v", err)
	}
	if parent != RootParent {
		t.Fatalf("parent = %q", parent)
	}
}

func TestApplyTombHidesFile(t *testing.T) {
	db := newTestDB(t)
	mustOp(t, db, 1, Op{Type: OpFileUpload, Parent: RootParent, Name: "x"})
	mustOp(t, db, 2, Op{Type: OpTomb, Obj: "f:1"})
	if FileExists(db, testChan, 1) {
		t.Fatal("file should be tombstoned")
	}
}

func TestApplyRmdirHidesFolder(t *testing.T) {
	db := newTestDB(t)
	mustOp(t, db, 1, Op{Type: OpMkdir, Obj: "d:a", Parent: RootParent, Name: "a"})
	mustOp(t, db, 2, Op{Type: OpRmdir, Obj: "d:a"})
	if FolderExists(db, testChan, "d:a") {
		t.Fatal("folder should be tombstoned")
	}
}

func TestApplyTombIdempotent(t *testing.T) {
	db := newTestDB(t)
	mustOp(t, db, 1, Op{Type: OpFileUpload, Parent: RootParent, Name: "x"})
	mustOp(t, db, 2, Op{Type: OpTomb, Obj: "f:1"})
	if err := runOp(t, db, testChan, 3, Op{Type: OpTomb, Obj: "f:1"}); err != nil {
		t.Fatalf("second tomb: %v", err)
	}
}

func TestApplyMkdirSameObjIDIsolatedAcrossChannels(t *testing.T) {
	db := newTestDB(t)
	const otherChan int64 = 9999
	if _, err := db.Exec(`
		INSERT INTO channels (channel_id, access_hash, title, kind, joined_at)
		VALUES (?, 0, 'Other', 'shared', 0)
	`, otherChan); err != nil {
		t.Fatalf("seed channel: %v", err)
	}

	if err := runOp(t, db, testChan, 1, Op{Type: OpMkdir, Obj: "d:shared", Parent: RootParent, Name: "FromA"}); err != nil {
		t.Fatalf("first: %v", err)
	}

	tx, err := db.Begin()
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	if err := ApplyOp(tx, otherChan, 1, Op{Type: OpMkdir, Obj: "d:shared", Parent: RootParent, Name: "FromB"}, 0); err != nil {
		_ = tx.Rollback()
		t.Fatalf("second mkdir on other channel: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}

	var rows []struct {
		channelID int64
		name      string
	}
	r, err := db.Query(`SELECT channel_id, name FROM folders WHERE id = ? ORDER BY channel_id`, "d:shared")
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	defer r.Close()
	for r.Next() {
		var cid int64
		var name string
		if err := r.Scan(&cid, &name); err != nil {
			t.Fatalf("scan: %v", err)
		}
		rows = append(rows, struct {
			channelID int64
			name      string
		}{cid, name})
	}
	if len(rows) != 2 {
		t.Fatalf("expected 2 rows (one per channel), got %d", len(rows))
	}
	if rows[0].channelID != testChan || rows[0].name != "FromA" {
		t.Fatalf("row0 = %+v want testChan/FromA", rows[0])
	}
	if rows[1].channelID != otherChan || rows[1].name != "FromB" {
		t.Fatalf("row1 = %+v want otherChan/FromB", rows[1])
	}
}

func TestApplyRejectsUnknownType(t *testing.T) {
	db := newTestDB(t)
	tx, _ := db.Begin()
	defer tx.Rollback()
	err := ApplyOp(tx, testChan, 1, Op{Type: "wat", Obj: "f:1", Name: "x"}, 0)
	if !errors.Is(err, ErrBadOp) {
		t.Fatalf("err = %v want ErrBadOp", err)
	}
}

func mustOp(t *testing.T, db *sql.DB, msgID int64, op Op) {
	t.Helper()
	if err := runOp(t, db, testChan, msgID, op); err != nil {
		t.Fatalf("apply msg=%d %v: %v", msgID, op, err)
	}
}
