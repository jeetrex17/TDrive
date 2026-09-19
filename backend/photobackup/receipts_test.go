package photobackup

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
)

// completeReceipts queues one asset per name and uploads each, returning the
// remote message ids the drive handed back, in queue order.
func completeReceipts(t *testing.T, e *Engine, scope Scope, names ...string) []int64 {
	t.Helper()
	ctx := context.Background()
	assets := make([]Asset, 0, len(names))
	for i, name := range names {
		assets = append(assets, Asset{ID: name, Version: "v1", Path: "/photos/" + name, Name: name, MediaType: "photo", ModifiedAt: time.Unix(int64(i+1), 0), Size: 1})
	}
	if _, err := e.EnqueuePage(ctx, scope, "camera", assets); err != nil {
		t.Fatal(err)
	}
	ids := make([]int64, 0, len(names))
	for i := range names {
		id := int64(2490 + i)
		uploaded, err := e.RunOnce(ctx, scope, func(context.Context, UploadRequest) (UploadResult, error) {
			return UploadResult{RemoteMessageID: id}, nil
		})
		if err != nil || uploaded != 1 {
			t.Fatalf("upload %d: uploaded=%d err=%v", i, uploaded, err)
		}
		ids = append(ids, id)
	}
	return ids
}

func lost(ids ...int64) RemoteIndex {
	return func(context.Context, int64, []int64) ([]int64, error) { return ids, nil }
}

func TestReceiptSweepHoldsLostUploadsForAnExplicitRetry(t *testing.T) {
	now := time.Unix(1000, 0)
	e, scope := testEngine(t, &now)
	configure(t, e, scope)
	ctx := context.Background()
	ids := completeReceipts(t, e, scope, "kept.jpg", "deleted.jpg")

	sweep, err := e.ReconcileReceipts(ctx, scope, lost(ids[1]), 0)
	if err != nil || sweep.Examined != 2 || sweep.Gone != 1 || sweep.Restored != 0 || !sweep.Complete {
		t.Fatalf("sweep=%+v err=%v", sweep, err)
	}
	status, err := e.Status(ctx, scope)
	if err != nil || status.Complete != 1 || status.Missing != 1 || status.Pending != 0 {
		t.Fatalf("status=%+v err=%v", status, err)
	}
	if status.LastError != ReceiptGoneReason {
		t.Fatalf("status message = %q, want the panel's reason", status.LastError)
	}

	// The whole point of the Missing state: a deliberate delete is not undone
	// behind the user's back, however often backup runs.
	uploaded, err := e.RunOnce(ctx, scope, func(context.Context, UploadRequest) (UploadResult, error) {
		t.Fatal("a lost receipt re-uploaded without the user asking")
		return UploadResult{}, nil
	})
	if err != nil || uploaded != 0 {
		t.Fatalf("uploaded=%d err=%v", uploaded, err)
	}

	if err := e.RetryErrors(ctx, scope); err != nil {
		t.Fatal(err)
	}
	uploaded, err = e.RunOnce(ctx, scope, func(_ context.Context, r UploadRequest) (UploadResult, error) {
		if r.Asset.ID != "deleted.jpg" {
			t.Fatalf("retry uploaded %q", r.Asset.ID)
		}
		return UploadResult{RemoteMessageID: 3001}, nil
	})
	if err != nil || uploaded != 1 {
		t.Fatalf("retried upload=%d err=%v", uploaded, err)
	}
	status, err = e.Status(ctx, scope)
	if err != nil || status.Complete != 2 || status.Missing != 0 {
		t.Fatalf("after retry status=%+v err=%v", status, err)
	}
}

func TestReceiptSweepRestoresAReceiptWhoseFileComesBack(t *testing.T) {
	now := time.Unix(2000, 0)
	e, scope := testEngine(t, &now)
	configure(t, e, scope)
	ctx := context.Background()
	ids := completeReceipts(t, e, scope, "restored.jpg")

	if _, err := e.ReconcileReceipts(ctx, scope, lost(ids[0]), 0); err != nil {
		t.Fatal(err)
	}
	sweep, err := e.ReconcileReceipts(ctx, scope, lost(), 0)
	if err != nil || sweep.Restored != 1 || sweep.Gone != 0 {
		t.Fatalf("sweep=%+v err=%v", sweep, err)
	}
	status, err := e.Status(ctx, scope)
	if err != nil || status.Complete != 1 || status.Missing != 0 || status.LastError != "" {
		t.Fatalf("status=%+v err=%v", status, err)
	}
}

func TestReceiptSweepIsBoundedResumableAndIdempotent(t *testing.T) {
	now := time.Unix(3000, 0)
	e, scope := testEngine(t, &now)
	configure(t, e, scope)
	ctx := context.Background()
	names := make([]string, 0, 5)
	for i := 0; i < 5; i++ {
		names = append(names, fmt.Sprintf("photo-%d.jpg", i))
	}
	ids := completeReceipts(t, e, scope, names...)

	var seen []int64
	index := func(_ context.Context, drive int64, batch []int64) ([]int64, error) {
		if drive != scope.DriveID {
			t.Fatalf("drive=%d", drive)
		}
		if len(batch) > 2 {
			t.Fatalf("batch of %d exceeded the budget", len(batch))
		}
		seen = append(seen, batch...)
		return nil, nil
	}
	for round := 0; round < 3; round++ {
		sweep, err := e.ReconcileReceipts(ctx, scope, index, 2)
		if err != nil {
			t.Fatal(err)
		}
		if complete := round == 2; sweep.Complete != complete {
			t.Fatalf("round %d sweep=%+v", round, sweep)
		}
	}
	if len(seen) != len(ids) {
		t.Fatalf("walked %v, want each receipt once", seen)
	}
	for i, id := range ids {
		if seen[i] != id {
			t.Fatalf("walk %v, want ascending %v", seen, ids)
		}
	}
	// A completed cycle rewinds, so a file that reappears later is noticed.
	var cursor int64
	if err := e.db.QueryRow(`SELECT receipt_cursor FROM photo_backup_settings WHERE account_id=? AND drive_id=?`, scope.AccountID, scope.DriveID).Scan(&cursor); err != nil || cursor != 0 {
		t.Fatalf("cursor=%d err=%v", cursor, err)
	}

	// Repeating the sweep changes nothing, including the maintained counters.
	for i := 0; i < 2; i++ {
		if _, err := e.ReconcileReceipts(ctx, scope, lost(ids[0], ids[1]), 0); err != nil {
			t.Fatal(err)
		}
	}
	status, err := e.Status(ctx, scope)
	if err != nil || status.Missing != 2 || status.Complete != 3 {
		t.Fatalf("status=%+v err=%v", status, err)
	}
	var counted int64
	if err := e.db.QueryRow(`SELECT COUNT(*) FROM photo_backup_jobs WHERE account_id=? AND drive_id=? AND status=?`, scope.AccountID, scope.DriveID, Missing).Scan(&counted); err != nil || counted != status.Missing {
		t.Fatalf("counter=%d rows=%d err=%v", status.Missing, counted, err)
	}
}

func TestReceiptSweepLeavesQueuedAndHeldJobsAlone(t *testing.T) {
	now := time.Unix(4000, 0)
	e, scope := testEngine(t, &now)
	configure(t, e, scope)
	ctx := context.Background()
	if _, err := e.EnqueuePage(ctx, scope, "camera", []Asset{{ID: "queued.jpg", Version: "v1", Path: "/photos/queued.jpg", Name: "queued.jpg", MediaType: "photo", ModifiedAt: now, Size: 1}}); err != nil {
		t.Fatal(err)
	}
	// An upload whose remote outcome is unknown carries no receipt, so the
	// sweep has nothing to compare and must not disturb its quarantine.
	if _, err := e.db.Exec(`UPDATE photo_backup_jobs SET status=? WHERE asset_id='queued.jpg'`, Uploading); err != nil {
		t.Fatal(err)
	}
	if err := e.RecoverInterrupted(ctx, scope); err != nil {
		t.Fatal(err)
	}
	sweep, err := e.ReconcileReceipts(ctx, scope, func(_ context.Context, _ int64, batch []int64) ([]int64, error) {
		t.Fatalf("receipt-less job offered to the drive: %v", batch)
		return nil, nil
	}, 0)
	if err != nil || sweep.Examined != 0 || !sweep.Complete {
		t.Fatalf("sweep=%+v err=%v", sweep, err)
	}
	status, err := e.Status(ctx, scope)
	if err != nil || status.Paused != 1 || status.Missing != 0 {
		t.Fatalf("status=%+v err=%v", status, err)
	}
}

func TestReceiptWalkUsesTheReceiptIndex(t *testing.T) {
	now := time.Unix(5000, 0)
	e, scope := testEngine(t, &now)
	rows, err := e.db.Query(`EXPLAIN QUERY PLAN SELECT DISTINCT remote_message_id FROM photo_backup_jobs WHERE account_id=? AND drive_id=? AND remote_message_id>? AND status IN (?,?) ORDER BY remote_message_id LIMIT 1`, scope.AccountID, scope.DriveID, 0, Complete, Missing)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	plan := ""
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		plan += detail
	}
	// A receipt walk that scanned the table or sorted its output would make
	// every launch pay for the whole ledger.
	if !strings.Contains(plan, "photo_backup_jobs_receipts") || strings.Contains(plan, "SCAN") || strings.Contains(plan, "TEMP B-TREE") {
		t.Fatalf("unexpected plan: %s", plan)
	}
}

func TestMigrateVersionFourAddsReceiptReconciliation(t *testing.T) {
	now := time.Unix(6000, 0)
	e, scope := testEngine(t, &now)
	configure(t, e, scope)
	ctx := context.Background()
	ids := completeReceipts(t, e, scope, "old.jpg")
	if _, err := e.db.Exec(`DROP INDEX photo_backup_jobs_receipts; ALTER TABLE photo_backup_settings DROP COLUMN receipt_cursor; ALTER TABLE photo_backup_jobs DROP COLUMN rel_dir; PRAGMA user_version=4`); err != nil {
		t.Fatal(err)
	}
	if err := e.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	var version int
	if err := e.db.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil || version != schemaVersion {
		t.Fatalf("version=%d err=%v", version, err)
	}
	sweep, err := e.ReconcileReceipts(ctx, scope, lost(ids[0]), 0)
	if err != nil || sweep.Gone != 1 {
		t.Fatalf("sweep=%+v err=%v", sweep, err)
	}
	if err := e.Migrate(ctx); err != nil {
		t.Fatalf("repeat migration: %v", err)
	}
}

func TestReconcileReceiptsRejectsInvalidArguments(t *testing.T) {
	now := time.Unix(7000, 0)
	e, scope := testEngine(t, &now)
	if _, err := e.ReconcileReceipts(context.Background(), Scope{}, lost(), 0); err != ErrInvalid {
		t.Fatalf("invalid scope err=%v", err)
	}
	if _, err := e.ReconcileReceipts(context.Background(), scope, nil, 0); err != ErrInvalid {
		t.Fatalf("missing index err=%v", err)
	}
}
