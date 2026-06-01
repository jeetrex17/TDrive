package projection

import (
	"testing"
)

func TestProjectFromOpInsertsLogAndProjectsAtomically(t *testing.T) {
	db := newTestDB(t)
	op := Op{Type: OpMkdir, Obj: "d:goa", Parent: RootParent, Name: "Goa"}
	header := Format(op)

	already, err := ProjectFromOp(db, testChan, 1, op, 0, header)
	if err != nil {
		t.Fatalf("first project: %v", err)
	}
	if already {
		t.Fatal("first call should not be alreadySeen")
	}

	if !FolderExists(db, testChan, "d:goa") {
		t.Fatal("folder missing after project")
	}

	var rows int
	if err := db.QueryRow(`SELECT COUNT(*) FROM replay_log WHERE channel_id=? AND msg_id=?`, testChan, 1).Scan(&rows); err != nil {
		t.Fatalf("count log: %v", err)
	}
	if rows != 1 {
		t.Fatalf("replay_log rows = %d, want 1", rows)
	}
}

func TestProjectFromOpIsIdempotentSameHash(t *testing.T) {
	db := newTestDB(t)
	op := Op{Type: OpMkdir, Obj: "d:goa", Parent: RootParent, Name: "Goa"}
	header := Format(op)

	if _, err := ProjectFromOp(db, testChan, 1, op, 0, header); err != nil {
		t.Fatalf("first: %v", err)
	}
	already, err := ProjectFromOp(db, testChan, 1, op, 0, header)
	if err != nil {
		t.Fatalf("second: %v", err)
	}
	if !already {
		t.Fatal("second call should be alreadySeen")
	}

	var rows int
	if err := db.QueryRow(`SELECT COUNT(*) FROM replay_log WHERE channel_id=? AND msg_id=?`, testChan, 1).Scan(&rows); err != nil {
		t.Fatalf("count: %v", err)
	}
	if rows != 1 {
		t.Fatalf("replay_log rows = %d, want 1", rows)
	}

	var tamperRows int
	if err := db.QueryRow(`SELECT COUNT(*) FROM replay_log_tamper WHERE channel_id=? AND msg_id=?`, testChan, 1).Scan(&tamperRows); err != nil {
		t.Fatalf("count tamper: %v", err)
	}
	if tamperRows != 0 {
		t.Fatal("same hash must not record tamper")
	}
}

func TestProjectFromOpDetectsTamper(t *testing.T) {
	db := newTestDB(t)
	op := Op{Type: OpMkdir, Obj: "d:goa", Parent: RootParent, Name: "Goa"}
	originalHeader := Format(op)
	if _, err := ProjectFromOp(db, testChan, 1, op, 0, originalHeader); err != nil {
		t.Fatalf("first: %v", err)
	}

	// Same msg_id, different header (someone edited the caption to rename
	// the folder). Tamper should be recorded; the original op stays canonical.
	editedOp := Op{Type: OpMkdir, Obj: "d:goa", Parent: RootParent, Name: "Hijack"}
	editedHeader := Format(editedOp)
	already, err := ProjectFromOp(db, testChan, 1, editedOp, 0, editedHeader)
	if err != nil {
		t.Fatalf("edited: %v", err)
	}
	if !already {
		t.Fatal("tampered op must report alreadySeen=true")
	}

	var tamperRows int
	if err := db.QueryRow(`SELECT COUNT(*) FROM replay_log_tamper WHERE channel_id=? AND msg_id=?`, testChan, 1).Scan(&tamperRows); err != nil {
		t.Fatalf("count tamper: %v", err)
	}
	if tamperRows != 1 {
		t.Fatalf("tamper rows = %d, want 1", tamperRows)
	}

	var name string
	if err := db.QueryRow(`SELECT name FROM folders WHERE channel_id=? AND id=?`, testChan, "d:goa").Scan(&name); err != nil {
		t.Fatalf("scan folder: %v", err)
	}
	if name != "Goa" {
		t.Fatalf("name = %q, want Goa (original op should stay canonical)", name)
	}
}

func TestProjectFromOpRecordsAndSkipsRejectedApplyError(t *testing.T) {
	db := newTestDB(t)
	op := Op{Type: OpRename, Obj: "raw", Name: "x"}
	header := Format(op)

	already, err := ProjectFromOp(db, testChan, 1, op, 0, header)
	if err != nil {
		t.Fatalf("project rejected op: %v", err)
	}
	if already {
		t.Fatal("fresh rejected op should not be alreadySeen")
	}

	var rows int
	if err := db.QueryRow(`SELECT COUNT(*) FROM replay_log WHERE channel_id=? AND msg_id=?`, testChan, 1).Scan(&rows); err != nil {
		t.Fatalf("count: %v", err)
	}
	if rows != 1 {
		t.Fatalf("replay_log row count = %d, want 1", rows)
	}

	var rejectRows int
	if err := db.QueryRow(`SELECT COUNT(*) FROM replay_log_rejects WHERE channel_id=? AND msg_id=?`, testChan, 1).Scan(&rejectRows); err != nil {
		t.Fatalf("count rejects: %v", err)
	}
	if rejectRows != 1 {
		t.Fatalf("reject row count = %d, want 1", rejectRows)
	}
}

func TestProjectFromOpTxAtomicWithCallerCheckpoint(t *testing.T) {
	db := newTestDB(t)
	tx, err := db.Begin()
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	op := Op{Type: OpMkdir, Obj: "d:goa", Parent: RootParent, Name: "Goa"}
	header := Format(op)

	already, err := ProjectFromOpTx(tx, testChan, 1, op, 0, header)
	if err != nil {
		_ = tx.Rollback()
		t.Fatalf("project: %v", err)
	}
	if already {
		_ = tx.Rollback()
		t.Fatal("first call should not be alreadySeen")
	}

	// Caller writes a checkpoint in the same tx.
	if _, err := tx.Exec(`
		INSERT INTO backfill_progress (channel_id, cursor_obj_id, cursor_kind, started_at, updated_at)
		VALUES (?, ?, 'folder', ?, ?)
		ON CONFLICT(channel_id) DO UPDATE SET cursor_obj_id=excluded.cursor_obj_id, updated_at=excluded.updated_at
	`, testChan, "d:goa", 0, 0); err != nil {
		_ = tx.Rollback()
		t.Fatalf("checkpoint: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}

	if !FolderExists(db, testChan, "d:goa") {
		t.Fatal("folder missing after commit")
	}
	var cursor string
	if err := db.QueryRow(`SELECT cursor_obj_id FROM backfill_progress WHERE channel_id=?`, testChan).Scan(&cursor); err != nil {
		t.Fatalf("read cursor: %v", err)
	}
	if cursor != "d:goa" {
		t.Fatalf("cursor = %q, want d:goa", cursor)
	}
}

func TestChannelIsEmptyDetectsAnyRow(t *testing.T) {
	db := newTestDB(t)
	empty, err := ChannelIsEmpty(db, testChan)
	if err != nil {
		t.Fatalf("empty check: %v", err)
	}
	if !empty {
		t.Fatal("freshly migrated channel should be empty")
	}

	op := Op{Type: OpMkdir, Obj: "d:x", Parent: RootParent, Name: "X"}
	if _, err := ProjectFromOp(db, testChan, 1, op, 0, Format(op)); err != nil {
		t.Fatalf("project: %v", err)
	}

	empty, err = ChannelIsEmpty(db, testChan)
	if err != nil {
		t.Fatalf("empty check 2: %v", err)
	}
	if empty {
		t.Fatal("channel should not be empty after a project")
	}
}
