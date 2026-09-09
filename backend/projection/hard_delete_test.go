package projection

import (
	"context"
	"database/sql"
	"errors"
	"math"
	"reflect"
	"testing"
)

func TestHardDeleteWireRoundTripHasNoRetentionFields(t *testing.T) {
	op := Op{
		Type: OpHardDeleteTree, ProtocolVersion: 1, OpID: "hard-delete-1",
		Obj: "d:docs", ExpectedRevision: 4,
	}
	wire := Format(op)
	got, err := Parse(wire)
	if err != nil {
		t.Fatalf("Parse(%q): %v", wire, err)
	}
	if !reflect.DeepEqual(got, op) {
		t.Fatalf("round trip\n got: %#v\nwant: %#v\nwire: %s", got, op, wire)
	}
	if got.DeletedAt != 0 || got.PurgeAfter != 0 || got.RetainedUntil != 0 {
		t.Fatalf("hard delete retained trash metadata: %+v", got)
	}
}

func TestHardDeleteFilePlansBodyAndExcludesControlMessages(t *testing.T) {
	db := newTestDB(t)
	mustOp(t, db, 101, Op{
		Type: OpFileCommit, ProtocolVersion: 1, OpID: "create-modern",
		Name: "report.txt", ContentMsgID: 9001,
	})
	if _, err := db.Exec(`UPDATE file_revisions SET retained_until=123 WHERE channel_id=? AND file_msg_id=101`, testChan); err != nil {
		t.Fatal(err)
	}
	mustOp(t, db, 102, Op{
		Type: OpHardDeleteTree, ProtocolVersion: 1, OpID: "delete-modern",
		Obj: "f:101", ExpectedRevision: 1,
	})

	assertHardDeletePlan(t, db, "delete-modern", []int64{9001}, 1, true)
	if _, ok, err := FileByID(db, testChan, 101); err != nil || ok {
		t.Fatalf("FileByID after hard delete: ok=%v err=%v", ok, err)
	}
	var retainedUntil int64
	if err := db.QueryRow(`
		SELECT retained_until FROM file_revisions
		WHERE channel_id=? AND file_msg_id=101
	`, testChan).Scan(&retainedUntil); err != nil {
		t.Fatal(err)
	}
	if retainedUntil != 0 {
		t.Fatalf("retained_until=%d, want 0", retainedUntil)
	}
	var trashCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM trash_entries WHERE channel_id=?`, testChan).Scan(&trashCount); err != nil {
		t.Fatal(err)
	}
	if trashCount != 0 {
		t.Fatalf("hard delete created %d trash entries", trashCount)
	}
}

func TestHardDeleteLegacySingleMessagePlansBodyControl(t *testing.T) {
	db := newTestDB(t)
	mustOp(t, db, 31, Op{Type: OpFileUpload, Name: "legacy.txt", FileSize: 8})
	mustOp(t, db, 40, Op{
		Type: OpHardDeleteTree, ProtocolVersion: 1, OpID: "delete-legacy",
		Obj: "f:31", ExpectedRevision: 1,
	})

	// Legacy uploads store bytes in the original file message itself, so that
	// message is both the historical control and the body that must be removed.
	assertHardDeletePlan(t, db, "delete-legacy", []int64{31}, 1, true)
}

func TestHardDeleteFolderPlansEveryHistoricalBodyAndMultipartPart(t *testing.T) {
	db := newTestDB(t)
	mustOp(t, db, 1, Op{
		Type: OpFolderCommit, ProtocolVersion: 1, OpID: "folder-root",
		Obj: "d:root", Name: "Root",
	})
	mustOp(t, db, 2, Op{
		Type: OpFolderCommit, ProtocolVersion: 1, OpID: "folder-child",
		Obj: "d:child", Parent: "d:root", Name: "Child",
	})
	mustOp(t, db, 101, Op{
		Type: OpFileCommit, ProtocolVersion: 1, OpID: "historical-base",
		Parent: "d:child", Name: "history.bin", ContentMsgID: 9001,
	})
	mustOp(t, db, 110, Op{Type: OpFilePart, UploadUUID: "history-parts", PartIndex: 0, FileSize: 4})
	mustOp(t, db, 111, Op{Type: OpFilePart, UploadUUID: "history-parts", PartIndex: 1, FileSize: 6})
	mustOp(t, db, 112, Op{
		Type: OpFileReplace, ProtocolVersion: 1, OpID: "historical-replace",
		Obj: "f:101", ExpectedRevision: 1, UploadUUID: "history-parts",
		PartCount: 2, FileSize: 10, RetainedUntil: 500,
	})
	mustOp(t, db, 201, Op{
		Type: OpFileCommit, ProtocolVersion: 1, OpID: "outside",
		Name: "outside.txt", ContentMsgID: 9900,
	})
	mustOp(t, db, 120, Op{
		Type: OpHardDeleteTree, ProtocolVersion: 1, OpID: "delete-folder",
		Obj: "d:root", ExpectedRevision: 1,
	})

	assertHardDeletePlan(t, db, "delete-folder", []int64{110, 111, 9001}, 3, true)
	for _, objectID := range []string{"d:root", "d:child", "f:101"} {
		entry, ok, err := DirentByID(db, testChan, objectID)
		if err != nil || !ok || !entry.Tombstoned {
			t.Fatalf("dirent %s = %+v, ok=%v err=%v", objectID, entry, ok, err)
		}
	}
	if _, ok, err := FileByID(db, testChan, 201); err != nil || !ok {
		t.Fatalf("outside file was hidden: ok=%v err=%v", ok, err)
	}
}

func TestHardDeleteFolderPlansPreviouslySoftDeletedDescendants(t *testing.T) {
	db := newTestDB(t)
	mustOp(t, db, 1, Op{
		Type: OpFolderCommit, ProtocolVersion: 1, OpID: "folder-with-trash",
		Obj: "d:root", Name: "Root",
	})
	mustOp(t, db, 101, Op{
		Type: OpFileCommit, ProtocolVersion: 1, OpID: "file-before-trash",
		Parent: "d:root", Name: "old.txt", ContentMsgID: 9001,
	})
	mustOp(t, db, 102, Op{
		Type: OpTrashTree, ProtocolVersion: 1, OpID: "soft-delete-child",
		Obj: "f:101", ExpectedRevision: 1, DeletedAt: 100, PurgeAfter: 200,
	})
	mustOp(t, db, 103, Op{
		Type: OpHardDeleteTree, ProtocolVersion: 1, OpID: "hard-delete-parent",
		Obj: "d:root", ExpectedRevision: 1,
	})

	assertHardDeletePlan(t, db, "hard-delete-parent", []int64{9001}, 1, true)
	var trashCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM trash_entries WHERE channel_id=?`, testChan).Scan(&trashCount); err != nil {
		t.Fatal(err)
	}
	if trashCount != 0 {
		t.Fatalf("hard-deleted subtree retained %d trash entries", trashCount)
	}
}

func TestHardDeleteRejectsKnownControlAndOversizedBodyReferences(t *testing.T) {
	for _, test := range []struct {
		name      string
		bodyMsgID int64
		seed      func(*testing.T, *sql.DB)
	}{
		{
			name:      "known non-body control",
			bodyMsgID: 50,
			seed: func(t *testing.T, db *sql.DB) {
				t.Helper()
				op := Op{Type: OpFolderCommit, ProtocolVersion: 1, OpID: "unrelated-control", Obj: "d:control", Name: "Control"}
				if _, err := ProjectFromOp(db, testChan, 50, op, 0, Format(op)); err != nil {
					t.Fatalf("seed control: %v", err)
				}
			},
		},
		{name: "outside Telegram integer range", bodyMsgID: math.MaxInt32 + 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			db := newTestDB(t)
			if test.seed != nil {
				test.seed(t, db)
			}
			mustOp(t, db, 101, Op{
				Type: OpFileCommit, ProtocolVersion: 1, OpID: "file-with-unsafe-reference",
				Name: "unsafe.bin", ContentMsgID: test.bodyMsgID,
			})
			op := Op{
				Type: OpHardDeleteTree, ProtocolVersion: 1, OpID: "reject-unsafe-reference",
				Obj: "f:101", ExpectedRevision: 1,
			}
			if err := RegisterHardDeleteIntent(context.Background(), db, testChan, op.OpID, op.Obj, op.ExpectedRevision); err != nil {
				t.Fatalf("register hard-delete intent: %v", err)
			}
			err := runOp(t, db, testChan, 102, op)
			if !errors.Is(err, ErrBadOp) {
				t.Fatalf("unsafe hard delete error=%v, want ErrBadOp", err)
			}
			if _, ok, lookupErr := FileByID(db, testChan, 101); lookupErr != nil || !ok {
				t.Fatalf("rejected hard delete mutated file: ok=%v err=%v", ok, lookupErr)
			}
		})
	}
}

func TestHardDeleteRejectsSharedChannelControl(t *testing.T) {
	db := newTestDB(t)
	mustOp(t, db, 101, Op{Type: OpFileUpload, Name: "shared.txt", FileSize: 1})
	if _, err := db.Exec(`UPDATE channels SET kind=? WHERE channel_id=?`, KindShared, testChan); err != nil {
		t.Fatal(err)
	}
	err := runOp(t, db, testChan, 102, Op{
		Type: OpHardDeleteTree, ProtocolVersion: 1, OpID: "shared-hard-delete",
		Obj: "f:101", ExpectedRevision: 1,
	})
	if !errors.Is(err, ErrBadOp) {
		t.Fatalf("shared hard delete error=%v, want ErrBadOp", err)
	}
	if _, ok, lookupErr := FileByID(db, testChan, 101); lookupErr != nil || !ok {
		t.Fatalf("shared hard delete mutated file: ok=%v err=%v", ok, lookupErr)
	}
}

func TestHardDeleteRejectsIncompleteHistoricalMultipart(t *testing.T) {
	db := newTestDB(t)
	mustOp(t, db, 10, Op{Type: OpFilePart, UploadUUID: "damaged", PartIndex: 0, FileSize: 4})
	mustOp(t, db, 11, Op{Type: OpFilePart, UploadUUID: "damaged", PartIndex: 1, FileSize: 6})
	mustOp(t, db, 101, Op{
		Type: OpFileCommit, ProtocolVersion: 1, OpID: "damaged-file",
		Name: "damaged.bin", UploadUUID: "damaged", PartCount: 2, FileSize: 10,
	})
	if _, err := db.Exec(`DELETE FROM file_parts WHERE channel_id=? AND msg_id=11`, testChan); err != nil {
		t.Fatal(err)
	}
	op := Op{
		Type: OpHardDeleteTree, ProtocolVersion: 1, OpID: "delete-damaged",
		Obj: "f:101", ExpectedRevision: 1,
	}
	if err := RegisterHardDeleteIntent(context.Background(), db, testChan, op.OpID, op.Obj, op.ExpectedRevision); err != nil {
		t.Fatalf("register hard-delete intent: %v", err)
	}
	err := runOp(t, db, testChan, 102, op)
	if !errors.Is(err, ErrContentIncomplete) {
		t.Fatalf("hard delete incomplete multipart error=%v, want ErrContentIncomplete", err)
	}
	if _, ok, lookupErr := FileByID(db, testChan, 101); lookupErr != nil || !ok {
		t.Fatalf("failed hard delete mutated file: ok=%v err=%v", ok, lookupErr)
	}
}

func TestHardDeleteRejectsBodiesReferencedOutsideSubtree(t *testing.T) {
	for _, test := range []struct {
		name    string
		prepare func(t *testing.T, db *sql.DB)
	}{
		{
			name: "single message body",
			prepare: func(t *testing.T, db *sql.DB) {
				t.Helper()
				mustOp(t, db, 101, Op{
					Type: OpFileCommit, ProtocolVersion: 1, OpID: "owned-direct",
					Parent: "d:inside", Name: "inside.bin", ContentMsgID: 9001,
				})
				mustOp(t, db, 102, Op{
					Type: OpFileCommit, ProtocolVersion: 1, OpID: "outside-direct",
					Name: "outside.bin", ContentMsgID: 9002,
				})
				if _, err := db.Exec(`
					UPDATE file_revisions SET content_msg_id=9001
					WHERE channel_id=? AND file_msg_id=102
				`, testChan); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "multipart body",
			prepare: func(t *testing.T, db *sql.DB) {
				t.Helper()
				mustOp(t, db, 10, Op{Type: OpFilePart, UploadUUID: "owned-parts", PartIndex: 0, FileSize: 4})
				mustOp(t, db, 11, Op{Type: OpFilePart, UploadUUID: "owned-parts", PartIndex: 1, FileSize: 6})
				mustOp(t, db, 101, Op{
					Type: OpFileCommit, ProtocolVersion: 1, OpID: "owned-multipart",
					Parent: "d:inside", Name: "inside.bin", UploadUUID: "owned-parts",
					PartCount: 2, FileSize: 10,
				})
				mustOp(t, db, 102, Op{
					Type: OpFileCommit, ProtocolVersion: 1, OpID: "outside-multipart",
					Name: "outside.bin", ContentMsgID: 9002,
				})
				if _, err := db.Exec(`
					UPDATE file_revisions
					SET content_msg_id=0, upload_uuid='owned-parts', part_count=2, size=10
					WHERE channel_id=? AND file_msg_id=102
				`, testChan); err != nil {
					t.Fatal(err)
				}
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			db := newTestDB(t)
			mustOp(t, db, 1, Op{
				Type: OpFolderCommit, ProtocolVersion: 1, OpID: "inside-folder",
				Obj: "d:inside", Name: "Inside",
			})
			test.prepare(t, db)

			op := Op{
				Type: OpHardDeleteTree, ProtocolVersion: 1, OpID: "delete-shared-body",
				Obj: "d:inside", ExpectedRevision: 1,
			}
			if err := RegisterHardDeleteIntent(context.Background(), db, testChan, op.OpID, op.Obj, op.ExpectedRevision); err != nil {
				t.Fatalf("register hard-delete intent: %v", err)
			}
			err := runOp(t, db, testChan, 200, op)
			if !errors.Is(err, ErrContentAlreadyCommitted) {
				t.Fatalf("hard delete shared body error=%v, want ErrContentAlreadyCommitted", err)
			}
			entry, ok, lookupErr := DirentByID(db, testChan, "d:inside")
			if lookupErr != nil || !ok || entry.Tombstoned {
				t.Fatalf("failed hard delete mutated subtree root: %+v ok=%v err=%v", entry, ok, lookupErr)
			}
			if _, _, _, planErr := HardDeletePlanPage(context.Background(), db, testChan, "delete-shared-body", 0, 10); !errors.Is(planErr, ErrHardDeletePlanNotFound) {
				t.Fatalf("failed hard delete persisted plan: %v", planErr)
			}
		})
	}
}

func TestHardDeletePlanPagesAndCompletionCompactsState(t *testing.T) {
	db := newTestDB(t)
	for index, msgID := range []int64{10, 20, 30} {
		mustOp(t, db, msgID, Op{Type: OpFilePart, UploadUUID: "paged", PartIndex: index, FileSize: 1})
	}
	mustOp(t, db, 101, Op{
		Type: OpFileCommit, ProtocolVersion: 1, OpID: "paged-file",
		Name: "paged.bin", UploadUUID: "paged", PartCount: 3, FileSize: 3,
	})
	mustOp(t, db, 102, Op{
		Type: OpHardDeleteTree, ProtocolVersion: 1, OpID: "delete-paged",
		Obj: "f:101", ExpectedRevision: 1,
	})

	ids, total, done, err := HardDeletePlanPage(context.Background(), db, testChan, "delete-paged", 0, 2)
	if err != nil || !reflect.DeepEqual(ids, []int64{10, 20}) || total != 3 || done {
		t.Fatalf("first page=(%v,%d,%t,%v)", ids, total, done, err)
	}
	ids, total, done, err = HardDeletePlanPage(context.Background(), db, testChan, "delete-paged", 20, 2)
	if err != nil || !reflect.DeepEqual(ids, []int64{30}) || total != 3 || !done {
		t.Fatalf("second page=(%v,%d,%t,%v)", ids, total, done, err)
	}
	if err := CompleteHardDeletePlan(context.Background(), db, testChan, "delete-paged"); err != nil {
		t.Fatalf("complete plan: %v", err)
	}
	ids, total, done, err = HardDeletePlanPage(context.Background(), db, testChan, "delete-paged", 0, 2)
	if err != nil || len(ids) != 0 || total != 3 || !done {
		t.Fatalf("completed page=(%v,%d,%t,%v)", ids, total, done, err)
	}
	var parts int
	if err := db.QueryRow(`SELECT COUNT(*) FROM file_parts WHERE channel_id=? AND upload_uuid='paged'`, testChan).Scan(&parts); err != nil {
		t.Fatal(err)
	}
	if parts != 0 {
		t.Fatalf("completed cleanup retained %d part pointers", parts)
	}
	// Completion is idempotent for recovery retries.
	if err := CompleteHardDeletePlan(context.Background(), db, testChan, "delete-paged"); err != nil {
		t.Fatalf("repeat complete: %v", err)
	}
}

func TestHardDeleteCompletedJobSurvivesRebuildWithoutRequeue(t *testing.T) {
	db := newTestDB(t)
	project := func(msgID int64, op Op) {
		t.Helper()
		if _, err := ProjectFromOp(db, testChan, msgID, op, 0, Format(op)); err != nil {
			t.Fatalf("project msg %d: %v", msgID, err)
		}
	}
	project(10, Op{Type: OpFilePart, UploadUUID: "rebuild-parts", PartIndex: 0, FileSize: 4})
	project(11, Op{Type: OpFilePart, UploadUUID: "rebuild-parts", PartIndex: 1, FileSize: 6})
	project(101, Op{
		Type: OpFileCommit, ProtocolVersion: 1, OpID: "rebuild-file",
		Name: "rebuild.bin", UploadUUID: "rebuild-parts", PartCount: 2, FileSize: 10,
	})
	if err := RegisterHardDeleteIntent(context.Background(), db, testChan, "rebuild-delete", "f:101", 1); err != nil {
		t.Fatalf("register intent: %v", err)
	}
	project(102, Op{
		Type: OpHardDeleteTree, ProtocolVersion: 1, OpID: "rebuild-delete",
		Obj: "f:101", ExpectedRevision: 1,
	})
	if err := CompleteHardDeletePlan(context.Background(), db, testChan, "rebuild-delete"); err != nil {
		t.Fatal(err)
	}
	if err := RebuildProjection(db, testChan); err != nil {
		t.Fatalf("rebuild: %v", err)
	}

	assertHardDeletePlan(t, db, "rebuild-delete", nil, 2, true)
	if _, ok, err := FileByID(db, testChan, 101); err != nil || ok {
		t.Fatalf("rebuilt hard-deleted file visible: ok=%v err=%v", ok, err)
	}
	var parts int
	if err := db.QueryRow(`SELECT COUNT(*) FROM file_parts WHERE channel_id=? AND upload_uuid='rebuild-parts'`, testChan).Scan(&parts); err != nil {
		t.Fatal(err)
	}
	if parts != 0 {
		t.Fatalf("rebuild restored %d completed body pointers", parts)
	}
}

func TestRemoteHardDeleteReplayDoesNotCreateLocalCleanupWork(t *testing.T) {
	db := newTestDB(t)
	project := func(msgID int64, op Op) {
		t.Helper()
		if _, err := ProjectFromOp(db, testChan, msgID, op, 0, Format(op)); err != nil {
			t.Fatalf("project msg %d: %v", msgID, err)
		}
	}
	project(10, Op{Type: OpFilePart, UploadUUID: "remote-parts", PartIndex: 0, FileSize: 4})
	project(101, Op{
		Type: OpFileCommit, ProtocolVersion: 1, OpID: "remote-file",
		Name: "remote.bin", UploadUUID: "remote-parts", PartCount: 1, FileSize: 4,
	})
	project(102, Op{
		Type: OpHardDeleteTree, ProtocolVersion: 1, OpID: "remote-delete",
		Obj: "f:101", ExpectedRevision: 1,
	})

	assertNoFileParts(t, db, "remote-parts")
	assertNoHardDeleteCleanupWork(t, db, "remote-delete")
	if _, ok, err := FileByID(db, testChan, 101); err != nil || ok {
		t.Fatalf("remote marker did not hide file: ok=%v err=%v", ok, err)
	}
	if err := RebuildProjection(db, testChan); err != nil {
		t.Fatalf("rebuild: %v", err)
	}
	assertNoFileParts(t, db, "remote-parts")
	assertNoHardDeleteCleanupWork(t, db, "remote-delete")
	if _, ok, err := FileByID(db, testChan, 101); err != nil || ok {
		t.Fatalf("rebuilt remote marker did not hide file: ok=%v err=%v", ok, err)
	}
}

func TestRemoteHardDeleteReplayTombstonesWithIncompleteMultipartIndex(t *testing.T) {
	db := newTestDB(t)
	project := func(msgID int64, op Op) {
		t.Helper()
		if _, err := ProjectFromOp(db, testChan, msgID, op, 0, Format(op)); err != nil {
			t.Fatalf("project msg %d: %v", msgID, err)
		}
	}
	project(10, Op{Type: OpFilePart, UploadUUID: "partial-remote", PartIndex: 0, FileSize: 4})
	project(11, Op{Type: OpFilePart, UploadUUID: "partial-remote", PartIndex: 1, FileSize: 6})
	project(101, Op{
		Type: OpFileCommit, ProtocolVersion: 1, OpID: "partial-remote-file",
		Name: "partial.bin", UploadUUID: "partial-remote", PartCount: 2, FileSize: 10,
	})
	if _, err := db.Exec(`DELETE FROM file_parts WHERE channel_id=? AND msg_id=?`, testChan, 11); err != nil {
		t.Fatal(err)
	}

	project(102, Op{
		Type: OpHardDeleteTree, ProtocolVersion: 1, OpID: "partial-remote-delete",
		Obj: "f:101", ExpectedRevision: 1,
	})

	assertNoFileParts(t, db, "partial-remote")
	assertNoHardDeleteCleanupWork(t, db, "partial-remote-delete")
	if _, ok, err := FileByID(db, testChan, 101); err != nil || ok {
		t.Fatalf("remote marker did not hide file: ok=%v err=%v", ok, err)
	}
}

func TestHardDeleteIntentSurvivesReconciliationAndRebuildUntilCompletion(t *testing.T) {
	db := newTestDB(t)
	project := func(msgID int64, op Op) {
		t.Helper()
		if _, err := ProjectFromOp(db, testChan, msgID, op, 0, Format(op)); err != nil {
			t.Fatalf("project msg %d: %v", msgID, err)
		}
	}
	project(10, Op{Type: OpFilePart, UploadUUID: "owned-parts", PartIndex: 0, FileSize: 4})
	project(101, Op{
		Type: OpFileCommit, ProtocolVersion: 1, OpID: "owned-file",
		Name: "owned.bin", UploadUUID: "owned-parts", PartCount: 1, FileSize: 4,
	})
	ctx := context.Background()
	if err := RegisterHardDeleteIntent(ctx, db, testChan, "owned-delete", "f:101", 1); err != nil {
		t.Fatalf("register intent: %v", err)
	}
	project(102, Op{
		Type: OpHardDeleteTree, ProtocolVersion: 1, OpID: "owned-delete",
		Obj: "f:101", ExpectedRevision: 1,
	})

	// Receipt reconciliation may call registration again after projection has
	// already hidden the target. Exact retry must remain idempotent.
	if err := RegisterHardDeleteIntent(ctx, db, testChan, "owned-delete", "f:101", 1); err != nil {
		t.Fatalf("re-register projected intent: %v", err)
	}
	assertHardDeletePlan(t, db, "owned-delete", []int64{10}, 1, true)
	assertNoFileParts(t, db, "owned-parts")

	if err := RebuildProjection(db, testChan); err != nil {
		t.Fatalf("rebuild pending delete: %v", err)
	}
	assertHardDeletePlan(t, db, "owned-delete", []int64{10}, 1, true)
	assertNoFileParts(t, db, "owned-parts")

	if err := CompleteHardDeletePlan(ctx, db, testChan, "owned-delete"); err != nil {
		t.Fatalf("complete: %v", err)
	}
	assertHardDeleteIntentCount(t, db, "owned-delete", 0)
	assertHardDeletePlan(t, db, "owned-delete", nil, 1, true)
}

func TestAbandonHardDeleteIntentPreventsOrphanCleanupWork(t *testing.T) {
	db := newTestDB(t)
	mustOp(t, db, 101, Op{
		Type: OpFileCommit, ProtocolVersion: 1, OpID: "abandoned-file",
		Name: "abandoned.bin", ContentMsgID: 9001,
	})
	ctx := context.Background()
	if err := RegisterHardDeleteIntent(ctx, db, testChan, "abandoned-delete", "f:101", 1); err != nil {
		t.Fatalf("register intent: %v", err)
	}
	if err := AbandonHardDeleteIntent(ctx, db, testChan, "abandoned-delete"); err != nil {
		t.Fatalf("abandon intent: %v", err)
	}
	if err := AbandonHardDeleteIntent(ctx, db, testChan, "abandoned-delete"); err != nil {
		t.Fatalf("repeat abandon: %v", err)
	}
	assertHardDeleteIntentCount(t, db, "abandoned-delete", 0)

	// If a marker unexpectedly arrives after a caller definitively abandoned
	// its send, replay still hides the object but cannot enqueue local cleanup.
	if _, err := ProjectFromOp(db, testChan, 102, Op{
		Type: OpHardDeleteTree, ProtocolVersion: 1, OpID: "abandoned-delete",
		Obj: "f:101", ExpectedRevision: 1,
	}, 0, ""); err != nil {
		t.Fatalf("project late marker: %v", err)
	}
	assertNoHardDeleteCleanupWork(t, db, "abandoned-delete")
}

func TestCannotAbandonAppliedHardDeleteIntent(t *testing.T) {
	db := newTestDB(t)
	mustOp(t, db, 101, Op{
		Type: OpFileCommit, ProtocolVersion: 1, OpID: "applied-file",
		Name: "applied.bin", ContentMsgID: 9001,
	})
	ctx := context.Background()
	if err := RegisterHardDeleteIntent(ctx, db, testChan, "applied-delete", "f:101", 1); err != nil {
		t.Fatalf("register intent: %v", err)
	}
	if _, err := ProjectFromOp(db, testChan, 102, Op{
		Type: OpHardDeleteTree, ProtocolVersion: 1, OpID: "applied-delete",
		Obj: "f:101", ExpectedRevision: 1,
	}, 0, ""); err != nil {
		t.Fatalf("project marker: %v", err)
	}
	if err := AbandonHardDeleteIntent(ctx, db, testChan, "applied-delete"); !errors.Is(err, ErrHardDeleteIntentApplied) {
		t.Fatalf("abandon applied intent error=%v, want ErrHardDeleteIntentApplied", err)
	}
	assertHardDeleteIntentCount(t, db, "applied-delete", 1)
}

func TestHardDeletePlanAPIBoundaries(t *testing.T) {
	db := newTestDB(t)
	for _, test := range []struct {
		name      string
		ctx       context.Context
		db        *sql.DB
		channelID int64
		opID      string
		after     int64
		limit     int
	}{
		{"nil context", nil, db, testChan, "op", 0, 1},
		{"nil db", context.Background(), nil, testChan, "op", 0, 1},
		{"bad channel", context.Background(), db, 0, "op", 0, 1},
		{"empty op", context.Background(), db, testChan, "", 0, 1},
		{"negative cursor", context.Background(), db, testChan, "op", -1, 1},
		{"zero limit", context.Background(), db, testChan, "op", 0, 0},
		{"large limit", context.Background(), db, testChan, "op", 0, 1001},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, _, _, err := HardDeletePlanPage(test.ctx, test.db, test.channelID, test.opID, test.after, test.limit); err == nil {
				t.Fatal("HardDeletePlanPage succeeded")
			}
		})
	}
	if _, _, _, err := HardDeletePlanPage(context.Background(), db, testChan, "missing", 0, 1); !errors.Is(err, ErrHardDeletePlanNotFound) {
		t.Fatalf("missing plan error=%v", err)
	}
	if err := CompleteHardDeletePlan(context.Background(), db, testChan, "missing"); !errors.Is(err, ErrHardDeletePlanNotFound) {
		t.Fatalf("complete missing error=%v", err)
	}
}

func assertHardDeletePlan(t *testing.T, db *sql.DB, opID string, wantIDs []int64, wantTotal int64, wantDone bool) {
	t.Helper()
	ids, total, done, err := HardDeletePlanPage(context.Background(), db, testChan, opID, 0, 1000)
	if err != nil {
		t.Fatalf("HardDeletePlanPage(%q): %v", opID, err)
	}
	if !reflect.DeepEqual(ids, wantIDs) || total != wantTotal || done != wantDone {
		t.Fatalf("plan %q=(%v,%d,%t), want (%v,%d,%t)", opID, ids, total, done, wantIDs, wantTotal, wantDone)
	}
}

func assertNoHardDeleteCleanupWork(t *testing.T, db *sql.DB, opID string) {
	t.Helper()
	if _, _, _, err := HardDeletePlanPage(context.Background(), db, testChan, opID, 0, 1000); !errors.Is(err, ErrHardDeletePlanNotFound) {
		t.Fatalf("HardDeletePlanPage(%q) error=%v, want ErrHardDeletePlanNotFound", opID, err)
	}
	for _, table := range []string{"hard_delete_jobs", "hard_delete_plan_items"} {
		var count int
		if err := db.QueryRow(`SELECT COUNT(*) FROM `+table+` WHERE channel_id=? AND op_id=?`, testChan, opID).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 0 {
			t.Fatalf("remote marker retained %d rows in %s", count, table)
		}
	}
}

func assertNoFileParts(t *testing.T, db *sql.DB, uploadUUID string) {
	t.Helper()
	var count int
	if err := db.QueryRow(`
		SELECT COUNT(*) FROM file_parts WHERE channel_id=? AND upload_uuid=?
	`, testChan, uploadUUID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("hard delete retained %d local part pointers for %q", count, uploadUUID)
	}
}

func assertHardDeleteIntentCount(t *testing.T, db *sql.DB, opID string, want int) {
	t.Helper()
	var count int
	if err := db.QueryRow(`
		SELECT COUNT(*) FROM hard_delete_intents WHERE channel_id=? AND op_id=?
	`, testChan, opID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != want {
		t.Fatalf("hard-delete intent count=%d, want %d", count, want)
	}
}
