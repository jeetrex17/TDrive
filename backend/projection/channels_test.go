package projection

import (
	"database/sql"
	"errors"
	"testing"

	_ "modernc.org/sqlite"
)

func newChannelsDB(t *testing.T) *sql.DB {
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

func TestInsertChannelInsertsAndUpdates(t *testing.T) {
	db := newChannelsDB(t)
	c := Channel{
		ChannelID:            5555,
		AccessHash:           777,
		Title:                "Friends",
		Kind:                 KindShared,
		InviteLink:           "t.me/+abc",
		JoinedAt:             100,
		PersonalBackfillDone: true,
	}
	if err := InsertChannel(db, c); err != nil {
		t.Fatalf("first insert: %v", err)
	}

	got, err := GetChannel(db, 5555)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Title != "Friends" || got.AccessHash != 777 || !got.PersonalBackfillDone {
		t.Fatalf("got %+v", got)
	}

	c.AccessHash = 888
	c.Title = "Friends Group"
	if err := InsertChannel(db, c); err != nil {
		t.Fatalf("second insert: %v", err)
	}
	got, err = GetChannel(db, 5555)
	if err != nil {
		t.Fatalf("get2: %v", err)
	}
	if got.Title != "Friends Group" || got.AccessHash != 888 {
		t.Fatalf("upsert did not update: %+v", got)
	}
}

func TestInsertChannelRejectsBadInput(t *testing.T) {
	db := newChannelsDB(t)
	if err := InsertChannel(db, Channel{}); err == nil {
		t.Fatal("expected error for zero id")
	}
	if err := InsertChannel(db, Channel{ChannelID: 1, Kind: "wat", Title: "x"}); err == nil {
		t.Fatal("expected error for bad kind")
	}
}

func TestListChannelsOrdersPersonalFirst(t *testing.T) {
	db := newChannelsDB(t)
	if err := InsertChannel(db, Channel{ChannelID: 7777, AccessHash: 1, Title: "Goa", Kind: KindShared, JoinedAt: 200, PersonalBackfillDone: true}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := InsertChannel(db, Channel{ChannelID: 8888, AccessHash: 1, Title: "Family", Kind: KindShared, JoinedAt: 100, PersonalBackfillDone: true}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	got, err := ListChannels(db)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("len = %d, want 3", len(got))
	}
	if got[0].Kind != KindPersonal {
		t.Fatalf("first is %q, want personal", got[0].Kind)
	}
	// Shared drives sorted by joined_at ascending.
	if got[1].ChannelID != 8888 || got[2].ChannelID != 7777 {
		t.Fatalf("shared order wrong: %d, %d", got[1].ChannelID, got[2].ChannelID)
	}
}

func TestDeleteChannelCascadesAllScopedTables(t *testing.T) {
	db := newChannelsDB(t)
	const sharedID int64 = 9999
	if err := InsertChannel(db, Channel{ChannelID: sharedID, AccessHash: 1, Title: "Goa", Kind: KindShared, PersonalBackfillDone: true}); err != nil {
		t.Fatalf("seed channel: %v", err)
	}

	mkdir := Op{Type: OpMkdir, Obj: "d:in-shared", Parent: RootParent, Name: "Spot"}
	if _, err := ProjectFromOp(db, sharedID, 1, mkdir, 0, Format(mkdir)); err != nil {
		t.Fatalf("seed mkdir: %v", err)
	}
	upload := Op{Type: OpFileUpload, Parent: RootParent, Name: "x.png", FileSize: 10, FileUploadTime: 1}
	if _, err := ProjectFromOp(db, sharedID, 2, upload, 0, Format(upload)); err != nil {
		t.Fatalf("seed file: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO replay_log_tamper (channel_id, msg_id, old_hash, new_hash, detected_at)
		VALUES (?, ?, '', '', 0)
	`, sharedID, 999); err != nil {
		t.Fatalf("seed tamper: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO backfill_progress (channel_id, cursor_obj_id, cursor_kind, started_at, updated_at)
		VALUES (?, 'x', 'folder', 0, 0)
	`, sharedID); err != nil {
		t.Fatalf("seed backfill: %v", err)
	}

	if err := DeleteChannel(db, sharedID); err != nil {
		t.Fatalf("delete: %v", err)
	}

	for table, query := range map[string]string{
		"channels":          `SELECT 1 FROM channels WHERE channel_id = ?`,
		"replay_log":        `SELECT 1 FROM replay_log WHERE channel_id = ?`,
		"replay_log_tamper": `SELECT 1 FROM replay_log_tamper WHERE channel_id = ?`,
		"folders":           `SELECT 1 FROM folders WHERE channel_id = ?`,
		"files":             `SELECT 1 FROM files WHERE channel_id = ?`,
		"backfill_progress": `SELECT 1 FROM backfill_progress WHERE channel_id = ?`,
	} {
		var tmp int
		err := db.QueryRow(query, sharedID).Scan(&tmp)
		if !errors.Is(err, sql.ErrNoRows) {
			t.Fatalf("%s row remained after delete (err=%v)", table, err)
		}
	}

	// Personal channel and its data must still exist.
	if _, err := GetChannel(db, testChan); err != nil {
		t.Fatalf("personal channel disappeared: %v", err)
	}
}

func TestUpdateInviteLink(t *testing.T) {
	db := newChannelsDB(t)
	if err := InsertChannel(db, Channel{ChannelID: 4444, AccessHash: 1, Title: "X", Kind: KindShared, PersonalBackfillDone: true}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := UpdateInviteLink(db, 4444, "https://t.me/+xyz"); err != nil {
		t.Fatalf("update: %v", err)
	}
	got, err := GetChannel(db, 4444)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.InviteLink != "https://t.me/+xyz" {
		t.Fatalf("invite_link = %q", got.InviteLink)
	}
}
