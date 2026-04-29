package backfill

import (
	"context"
	"database/sql"
	"testing"

	"TDrive/backend/projection"
	"TDrive/backend/tgclient"

	_ "modernc.org/sqlite"
)

const testChan int64 = 7777

type fakePeers struct{}

func (fakePeers) ResolvePeer(ctx context.Context, channelID int64) (tgclient.InputPeer, error) {
	return tgclient.InputPeer{ChannelID: channelID, AccessHash: 1}, nil
}

func newBackfillEnv(t *testing.T) (*sql.DB, *tgclient.Fake, *Runner) {
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
	return db, tg, NewRunner(db, tg, fakePeers{})
}

func seedDirectFolder(t *testing.T, db *sql.DB, id, parent, name string) {
	t.Helper()
	if _, err := db.Exec(`
		INSERT INTO folders (channel_id, id, name, parent_id, tombstoned)
		VALUES (?, ?, ?, ?, 0)
	`, testChan, id, name, parent); err != nil {
		t.Fatalf("seed folder: %v", err)
	}
}

func seedDirectFile(t *testing.T, db *sql.DB, msgID int64, parent, name string) {
	t.Helper()
	if _, err := db.Exec(`
		INSERT INTO files (channel_id, msg_id, name, size, parent_id, upload_time, uploader_user_id, tombstoned)
		VALUES (?, ?, ?, 100, ?, 1700000000, 0, 0)
	`, testChan, msgID, name, parent); err != nil {
		t.Fatalf("seed file: %v", err)
	}
}

func TestBackfillPublishesMkdirAndMeta(t *testing.T) {
	db, tg, r := newBackfillEnv(t)
	seedDirectFolder(t, db, "d:a", projection.RootParent, "A")
	seedDirectFolder(t, db, "d:b", "d:a", "B")
	seedDirectFile(t, db, 1001, projection.RootParent, "root.txt")
	seedDirectFile(t, db, 1002, "d:a", "in_a.txt")

	if err := r.RunPersonal(context.Background(), testChan, nil); err != nil {
		t.Fatalf("backfill: %v", err)
	}
	controls := tg.SentControls()
	if len(controls) != 4 {
		t.Fatalf("control count = %d, want 4", len(controls))
	}

	// First two ops must be mkdir, ordered parents-first.
	if !contains(controls[0].Text, "t=mkdir") || !contains(controls[0].Text, "obj=d:a") {
		t.Fatalf("control[0] = %q, want mkdir d:a", controls[0].Text)
	}
	if !contains(controls[1].Text, "t=mkdir") || !contains(controls[1].Text, "obj=d:b") {
		t.Fatalf("control[1] = %q, want mkdir d:b", controls[1].Text)
	}
	for _, c := range controls {
		if !c.Silent {
			t.Fatalf("control %q not silent", c.Text)
		}
	}

	// Next two must be meta ops.
	metaCount := 0
	for _, c := range controls[2:] {
		if contains(c.Text, "t=meta") {
			metaCount++
		}
	}
	if metaCount != 2 {
		t.Fatalf("meta op count = %d, want 2", metaCount)
	}

	// Flag flipped.
	var done int
	if err := db.QueryRow(`SELECT personal_backfill_done FROM channels WHERE channel_id=?`, testChan).Scan(&done); err != nil {
		t.Fatalf("scan flag: %v", err)
	}
	if done != 1 {
		t.Fatalf("personal_backfill_done = %d, want 1", done)
	}

	// Replay_log rows match (4 backfilled).
	var rows int
	_ = db.QueryRow(`SELECT COUNT(*) FROM replay_log WHERE channel_id=?`, testChan).Scan(&rows)
	if rows != 4 {
		t.Fatalf("replay_log rows = %d, want 4", rows)
	}
}

func TestBackfillIsResumableViaReplayLog(t *testing.T) {
	db, tg, r := newBackfillEnv(t)
	seedDirectFolder(t, db, "d:a", projection.RootParent, "A")
	seedDirectFolder(t, db, "d:b", projection.RootParent, "B")

	// Simulate first run completing only the d:a mkdir before crashing,
	// by manually inserting a replay_log row for d:a.
	preOp := projection.Op{Type: projection.OpMkdir, Obj: "d:a", Parent: projection.RootParent, Name: "A"}
	preHeader := projection.Format(preOp)
	if _, err := projection.ProjectFromOp(db, testChan, 50, preOp, 0, preHeader); err != nil {
		t.Fatalf("preseed replay: %v", err)
	}

	if err := r.RunPersonal(context.Background(), testChan, nil); err != nil {
		t.Fatalf("backfill: %v", err)
	}
	controls := tg.SentControls()
	// Only d:b should be sent now; d:a was already in replay_log.
	if len(controls) != 1 {
		t.Fatalf("control count = %d, want 1 (d:a was already published)", len(controls))
	}
	if !contains(controls[0].Text, "obj=d:b") {
		t.Fatalf("control[0] = %q, want d:b", controls[0].Text)
	}
}

func TestBackfillSkipsIfAlreadyDone(t *testing.T) {
	db, tg, r := newBackfillEnv(t)
	if _, err := db.Exec(`UPDATE channels SET personal_backfill_done = 1 WHERE channel_id = ?`, testChan); err != nil {
		t.Fatalf("flag: %v", err)
	}
	seedDirectFolder(t, db, "d:a", projection.RootParent, "A")
	if err := r.RunPersonal(context.Background(), testChan, nil); err != nil {
		t.Fatalf("backfill: %v", err)
	}
	if len(tg.SentControls()) != 0 {
		t.Fatalf("expected zero controls, got %d", len(tg.SentControls()))
	}
}

func TestBackfillRetriesOnFloodWait(t *testing.T) {
	db, tg, r := newBackfillEnv(t)
	seedDirectFolder(t, db, "d:a", projection.RootParent, "A")
	tg.InjectFloodWaits(2)

	if err := r.RunPersonal(context.Background(), testChan, nil); err != nil {
		t.Fatalf("backfill: %v", err)
	}
	controls := tg.SentControls()
	if len(controls) != 1 {
		t.Fatalf("control count = %d, want 1 after retries", len(controls))
	}
}

func TestBackfillEmitsProgress(t *testing.T) {
	db, tg, r := newBackfillEnv(t)
	_ = tg
	seedDirectFolder(t, db, "d:a", projection.RootParent, "A")
	seedDirectFile(t, db, 1, projection.RootParent, "x.txt")

	var events []ProgressEvent
	if err := r.RunPersonal(context.Background(), testChan, func(e ProgressEvent) {
		events = append(events, e)
	}); err != nil {
		t.Fatalf("backfill: %v", err)
	}
	if len(events) < 2 {
		t.Fatalf("expected at least 2 progress events, got %d", len(events))
	}
	last := events[len(events)-1]
	if last.Phase != "done" {
		t.Fatalf("last phase = %q, want done", last.Phase)
	}
	if last.Total != 2 || last.Done != 2 {
		t.Fatalf("last event = %+v, want Total=2 Done=2", last)
	}
}

func contains(haystack, needle string) bool {
	return len(haystack) >= len(needle) && stringIndex(haystack, needle) >= 0
}

func stringIndex(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
