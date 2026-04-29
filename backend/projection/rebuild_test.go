package projection

import (
	"database/sql"
	"encoding/json"
	"testing"

	_ "modernc.org/sqlite"
)

func newRebuildDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := MigratePersonalChannel(db, testChan); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}

func seedReplay(t *testing.T, db *sql.DB, channelID int64, msgID int64, op Op) {
	t.Helper()
	payload, err := json.Marshal(op)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	_, err = db.Exec(`
		INSERT INTO replay_log
		  (channel_id, msg_id, op_type, op_payload_json, raw_header, first_seen_hash, actor_user_id, seen_at)
		VALUES (?, ?, ?, ?, '', '', 0, 0)
	`, channelID, msgID, op.Type, string(payload))
	if err != nil {
		t.Fatalf("seed replay: %v", err)
	}
}

func TestRebuildEmptyChannelClears(t *testing.T) {
	db := newRebuildDB(t)
	mustOp(t, db, 1, Op{Type: OpMkdir, Obj: "d:strays", Parent: RootParent, Name: "Stray"})

	if err := RebuildProjection(db, testChan); err != nil {
		t.Fatalf("rebuild: %v", err)
	}
	if FolderExists(db, testChan, "d:strays") {
		t.Fatal("rebuild should have wiped pre-existing rows when replay_log is empty")
	}
}

func TestRebuildReplaysAscending(t *testing.T) {
	db := newRebuildDB(t)
	seedReplay(t, db, testChan, 10, Op{Type: OpMkdir, Obj: "d:a", Parent: RootParent, Name: "First"})
	seedReplay(t, db, testChan, 30, Op{Type: OpRename, Obj: "d:a", Name: "Final"})
	seedReplay(t, db, testChan, 20, Op{Type: OpRename, Obj: "d:a", Name: "Middle"})

	if err := RebuildProjection(db, testChan); err != nil {
		t.Fatalf("rebuild: %v", err)
	}

	var name string
	if err := db.QueryRow(`SELECT name FROM folders WHERE id=?`, "d:a").Scan(&name); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if name != "Final" {
		t.Fatalf("name = %q want Final (highest msg_id wins)", name)
	}
}

func TestRebuildDifferentChannelsAreIsolated(t *testing.T) {
	db := newRebuildDB(t)
	const otherChan int64 = 9999
	if _, err := db.Exec(`
		INSERT INTO channels (channel_id, access_hash, title, kind, joined_at)
		VALUES (?, 0, 'Other', 'shared', 0)
	`, otherChan); err != nil {
		t.Fatalf("seed channel: %v", err)
	}

	seedReplay(t, db, testChan, 1, Op{Type: OpMkdir, Obj: "d:a", Parent: RootParent, Name: "A"})
	seedReplay(t, db, otherChan, 1, Op{Type: OpMkdir, Obj: "d:b", Parent: RootParent, Name: "B"})

	if err := RebuildProjection(db, testChan); err != nil {
		t.Fatalf("rebuild test: %v", err)
	}

	if !FolderExists(db, testChan, "d:a") {
		t.Fatal("d:a missing after rebuild of test channel")
	}
	if FolderExists(db, otherChan, "d:b") {
		t.Fatal("rebuild leaked into other channel — d:b should not be projected because we did not rebuild that channel")
	}
}

func TestRebuildHandlesTombstones(t *testing.T) {
	db := newRebuildDB(t)
	seedReplay(t, db, testChan, 1, Op{Type: OpFileUpload, Parent: RootParent, Name: "x.png", FileSize: 10})
	seedReplay(t, db, testChan, 2, Op{Type: OpTomb, Obj: "f:1"})

	if err := RebuildProjection(db, testChan); err != nil {
		t.Fatalf("rebuild: %v", err)
	}

	if FileExists(db, testChan, 1) {
		t.Fatal("tombstoned file should not be visible after rebuild")
	}
}
