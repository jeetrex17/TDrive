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

func TestIncrementalRepairsLocallyAheadReplayBeforeReturning(t *testing.T) {
	db, telegram, engine := newSyncEnv(t)
	mkdir := projection.Op{Type: projection.OpMkdir, Obj: "d:incremental-ordered", Name: "Before"}
	rename := projection.Op{Type: projection.OpRename, Obj: "d:incremental-ordered", Name: "After"}

	// Simulate a writable local commit projected before the contiguous history
	// preceding it. Its first application cannot find the target, so replaying
	// the completed log in message order is required before Incremental succeeds.
	if _, err := projection.ProjectFromOp(db, testChan, 101, rename, 7, projection.Format(rename)); err != nil {
		t.Fatalf("project ahead rename: %v", err)
	}
	telegram.SeedHistory(
		tgclient.HistoryMessage{MsgID: 100, FromID: 7, Text: projection.Format(mkdir)},
		tgclient.HistoryMessage{MsgID: 101, FromID: 7, Text: projection.Format(rename)},
	)

	if err := engine.Incremental(context.Background(), testChan); err != nil {
		t.Fatalf("Incremental: %v", err)
	}
	folder, found, err := projection.FolderByID(db, testChan, "d:incremental-ordered")
	if err != nil || !found || folder.Name != "After" {
		t.Fatalf("ordered folder after one Incremental = %+v found=%v err=%v", folder, found, err)
	}
	channel, err := projection.GetChannel(db, testChan)
	if err != nil {
		t.Fatalf("GetChannel: %v", err)
	}
	if channel.NeedsProjectionRebuild {
		t.Fatal("Incremental returned with projection rebuild still pending")
	}
}

func TestPrepareHardDeleteProjectionRebuildsLocallyAheadStateInMessageOrder(t *testing.T) {
	db, telegram, engine := newSyncEnv(t)
	mkdir := projection.Op{Type: projection.OpMkdir, Obj: "d:ordered", Name: "Before"}
	rename := projection.Op{Type: projection.OpRename, Obj: "d:ordered", Name: "After"}

	// Simulate a local commit projected ahead of the contiguous sync watermark.
	// The rename is initially a no-op because its earlier mkdir is still unseen.
	if _, err := projection.ProjectFromOp(db, testChan, 101, rename, 7, projection.Format(rename)); err != nil {
		t.Fatalf("project ahead rename: %v", err)
	}
	telegram.SeedHistory(
		tgclient.HistoryMessage{MsgID: 100, FromID: 7, Text: projection.Format(mkdir)},
		tgclient.HistoryMessage{MsgID: 101, FromID: 7, Text: projection.Format(rename)},
	)

	if err := engine.PrepareHardDeleteProjection(context.Background(), testChan); err != nil {
		t.Fatalf("PrepareHardDeleteProjection: %v", err)
	}
	folder, found, err := projection.FolderByID(db, testChan, "d:ordered")
	if err != nil || !found || folder.Name != "After" {
		t.Fatalf("ordered folder = %+v found=%v err=%v", folder, found, err)
	}
}

func TestPrepareHardDeleteProjectionRepairsAheadReplayWithoutFullHistoryScan(t *testing.T) {
	db, telegram, _ := newSyncEnv(t)
	recorder := &recordingHistoryPager{Fake: telegram}
	engine := NewEngine(db, recorder, fakePeers{})
	mkdir := projection.Op{Type: projection.OpMkdir, Obj: "d:recent", Name: "Before"}
	rename := projection.Op{Type: projection.OpRename, Obj: "d:recent", Name: "After"}

	if _, err := db.Exec(`UPDATE channels SET last_synced_msg=? WHERE channel_id=?`, 99, testChan); err != nil {
		t.Fatalf("seed watermark: %v", err)
	}
	if _, err := projection.ProjectFromOp(db, testChan, 101, rename, 7, projection.Format(rename)); err != nil {
		t.Fatalf("project ahead rename: %v", err)
	}
	telegram.SeedHistory(
		tgclient.HistoryMessage{MsgID: 100, FromID: 7, Text: projection.Format(mkdir)},
		tgclient.HistoryMessage{MsgID: 101, FromID: 7, Text: projection.Format(rename)},
	)

	if err := engine.PrepareHardDeleteProjection(context.Background(), testChan); err != nil {
		t.Fatalf("PrepareHardDeleteProjection: %v", err)
	}
	for _, minID := range recorder.minIDs {
		if minID == 0 {
			t.Fatalf("hard-delete preparation performed a full history scan: min_ids=%v", recorder.minIDs)
		}
	}
	folder, found, err := projection.FolderByID(db, testChan, "d:recent")
	if err != nil || !found || folder.Name != "After" {
		t.Fatalf("ordered folder = %+v found=%v err=%v", folder, found, err)
	}
}

func TestPrepareHardDeleteProjectionRepairsOrderingWithoutForeignPlan(t *testing.T) {
	db, telegram, engine := newSyncEnv(t)
	root := projection.Op{Type: projection.OpFolderCommit, ProtocolVersion: 1, OpID: "ordered-root", Obj: "d:root", Name: "Root"}
	outside := projection.Op{Type: projection.OpFolderCommit, ProtocolVersion: 1, OpID: "ordered-outside", Obj: "d:outside", Name: "Outside"}
	part := projection.Op{Type: projection.OpFilePart, UploadUUID: "ordered-body", PartIndex: 0, FileSize: 1}
	file := projection.Op{
		Type: projection.OpFileCommit, ProtocolVersion: 1, OpID: "ordered-file",
		Parent: "d:root", Name: "move-me.txt", UploadUUID: "ordered-body", PartCount: 1, FileSize: 1,
	}
	moveOut := projection.Op{
		Type: projection.OpRelocate, ProtocolVersion: 1, OpID: "ordered-move-out",
		Obj: "f:103", Parent: "d:outside", Name: "move-me.txt", ExpectedRevision: 1,
	}
	hardDelete := projection.Op{
		Type: projection.OpHardDeleteTree, ProtocolVersion: 1, OpID: "ordered-hard-delete",
		Obj: "d:root", ExpectedRevision: 1,
	}

	// The locally projected relocate is initially rejected because its target
	// has not been synchronized. The ordered repair must still move the file
	// before applying the hard-delete marker. This installation did not register
	// the marker intent, so it must not retain an actionable cleanup plan.
	if _, err := projection.ProjectFromOp(db, testChan, 104, moveOut, 7, projection.Format(moveOut)); err != nil {
		t.Fatalf("project ahead relocate: %v", err)
	}
	telegram.SeedHistory(
		tgclient.HistoryMessage{MsgID: 100, FromID: 7, Text: projection.Format(root)},
		tgclient.HistoryMessage{MsgID: 101, FromID: 7, Text: projection.Format(outside)},
		tgclient.HistoryMessage{MsgID: 102, FromID: 7, Text: projection.Format(part)},
		tgclient.HistoryMessage{MsgID: 103, FromID: 7, Text: projection.Format(file)},
		tgclient.HistoryMessage{MsgID: 104, FromID: 7, Text: projection.Format(moveOut)},
		tgclient.HistoryMessage{MsgID: 105, FromID: 7, Text: projection.Format(hardDelete)},
	)

	if err := engine.PrepareHardDeleteProjection(context.Background(), testChan); err != nil {
		t.Fatalf("PrepareHardDeleteProjection: %v", err)
	}
	fileRow, found, err := projection.FileByID(db, testChan, 103)
	if err != nil || !found || fileRow.ParentID != "d:outside" {
		t.Fatalf("moved file = %+v found=%v err=%v", fileRow, found, err)
	}
	channel, err := projection.GetChannel(db, testChan)
	if err != nil {
		t.Fatalf("GetChannel: %v", err)
	}
	if channel.NeedsProjectionRebuild {
		t.Fatal("ordered repair left projection rebuild pending")
	}
	ids, total, done, err := projection.HardDeletePlanPage(
		context.Background(), db, testChan, "ordered-hard-delete", 0, 10,
	)
	if !errors.Is(err, projection.ErrHardDeletePlanNotFound) || len(ids) != 0 || total != 0 || done {
		t.Fatalf("foreign hard-delete plan=(%v,%d,%t,%v), want no local plan", ids, total, done, err)
	}
}

func TestPrepareHardDeleteProjectionSkipsFullRebuildForContiguousReplay(t *testing.T) {
	db, telegram, engine := newSyncEnv(t)
	mkdir := projection.Op{Type: projection.OpMkdir, Obj: "d:ordered", Name: "Before"}
	rename := projection.Op{Type: projection.OpRename, Obj: "d:ordered", Name: "After"}
	sendOp(t, telegram, mkdir)
	if err := engine.Incremental(context.Background(), testChan); err != nil {
		t.Fatalf("initial incremental: %v", err)
	}

	// This row is intentionally outside replay_log. It makes an unnecessary
	// full rebuild observable without coupling production code to a test hook.
	if _, err := db.Exec(`
		INSERT INTO folders (channel_id, id, name, parent_id, tombstoned, revision)
		VALUES (?, 'd:sentinel', 'Sentinel', '', 0, 1)
	`, testChan); err != nil {
		t.Fatalf("insert sentinel projection row: %v", err)
	}

	sendOp(t, telegram, rename)
	if err := engine.PrepareHardDeleteProjection(context.Background(), testChan); err != nil {
		t.Fatalf("PrepareHardDeleteProjection: %v", err)
	}
	if !projection.FolderExists(db, testChan, "d:sentinel") {
		t.Fatal("contiguous hard-delete preparation performed an unnecessary full rebuild")
	}
	folder, found, err := projection.FolderByID(db, testChan, "d:ordered")
	if err != nil || !found || folder.Name != "After" {
		t.Fatalf("ordered folder = %+v found=%v err=%v", folder, found, err)
	}
}

func TestIncrementalRetriesReadFloodWait(t *testing.T) {
	db, tg, eng := newSyncEnv(t)
	idA := sendOp(t, tg, projection.Op{Type: projection.OpMkdir, Obj: "d:a", Parent: projection.RootParent, Name: "A"})

	var waits []time.Duration
	eng.OnProgress = func(p Progress) {
		if p.Phase == ProgressWaiting {
			waits = append(waits, p.Wait)
		}
	}
	tg.InjectReadFloodWaits(2) // first two history reads flood-wait, then succeed

	if err := eng.Incremental(context.Background(), testChan); err != nil {
		t.Fatalf("incremental: %v", err)
	}
	if !projection.FolderExists(db, testChan, "d:a") {
		t.Fatal("d:a missing after flood-wait retry")
	}
	if len(waits) != 2 {
		t.Fatalf("waiting progress fired %d times, want 2", len(waits))
	}
	for _, wait := range waits {
		if wait <= 0 {
			t.Fatalf("waiting progress reported wait %v, want a positive duration", wait)
		}
	}
	var wm int64
	if err := db.QueryRow(`SELECT last_synced_msg FROM channels WHERE channel_id = ?`, testChan).Scan(&wm); err != nil {
		t.Fatalf("watermark: %v", err)
	}
	if wm != idA {
		t.Fatalf("watermark = %d, want %d", wm, idA)
	}
}

type recordingHistoryPager struct {
	*tgclient.Fake
	minIDs []int64
}

func (pager *recordingHistoryPager) GetHistory(
	ctx context.Context,
	peer tgclient.InputPeer,
	minID int64,
	offsetID int64,
	limit int,
) ([]tgclient.HistoryMessage, error) {
	pager.minIDs = append(pager.minIDs, minID)
	return pager.Fake.GetHistory(ctx, peer, minID, offsetID, limit)
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

func TestIncrementalAutoAdoptsCaptionlessMediaInPersonalDrive(t *testing.T) {
	db, tg, eng := newSyncEnv(t)
	tg.SeedHistory(tgclient.HistoryMessage{
		MsgID:        42,
		Date:         1234,
		FromID:       9,
		Text:         "forwarded from another channel",
		HasMedia:     true,
		MediaSize:    9876,
		DocumentName: "forwarded.mkv",
	})

	if err := eng.Incremental(context.Background(), testChan); err != nil {
		t.Fatalf("incremental: %v", err)
	}

	var name string
	var size, uploadTime, uploader int64
	if err := db.QueryRow(`
		SELECT name, size, upload_time, uploader_user_id FROM files
		WHERE channel_id = ? AND msg_id = ?
	`, testChan, 42).Scan(&name, &size, &uploadTime, &uploader); err != nil {
		t.Fatalf("file row: %v", err)
	}
	if name != "forwarded.mkv" {
		t.Fatalf("name = %q, want forwarded.mkv", name)
	}
	if size != 9876 {
		t.Fatalf("size = %d, want 9876", size)
	}
	if uploadTime != 1234 {
		t.Fatalf("upload_time = %d, want 1234", uploadTime)
	}
	if uploader != 9 {
		t.Fatalf("uploader = %d, want 9", uploader)
	}
}

func TestIncrementalRecoversRecentlySkippedCaptionlessMediaBelowWatermark(t *testing.T) {
	db, tg, eng := newSyncEnv(t)
	tg.SeedHistory(tgclient.HistoryMessage{
		MsgID:        42,
		Date:         1234,
		FromID:       9,
		Text:         "forwarded before the auto-adopt fix",
		HasMedia:     true,
		MediaSize:    9876,
		DocumentName: "already-skipped.mkv",
	})
	if _, err := db.Exec(`UPDATE channels SET last_synced_msg = ? WHERE channel_id = ?`, int64(100), testChan); err != nil {
		t.Fatalf("seed watermark: %v", err)
	}

	if err := eng.Incremental(context.Background(), testChan); err != nil {
		t.Fatalf("incremental: %v", err)
	}

	var name string
	if err := db.QueryRow(`
		SELECT name FROM files
		WHERE channel_id = ? AND msg_id = ?
	`, testChan, 42).Scan(&name); err != nil {
		t.Fatalf("file row: %v", err)
	}
	if name != "already-skipped.mkv" {
		t.Fatalf("name = %q, want already-skipped.mkv", name)
	}
	var wm int64
	if err := db.QueryRow(`SELECT last_synced_msg FROM channels WHERE channel_id = ?`, testChan).Scan(&wm); err != nil {
		t.Fatalf("watermark: %v", err)
	}
	if wm != 100 {
		t.Fatalf("watermark = %d, want 100", wm)
	}
}

func TestIncrementalDoesNotAutoAdoptCaptionlessMediaInSharedDrive(t *testing.T) {
	db, tg, eng := newSyncEnv(t)
	if _, err := db.Exec(`UPDATE channels SET kind = ? WHERE channel_id = ?`, projection.KindShared, testChan); err != nil {
		t.Fatalf("mark shared: %v", err)
	}
	tg.SeedHistory(tgclient.HistoryMessage{
		MsgID:        42,
		Date:         1234,
		FromID:       9,
		Text:         "regular shared-drive attachment",
		HasMedia:     true,
		MediaSize:    9876,
		DocumentName: "chat-attachment.mkv",
	})

	if err := eng.Incremental(context.Background(), testChan); err != nil {
		t.Fatalf("incremental: %v", err)
	}

	var rows int
	if err := db.QueryRow(`SELECT COUNT(*) FROM files WHERE channel_id = ?`, testChan).Scan(&rows); err != nil {
		t.Fatalf("file count: %v", err)
	}
	if rows != 0 {
		t.Fatalf("files = %d, want 0", rows)
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

func TestParseHistoryPageCanAdoptCaptionlessMedia(t *testing.T) {
	parsed := ParseHistoryPageWithOptions([]tgclient.HistoryMessage{{
		MsgID:        42,
		Date:         1234,
		FromID:       9,
		Text:         "not a TDX header",
		HasMedia:     true,
		MediaSize:    9876,
		DocumentName: "forwarded.mkv",
	}}, ParseOptions{AdoptCaptionlessMedia: true})
	if len(parsed) != 1 {
		t.Fatalf("parsed = %d, want 1", len(parsed))
	}
	got := parsed[0]
	if got.Op.Type != projection.OpFileUpload {
		t.Fatalf("op type = %q, want file upload", got.Op.Type)
	}
	if got.Op.Parent != projection.RootParent || got.Op.Name != "forwarded.mkv" {
		t.Fatalf("op parent/name = %q/%q, want root/forwarded.mkv", got.Op.Parent, got.Op.Name)
	}
	if got.Op.FileSize != 9876 || got.Op.FileUploadTime != 1234 {
		t.Fatalf("op size/time = %d/%d, want 9876/1234", got.Op.FileSize, got.Op.FileUploadTime)
	}
	if got.RawHeader == "" {
		t.Fatal("raw header must be deterministic for replay-log hashing")
	}
}

func TestParseHistoryPageDoesNotAdoptMalformedTDXMedia(t *testing.T) {
	parsed := ParseHistoryPageWithOptions([]tgclient.HistoryMessage{{
		MsgID:        42,
		Date:         1234,
		FromID:       9,
		Text:         "TDX1|t=f|p=",
		HasMedia:     true,
		MediaSize:    9876,
		DocumentName: "forwarded.mkv",
	}}, ParseOptions{AdoptCaptionlessMedia: true})
	if len(parsed) != 0 {
		t.Fatalf("parsed = %d, want 0 for malformed TDX header", len(parsed))
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
