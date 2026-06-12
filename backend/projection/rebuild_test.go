package projection

import (
	"bytes"
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

func TestRebuildClearsAndReplaysFileParts(t *testing.T) {
	db := newRebuildDB(t)

	// A stale file_parts row that no replay_log op accounts for.
	if _, err := db.Exec(`INSERT INTO file_parts (channel_id, upload_uuid, part_index, msg_id, size) VALUES (?, 'stale', 0, 999, 10)`, testChan); err != nil {
		t.Fatalf("seed stale part: %v", err)
	}

	// A real multipart file in the log: two part ops then a manifest.
	seedReplay(t, db, testChan, 1, Op{Type: OpFilePart, UploadUUID: "u1", PartIndex: 0, FileSize: 100})
	seedReplay(t, db, testChan, 2, Op{Type: OpFilePart, UploadUUID: "u1", PartIndex: 1, FileSize: 100})
	seedReplay(t, db, testChan, 3, Op{Type: OpFileManifest, UploadUUID: "u1", Parent: RootParent, Name: "big.bin", FileSize: 200, PartCount: 2})

	if err := RebuildProjection(db, testChan); err != nil {
		t.Fatalf("rebuild: %v", err)
	}

	// The stale row is gone — file_parts reflects exactly the replayed log.
	var staleCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM file_parts WHERE channel_id = ? AND upload_uuid = 'stale'`, testChan).Scan(&staleCount); err != nil {
		t.Fatalf("count stale: %v", err)
	}
	if staleCount != 0 {
		t.Fatalf("stale file_parts survived rebuild = %d, want 0", staleCount)
	}
	// And the real multipart file rebuilt cleanly.
	parts, err := MultipartParts(db, testChan, 3)
	if err != nil || len(parts) != 2 {
		t.Fatalf("rebuilt parts = %+v (err %v), want 2", parts, err)
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

func TestRebuildReplaysEncryptionConfig(t *testing.T) {
	db := newRebuildDB(t)
	want := Op{
		Type:             OpEncConfig,
		KDFSalt:          []byte("salt"),
		KDFParamsJSON:    `{"memory":65536}`,
		WrappedMasterKey: []byte("wrapped"),
		KeyCheck:         []byte("check"),
		Hint:             "pet name",
		ConfigVersion:    1,
	}
	seedReplay(t, db, testChan, 7, want)

	if err := RebuildProjection(db, testChan); err != nil {
		t.Fatalf("rebuild: %v", err)
	}

	got, err := GetEncryptionConfig(db, testChan)
	if err != nil {
		t.Fatalf("get config: %v", err)
	}
	if !bytes.Equal(got.KDFSalt, want.KDFSalt) ||
		!bytes.Equal(got.WrappedMasterKey, want.WrappedMasterKey) ||
		!bytes.Equal(got.KeyCheck, want.KeyCheck) ||
		got.KDFParamsJSON != want.KDFParamsJSON ||
		got.Hint != want.Hint ||
		got.Version != want.ConfigVersion {
		t.Fatalf("config mismatch: got %+v want %+v", got, want)
	}
}
