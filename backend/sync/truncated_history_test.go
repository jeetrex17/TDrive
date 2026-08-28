package sync

import (
	"context"
	"database/sql"
	"testing"

	"TDrive/backend/projection"
	"TDrive/backend/tgclient"
)

// shortPagePager serves real history but hands back fewer messages per call
// than asked for, which is what Telegram does whenever the window it scanned is
// thinned by deletions or service events. Only an empty page means the channel
// is exhausted, so a caller that stops on a short one silently loses the rest.
type shortPagePager struct {
	*tgclient.Fake
	live    []tgclient.HistoryMessage // ascending by MsgID
	pageCap int
}

func (pager *shortPagePager) GetHistory(
	_ context.Context,
	_ tgclient.InputPeer,
	minID, offsetID int64,
	limit int,
) ([]tgclient.HistoryMessage, error) {
	if pager.pageCap < limit {
		limit = pager.pageCap
	}
	page := make([]tgclient.HistoryMessage, 0, limit)
	for index := len(pager.live) - 1; index >= 0 && len(page) < limit; index-- {
		msg := pager.live[index]
		if msg.MsgID <= minID || (offsetID != 0 && msg.MsgID >= offsetID) {
			continue
		}
		page = append(page, msg)
	}
	return page, nil
}

// TestEnsureAuthoritativeScansPastPagesThinnedByDeletions reproduces the drive
// reported in issue #88. A stretch of deleted messages sits between the newest
// history and the mkdir that creates the root folder. The scan used to stop at
// the short page, mark the channel authoritative at the newest id it had seen,
// and strand every folder below the ancestor it never read.
func TestEnsureAuthoritativeScansPastPagesThinnedByDeletions(t *testing.T) {
	db, _, _ := newSyncEnv(t)

	root := projection.Op{Type: projection.OpMkdir, Obj: "d:root", Parent: projection.RootParent, Name: "New Volume"}
	child := projection.Op{Type: projection.OpMkdir, Obj: "d:child", Parent: "d:root", Name: "Revit"}

	// The ancestor mkdir is the oldest message in the channel; the child that
	// depends on it sits near the newest. Anything that stops paging before
	// reaching msg 10 keeps the child and strands it under a parent that has
	// no record, which is exactly what the reported drive looked like.
	live := []tgclient.HistoryMessage{{MsgID: 10, Text: projection.Format(root)}}
	for id := int64(201); id <= 300; id++ {
		msg := tgclient.HistoryMessage{MsgID: id}
		if id == 260 {
			msg.Text = projection.Format(child)
		}
		live = append(live, msg)
	}

	engine := NewEngine(db, &shortPagePager{Fake: tgclient.NewFake(7), live: live, pageCap: 60}, fakePeers{})
	if err := engine.EnsureAuthoritative(context.Background(), testChan); err != nil {
		t.Fatalf("EnsureAuthoritative() error = %v", err)
	}

	for _, folder := range []struct{ id, name string }{{"d:root", "New Volume"}, {"d:child", "Revit"}} {
		var name string
		err := db.QueryRow(
			`SELECT name FROM folders WHERE channel_id = ? AND id = ? AND tombstoned = 0`,
			testChan, folder.id,
		).Scan(&name)
		if err != nil {
			t.Fatalf("folder %s missing after full scan: %v", folder.id, err)
		}
		if name != folder.name {
			t.Fatalf("folder %s name = %q, want %q", folder.id, name, folder.name)
		}
	}

	channel, err := projection.GetChannel(db, testChan)
	if err != nil {
		t.Fatalf("GetChannel() error = %v", err)
	}
	if !channel.InitialSyncDone || channel.LastSyncedMsg != 300 {
		t.Fatalf("channel state = %#v, want authoritative through 300", channel)
	}
}

// TestFullScanRepairsOpsBankedAgainstUnreadObjects is the reported drive's
// exact shape. A rename landed while the folder it targets had not been read,
// so applyRename updated no rows, returned nil, and the op was banked in
// replay_log as applied. Re-reading the mkdir alone therefore restores the
// folder under its original name; only replaying the completed log applies the
// rename that followed.
func TestFullScanRepairsOpsBankedAgainstUnreadObjects(t *testing.T) {
	db, _, _ := newSyncEnv(t)

	mkdir := projection.Op{Type: projection.OpMkdir, Obj: "d:autodesk", Parent: projection.RootParent, Name: "Software"}
	rename := projection.Op{Type: projection.OpRename, Obj: "d:autodesk", Name: "Autodesk"}

	// The truncated scan read only the rename, which silently did nothing.
	if _, err := projection.ProjectFromOp(db, testChan, 2411, rename, 7, projection.Format(rename)); err != nil {
		t.Fatalf("seed banked rename: %v", err)
	}
	var folders int
	if err := db.QueryRow(`SELECT count(*) FROM folders WHERE channel_id = ?`, testChan).Scan(&folders); err != nil {
		t.Fatalf("count folders: %v", err)
	}
	if folders != 0 {
		t.Fatalf("seeded folders = %d, want the rename to have been a silent no-op", folders)
	}
	if _, err := db.Exec(
		`UPDATE channels SET last_synced_msg = 0, initial_sync_done = 0, needs_projection_rebuild = 1
		 WHERE channel_id = ?`, testChan); err != nil {
		t.Fatalf("flag rebuild: %v", err)
	}

	live := []tgclient.HistoryMessage{
		{MsgID: 10, Text: projection.Format(mkdir)},
		{MsgID: 2411, Text: projection.Format(rename)},
	}
	engine := NewEngine(db, &shortPagePager{Fake: tgclient.NewFake(7), live: live, pageCap: 60}, fakePeers{})
	if err := engine.EnsureAuthoritative(context.Background(), testChan); err != nil {
		t.Fatalf("EnsureAuthoritative() error = %v", err)
	}

	var name string
	if err := db.QueryRow(
		`SELECT name FROM folders WHERE channel_id = ? AND id = 'd:autodesk' AND tombstoned = 0`,
		testChan).Scan(&name); err != nil {
		t.Fatalf("folder missing after repair: %v", err)
	}
	if name != "Autodesk" {
		t.Fatalf("folder name = %q, want the banked rename replayed as %q", name, "Autodesk")
	}

	channel, err := projection.GetChannel(db, testChan)
	if err != nil {
		t.Fatalf("GetChannel() error = %v", err)
	}
	if channel.NeedsProjectionRebuild {
		t.Fatal("rebuild flag survived the repair, so every later sync would rescan")
	}
}

// TestRescanTruncatedChannelsClearsWatermarkForOrphanedDrives covers the repair
// path for drives already left half-scanned by the old pagination, which cannot
// heal on their own: the channel reads as fully synced, so every later pass
// starts above the history it skipped.
func TestRescanTruncatedChannelsClearsWatermarkForOrphanedDrives(t *testing.T) {
	orphaned := seedScannedChannel(t, 4384929171, "d:never-read", false)
	if orphaned.LastSyncedMsg != 0 || orphaned.InitialSyncDone || !orphaned.NeedsProjectionRebuild {
		t.Fatalf("orphaned drive = %#v, want it flagged for a full rescan", orphaned)
	}

	// A writable-op drive rejects rather than orphaning, so it leaves no dangling
	// row and is recognised only by the rejected op it recorded.
	rejected := seedScannedChannel(t, 4384929173, "", true)
	if rejected.LastSyncedMsg != 0 || rejected.InitialSyncDone || !rejected.NeedsProjectionRebuild {
		t.Fatalf("writable-op drive = %#v, want it flagged for a full rescan", rejected)
	}

	// A tombstoned parent is a real delete, not a truncated scan. Those drives
	// must keep their watermark instead of re-reading the whole channel.
	deleted := seedScannedChannel(t, 4384929172, "d:tombstoned", false)
	if deleted.LastSyncedMsg != 2458 || !deleted.InitialSyncDone || deleted.NeedsProjectionRebuild {
		t.Fatalf("deleted-folder drive = %#v, want it left alone", deleted)
	}
}

// seedScannedChannel builds a pre-migration drive whose live child hangs off
// parentID, optionally with a rejected op instead, then migrates it and reports
// the resulting channel row.
func seedScannedChannel(t *testing.T, channelID int64, parentID string, rejectedOp bool) projection.Channel {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	// Build the current schema, seed the half-scanned state into it, then wind
	// the recorded version back so the repair migration runs over it.
	if err := projection.MigratePersonalChannel(db, channelID); err != nil {
		t.Fatalf("build schema: %v", err)
	}
	if _, err := db.Exec(
		`UPDATE channels SET last_synced_msg = 2458, initial_sync_done = 1 WHERE channel_id = ?`,
		channelID); err != nil {
		t.Fatalf("seed channel: %v", err)
	}
	if rejectedOp {
		if _, err := db.Exec(
			`INSERT INTO replay_log_rejects (channel_id, msg_id, error, detected_at)
			 VALUES (?, 2403, 'projection: object not found', 0)`, channelID); err != nil {
			t.Fatalf("seed rejected op: %v", err)
		}
	} else if _, err := db.Exec(
		`INSERT INTO folders (channel_id, id, name, parent_id, tombstoned) VALUES
		   (?, 'd:tombstoned', 'Deleted', '', 1),
		   (?, 'd:child', 'Orphan', ?, 0)`,
		channelID, channelID, parentID); err != nil {
		t.Fatalf("seed folders: %v", err)
	}
	if _, err := db.Exec(`DELETE FROM schema_version; INSERT INTO schema_version (version) VALUES (8)`); err != nil {
		t.Fatalf("rewind schema version: %v", err)
	}

	if err := projection.MigratePersonalChannel(db, channelID); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	channel, err := projection.GetChannel(db, channelID)
	if err != nil {
		t.Fatalf("GetChannel() error = %v", err)
	}
	return channel
}
