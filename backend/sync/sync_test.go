package sync

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"testing"
	"time"

	"TDrive/backend/projection"
	"TDrive/backend/tgclient"

	_ "modernc.org/sqlite"
)

const testChan int64 = 555

type fakePeers struct{}

func (fakePeers) ResolvePeer(ctx context.Context, channelID int64) (tgclient.InputPeer, error) {
	return tgclient.InputPeer{ChannelID: channelID, AccessHash: 1}, nil
}

func newSyncEnv(t *testing.T) (*sql.DB, *tgclient.Fake, *Engine) {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	if err := projection.MigratePersonalChannel(db, testChan); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	tg := tgclient.NewFake(7)
	eng := NewEngine(db, tg, fakePeers{})
	return db, tg, eng
}

func sendOp(t *testing.T, tg *tgclient.Fake, op projection.Op) int64 {
	t.Helper()
	header := projection.Format(op)
	id, err := tg.SendControl(context.Background(), tgclient.InputPeer{ChannelID: testChan, AccessHash: 1}, header, true)
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	return id
}

func TestIncrementalAppliesOpsAscending(t *testing.T) {
	db, tg, eng := newSyncEnv(t)

	// Send out of natural order: msg 2 references parent created in msg 1,
	// but we'll manually reorder the history to simulate a network returning
	// them in any order. Sync must still apply ascending.
	idA := sendOp(t, tg, projection.Op{Type: projection.OpMkdir, Obj: "d:a", Parent: projection.RootParent, Name: "A"})
	idB := sendOp(t, tg, projection.Op{Type: projection.OpMkdir, Obj: "d:b", Parent: "d:a", Name: "B"})
	if idB <= idA {
		t.Fatalf("expected idB > idA, got %d %d", idA, idB)
	}

	if err := eng.Incremental(context.Background(), testChan); err != nil {
		t.Fatalf("incremental: %v", err)
	}
	if !projection.FolderExists(db, testChan, "d:a") {
		t.Fatal("d:a missing")
	}
	if !projection.FolderExists(db, testChan, "d:b") {
		t.Fatal("d:b missing")
	}
	parent, err := projection.FolderParent(db, testChan, "d:b")
	if err != nil || parent != "d:a" {
		t.Fatalf("d:b parent = %q (err %v), want d:a", parent, err)
	}

	// Watermark advanced.
	var wm int64
	if err := db.QueryRow(`SELECT last_synced_msg FROM channels WHERE channel_id = ?`, testChan).Scan(&wm); err != nil {
		t.Fatalf("watermark: %v", err)
	}
	if wm != idB {
		t.Fatalf("watermark = %d, want %d", wm, idB)
	}
}

func TestIncrementalRetriesReadFloodWait(t *testing.T) {
	db, tg, eng := newSyncEnv(t)
	idA := sendOp(t, tg, projection.Op{Type: projection.OpMkdir, Obj: "d:a", Parent: projection.RootParent, Name: "A"})

	var hooks int
	eng.OnFloodWait = func(channelID int64, wait time.Duration) { hooks++ }
	tg.InjectReadFloodWaits(2) // first two history reads flood-wait, then succeed

	if err := eng.Incremental(context.Background(), testChan); err != nil {
		t.Fatalf("incremental: %v", err)
	}
	if !projection.FolderExists(db, testChan, "d:a") {
		t.Fatal("d:a missing after flood-wait retry")
	}
	if hooks != 2 {
		t.Fatalf("OnFloodWait fired %d times, want 2", hooks)
	}
	var wm int64
	if err := db.QueryRow(`SELECT last_synced_msg FROM channels WHERE channel_id = ?`, testChan).Scan(&wm); err != nil {
		t.Fatalf("watermark: %v", err)
	}
	if wm != idA {
		t.Fatalf("watermark = %d, want %d", wm, idA)
	}
}

func TestIncrementalFailsAfterMaxFloodRetries(t *testing.T) {
	_, tg, eng := newSyncEnv(t)
	sendOp(t, tg, projection.Op{Type: projection.OpMkdir, Obj: "d:a", Parent: projection.RootParent, Name: "A"})
	tg.InjectReadFloodWaits(maxFloodWaitRetries + 2) // never recovers within the retry budget

	err := eng.Incremental(context.Background(), testChan)
	if err == nil {
		t.Fatal("expected error after exhausting flood-wait retries")
	}
	if !errors.Is(err, tgclient.ErrFloodWait) {
		t.Fatalf("err = %v, want flood-wait", err)
	}
}

func TestIncrementalIsIdempotent(t *testing.T) {
	db, tg, eng := newSyncEnv(t)
	sendOp(t, tg, projection.Op{Type: projection.OpMkdir, Obj: "d:a", Parent: projection.RootParent, Name: "A"})

	if err := eng.Incremental(context.Background(), testChan); err != nil {
		t.Fatalf("first: %v", err)
	}
	// Second call with no new messages must be a no-op and not re-insert.
	if err := eng.Incremental(context.Background(), testChan); err != nil {
		t.Fatalf("second: %v", err)
	}
	var rows int
	if err := db.QueryRow(`SELECT COUNT(*) FROM replay_log WHERE channel_id = ?`, testChan).Scan(&rows); err != nil {
		t.Fatalf("count: %v", err)
	}
	if rows != 1 {
		t.Fatalf("replay_log rows = %d, want 1", rows)
	}
}

func TestIncrementalBackfillsMissingFileSizeFromTelegramMedia(t *testing.T) {
	db, tg, eng := newSyncEnv(t)
	tg.SeedHistory(tgclient.HistoryMessage{
		MsgID:     42,
		Date:      1234,
		FromID:    9,
		Text:      "TDX1|t=f|p=|n=shared.bin",
		HasMedia:  true,
		MediaSize: 9876,
	})

	if err := eng.Incremental(context.Background(), testChan); err != nil {
		t.Fatalf("incremental: %v", err)
	}

	var size, uploadTime int64
	if err := db.QueryRow(`
		SELECT size, upload_time FROM files
		WHERE channel_id = ? AND msg_id = ?
	`, testChan, 42).Scan(&size, &uploadTime); err != nil {
		t.Fatalf("file row: %v", err)
	}
	if size != 9876 {
		t.Fatalf("size = %d, want 9876", size)
	}
	if uploadTime != 1234 {
		t.Fatalf("upload_time = %d, want 1234", uploadTime)
	}
}

func TestParseHistoryPagePreservesExplicitFileSizeAndTime(t *testing.T) {
	parsed := ParseHistoryPage([]tgclient.HistoryMessage{{
		MsgID:     42,
		Date:      9999,
		FromID:    9,
		Text:      "TDX1|t=f|p=|n=shared.bin|sz=111|ts=222",
		HasMedia:  true,
		MediaSize: 9876,
	}})
	if len(parsed) != 1 {
		t.Fatalf("parsed = %d, want 1", len(parsed))
	}
	if parsed[0].Op.FileSize != 111 {
		t.Fatalf("size = %d, want explicit 111", parsed[0].Op.FileSize)
	}
	if parsed[0].Op.FileUploadTime != 222 {
		t.Fatalf("upload_time = %d, want explicit 222", parsed[0].Op.FileUploadTime)
	}
}

func TestIncrementalPaginatesAllNewMessagesNewestFirst(t *testing.T) {
	db, tg, eng := newSyncEnv(t)

	var lastID int64
	for i := 0; i < defaultPageSize*2+17; i++ {
		lastID = sendOp(t, tg, projection.Op{
			Type:   projection.OpMkdir,
			Obj:    fmt.Sprintf("d:%03d", i),
			Parent: projection.RootParent,
			Name:   fmt.Sprintf("Folder %03d", i),
		})
	}

	if err := eng.Incremental(context.Background(), testChan); err != nil {
		t.Fatalf("incremental: %v", err)
	}

	var rows int
	if err := db.QueryRow(`SELECT COUNT(*) FROM replay_log WHERE channel_id = ?`, testChan).Scan(&rows); err != nil {
		t.Fatalf("count replay_log: %v", err)
	}
	if rows != defaultPageSize*2+17 {
		t.Fatalf("replay_log rows = %d, want %d", rows, defaultPageSize*2+17)
	}
	if !projection.FolderExists(db, testChan, "d:000") || !projection.FolderExists(db, testChan, "d:216") {
		t.Fatal("sync skipped at least one page boundary folder")
	}
	var wm int64
	if err := db.QueryRow(`SELECT last_synced_msg FROM channels WHERE channel_id = ?`, testChan).Scan(&wm); err != nil {
		t.Fatalf("watermark: %v", err)
	}
	if wm != lastID {
		t.Fatalf("watermark = %d, want %d", wm, lastID)
	}
}

func TestIncrementalSkipsAlreadyProjected(t *testing.T) {
	db, tg, eng := newSyncEnv(t)

	op := projection.Op{Type: projection.OpMkdir, Obj: "d:a", Parent: projection.RootParent, Name: "A"}
	header := projection.Format(op)
	msgID, err := tg.SendControl(context.Background(), tgclient.InputPeer{ChannelID: testChan, AccessHash: 1}, header, true)
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	// Pre-project the op locally as if we sent it ourselves.
	if _, err := projection.ProjectFromOp(db, testChan, msgID, op, 0, header); err != nil {
		t.Fatalf("preproject: %v", err)
	}

	if err := eng.Incremental(context.Background(), testChan); err != nil {
		t.Fatalf("sync: %v", err)
	}
	// Still exactly one replay_log row, no tamper, projection unchanged.
	var rows, tamperRows int
	_ = db.QueryRow(`SELECT COUNT(*) FROM replay_log WHERE channel_id = ?`, testChan).Scan(&rows)
	_ = db.QueryRow(`SELECT COUNT(*) FROM replay_log_tamper WHERE channel_id = ?`, testChan).Scan(&tamperRows)
	if rows != 1 || tamperRows != 0 {
		t.Fatalf("rows=%d tamper=%d, want 1/0", rows, tamperRows)
	}
}

func TestIncrementalDetectsTamperFromTelegram(t *testing.T) {
	db, tg, eng := newSyncEnv(t)

	sendOp(t, tg, projection.Op{Type: projection.OpMkdir, Obj: "d:a", Parent: projection.RootParent, Name: "A"})
	if err := eng.Incremental(context.Background(), testChan); err != nil {
		t.Fatalf("first: %v", err)
	}

	if _, err := tg.EditLastControlText(projection.Format(projection.Op{
		Type: projection.OpMkdir, Obj: "d:a", Parent: projection.RootParent, Name: "Hijack",
	})); err != nil {
		t.Fatalf("edit: %v", err)
	}

	// Reset watermark so sync re-encounters the edited message.
	if _, err := db.Exec(`UPDATE channels SET last_synced_msg = 0 WHERE channel_id = ?`, testChan); err != nil {
		t.Fatalf("reset watermark: %v", err)
	}

	if err := eng.Incremental(context.Background(), testChan); err != nil {
		t.Fatalf("second: %v", err)
	}

	var tamper int
	if err := db.QueryRow(`SELECT COUNT(*) FROM replay_log_tamper WHERE channel_id = ?`, testChan).Scan(&tamper); err != nil {
		t.Fatalf("tamper: %v", err)
	}
	if tamper != 1 {
		t.Fatalf("tamper count = %d, want 1", tamper)
	}
	var name string
	if err := db.QueryRow(`SELECT name FROM folders WHERE id = ?`, "d:a").Scan(&name); err != nil {
		t.Fatalf("name: %v", err)
	}
	if name != "A" {
		t.Fatalf("name = %q, want A (original op stays canonical)", name)
	}
}

func TestIncrementalIgnoresNonTDXMessages(t *testing.T) {
	db, tg, eng := newSyncEnv(t)
	if _, err := tg.SendControl(context.Background(), tgclient.InputPeer{ChannelID: testChan, AccessHash: 1}, "TDrive File: legacy.txt", false); err != nil {
		t.Fatalf("legacy: %v", err)
	}
	sendOp(t, tg, projection.Op{Type: projection.OpMkdir, Obj: "d:a", Parent: projection.RootParent, Name: "A"})

	if err := eng.Incremental(context.Background(), testChan); err != nil {
		t.Fatalf("sync: %v", err)
	}
	var rows int
	_ = db.QueryRow(`SELECT COUNT(*) FROM replay_log WHERE channel_id = ?`, testChan).Scan(&rows)
	if rows != 1 {
		t.Fatalf("replay_log rows = %d, want 1 (legacy must be skipped)", rows)
	}
	if !projection.FolderExists(db, testChan, "d:a") {
		t.Fatal("d:a missing")
	}
}

func TestInitialSyncRequiresEmptyChannel(t *testing.T) {
	db, tg, eng := newSyncEnv(t)
	sendOp(t, tg, projection.Op{Type: projection.OpMkdir, Obj: "d:a", Parent: projection.RootParent, Name: "A"})
	if err := eng.Incremental(context.Background(), testChan); err != nil {
		t.Fatalf("seed: %v", err)
	}
	_ = db
	err := eng.InitialSyncEmptyChannel(context.Background(), testChan)
	if err != projection.ErrChannelNotEmpty {
		t.Fatalf("err = %v, want ErrChannelNotEmpty", err)
	}
}

func TestInitialSyncProjectsFullHistoryAscending(t *testing.T) {
	db, tg, eng := newSyncEnv(t)

	sendOp(t, tg, projection.Op{Type: projection.OpMkdir, Obj: "d:a", Parent: projection.RootParent, Name: "A"})
	sendOp(t, tg, projection.Op{Type: projection.OpMkdir, Obj: "d:b", Parent: "d:a", Name: "B"})
	idLast := sendOp(t, tg, projection.Op{Type: projection.OpRename, Obj: "d:a", Name: "A2"})

	if err := eng.InitialSyncEmptyChannel(context.Background(), testChan); err != nil {
		t.Fatalf("initial: %v", err)
	}

	var name string
	if err := db.QueryRow(`SELECT name FROM folders WHERE id = ?`, "d:a").Scan(&name); err != nil {
		t.Fatalf("name: %v", err)
	}
	if name != "A2" {
		t.Fatalf("name = %q want A2 (rename applied last)", name)
	}

	var wm int64
	_ = db.QueryRow(`SELECT last_synced_msg FROM channels WHERE channel_id = ?`, testChan).Scan(&wm)
	if wm != idLast {
		t.Fatalf("watermark = %d want %d", wm, idLast)
	}

	var initSyncDone int
	_ = db.QueryRow(`SELECT initial_sync_done FROM channels WHERE channel_id = ?`, testChan).Scan(&initSyncDone)
	if initSyncDone != 1 {
		t.Fatalf("initial_sync_done = %d, want 1", initSyncDone)
	}
}
