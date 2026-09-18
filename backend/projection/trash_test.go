package projection

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

// projectTrashOp publishes one op the way a real client does -- through the
// replay log -- so the same history can be replayed from scratch afterwards.
func projectTrashOp(t *testing.T, db *sql.DB, msgID int64, op Op) {
	t.Helper()
	if _, err := ProjectFromOp(db, testChan, msgID, op, 0, Format(op)); err != nil {
		t.Fatalf("project msg %d: %v", msgID, err)
	}
	if op.OpID == "" {
		return
	}
	if err := ConfirmWritableOperation(db, testChan, op.OpID); err != nil {
		t.Fatalf("op %s msg %d: %v", op.Type, msgID, err)
	}
}

func projectTrashOpRejected(t *testing.T, db *sql.DB, msgID int64, op Op) error {
	t.Helper()
	if _, err := ProjectFromOp(db, testChan, msgID, op, 0, Format(op)); err != nil {
		t.Fatalf("project msg %d: %v", msgID, err)
	}
	err := ConfirmWritableOperation(db, testChan, op.OpID)
	if err == nil {
		t.Fatalf("op %s msg %d applied, want rejection", op.Type, msgID)
	}
	return err
}

// namespaceSnapshot renders every row that replay is required to converge on.
// Comparing two snapshots is the only honest convergence assertion: it fails on
// any column a second replay reconstructs differently, not just the ones a test
// happened to think of.
func namespaceSnapshot(t *testing.T, db *sql.DB) string {
	t.Helper()
	var out strings.Builder
	for _, query := range []string{
		`SELECT 'dirent', object_id, object_kind, parent_id, display_name, revision, tombstoned
		   FROM dirents WHERE channel_id=? ORDER BY object_id`,
		`SELECT 'file', msg_id, name, parent_id, tombstoned, revision, size
		   FROM files WHERE channel_id=? ORDER BY msg_id`,
		`SELECT 'folder', id, name, parent_id, tombstoned, revision, ''
		   FROM folders WHERE channel_id=? ORDER BY id`,
		`SELECT 'trash', object_id, object_kind, original_parent_id, original_name,
		        original_revision, deleted_at || '/' || purge_after
		   FROM trash_entries WHERE channel_id=? ORDER BY object_id`,
		`SELECT 'revision', file_msg_id, revision, content_msg_id, upload_uuid,
		        retained_until, ''
		   FROM file_revisions WHERE channel_id=? ORDER BY file_msg_id, revision`,
	} {
		rows, err := db.Query(query, testChan)
		if err != nil {
			t.Fatalf("snapshot: %v", err)
		}
		for rows.Next() {
			cells := make([]any, 7)
			values := make([]sql.NullString, 7)
			for i := range values {
				cells[i] = &values[i]
			}
			if err := rows.Scan(cells...); err != nil {
				_ = rows.Close()
				t.Fatalf("snapshot scan: %v", err)
			}
			for _, value := range values {
				fmt.Fprintf(&out, "%s|", value.String)
			}
			out.WriteByte('\n')
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			t.Fatalf("snapshot rows: %v", err)
		}
		_ = rows.Close()
	}
	return out.String()
}

func seedTrashedFile(t *testing.T, db *sql.DB) {
	t.Helper()
	projectTrashOp(t, db, 1, Op{
		Type: OpFolderCommit, ProtocolVersion: 1, OpID: "mk-docs",
		Obj: "d:docs", Name: "Docs",
	})
	projectTrashOp(t, db, 101, Op{
		Type: OpFileCommit, ProtocolVersion: 1, OpID: "put-report",
		Parent: "d:docs", Name: "report.txt", ContentMsgID: 9001, FileSize: 12,
	})
	projectTrashOp(t, db, 102, Op{
		Type: OpTrashTree, ProtocolVersion: 1, OpID: "trash-report",
		Obj: "f:101", ExpectedRevision: 1, DeletedAt: 1000, PurgeAfter: 2000,
	})
}

func TestTrashRecordsRestorableEntryAndHidesTheFile(t *testing.T) {
	db := newTestDB(t)
	seedTrashedFile(t, db)

	entry, err := TrashEntryByID(db, testChan, "f:101")
	if err != nil {
		t.Fatalf("TrashEntryByID: %v", err)
	}
	want := TrashEntry{
		ObjectID: "f:101", ObjectKind: ObjectKindFile,
		OriginalParentID: "d:docs", OriginalName: "report.txt",
		OriginalRevision: 1, DeletedAt: 1000, PurgeAfter: 2000,
		OpID: "trash-report", Size: 12,
	}
	if !reflect.DeepEqual(entry, want) {
		t.Fatalf("trash entry\n got: %+v\nwant: %+v", entry, want)
	}
	if _, ok, err := FileByID(db, testChan, 101); err != nil || ok {
		t.Fatalf("trashed file still visible: ok=%v err=%v", ok, err)
	}
	// A trash entry is a promise the bytes are still there; no cleanup work may
	// have been queued against them.
	assertNoHardDeleteCleanupWork(t, db, "trash-report")
}

func TestRestoreReturnsFileToItsOriginalParentAndName(t *testing.T) {
	db := newTestDB(t)
	seedTrashedFile(t, db)

	projectTrashOp(t, db, 103, Op{
		Type: OpRestoreTree, ProtocolVersion: 1, OpID: "restore-report",
		Obj: "f:101", Parent: "d:docs", Name: "report.txt", ExpectedRevision: 2,
	})

	file, ok, err := FileByID(db, testChan, 101)
	if err != nil || !ok {
		t.Fatalf("restored file missing: ok=%v err=%v", ok, err)
	}
	if file.ParentID != "d:docs" || file.Name != "report.txt" {
		t.Fatalf("restored to %s/%s, want d:docs/report.txt", file.ParentID, file.Name)
	}
	// Revision keeps climbing across delete and restore, so a client holding
	// the pre-delete revision cannot compare-and-swap against the new object.
	if file.Revision != 3 {
		t.Fatalf("restored revision = %d, want 3", file.Revision)
	}
	if _, err := TrashEntryByID(db, testChan, "f:101"); !errors.Is(err, ErrTrashEntryNotFound) {
		t.Fatalf("trash entry after restore: %v", err)
	}
}

func TestRestoreRejectsAnOccupiedNameAndAnUnknownEntry(t *testing.T) {
	db := newTestDB(t)
	seedTrashedFile(t, db)
	projectTrashOp(t, db, 110, Op{
		Type: OpFileCommit, ProtocolVersion: 1, OpID: "put-replacement",
		Parent: "d:docs", Name: "report.txt", ContentMsgID: 9002, FileSize: 3,
	})

	err := projectTrashOpRejected(t, db, 111, Op{
		Type: OpRestoreTree, ProtocolVersion: 1, OpID: "restore-onto-name",
		Obj: "f:101", Parent: "d:docs", Name: "report.txt", ExpectedRevision: 2,
	})
	if !strings.Contains(err.Error(), ErrNameConflict.Error()) {
		t.Fatalf("restore over a live sibling: %v, want name conflict", err)
	}
	err = projectTrashOpRejected(t, db, 112, Op{
		Type: OpRestoreTree, ProtocolVersion: 1, OpID: "restore-unknown",
		Obj: "f:110", Parent: "d:docs", Name: "ghost.txt", ExpectedRevision: 1,
	})
	if !strings.Contains(err.Error(), ErrObjectNotFound.Error()) {
		t.Fatalf("restore without a trash entry: %v, want not found", err)
	}
}

func TestRestoreFolderLeavesSeparatelyTrashedMembersTrashed(t *testing.T) {
	db := newTestDB(t)
	projectTrashOp(t, db, 1, Op{
		Type: OpFolderCommit, ProtocolVersion: 1, OpID: "mk-root",
		Obj: "d:root", Name: "Root",
	})
	projectTrashOp(t, db, 2, Op{
		Type: OpFolderCommit, ProtocolVersion: 1, OpID: "mk-child",
		Obj: "d:child", Parent: "d:root", Name: "Child",
	})
	projectTrashOp(t, db, 101, Op{
		Type: OpFileCommit, ProtocolVersion: 1, OpID: "put-keep",
		Parent: "d:child", Name: "keep.txt", ContentMsgID: 9001, FileSize: 4,
	})
	projectTrashOp(t, db, 102, Op{
		Type: OpFileCommit, ProtocolVersion: 1, OpID: "put-gone",
		Parent: "d:child", Name: "gone.txt", ContentMsgID: 9002, FileSize: 4,
	})
	// gone.txt is deleted on its own first, so it owns a trash entry of its
	// own and must survive its parent's delete-and-restore untouched.
	projectTrashOp(t, db, 103, Op{
		Type: OpTrashTree, ProtocolVersion: 1, OpID: "trash-gone",
		Obj: "f:102", ExpectedRevision: 1, DeletedAt: 1000, PurgeAfter: 2000,
	})
	projectTrashOp(t, db, 104, Op{
		Type: OpTrashTree, ProtocolVersion: 1, OpID: "trash-root",
		Obj: "d:root", ExpectedRevision: 1, DeletedAt: 1100, PurgeAfter: 2100,
	})
	projectTrashOp(t, db, 105, Op{
		Type: OpRestoreTree, ProtocolVersion: 1, OpID: "restore-root",
		Obj: "d:root", Parent: RootParent, Name: "Root", ExpectedRevision: 2,
	})

	for _, objectID := range []string{"d:root", "d:child", "f:101"} {
		entry, ok, err := DirentByID(db, testChan, objectID)
		if err != nil || !ok || entry.Tombstoned {
			t.Fatalf("%s was not restored: %+v ok=%v err=%v", objectID, entry, ok, err)
		}
	}
	entry, ok, err := DirentByID(db, testChan, "f:102")
	if err != nil || !ok || !entry.Tombstoned {
		t.Fatalf("separately trashed file was resurrected: %+v ok=%v err=%v", entry, ok, err)
	}
	if _, err := TrashEntryByID(db, testChan, "f:102"); err != nil {
		t.Fatalf("separately trashed file lost its entry: %v", err)
	}
}

func TestTrashAndRestoreReplayConverges(t *testing.T) {
	db := newTestDB(t)
	projectTrashOp(t, db, 1, Op{
		Type: OpFolderCommit, ProtocolVersion: 1, OpID: "mk-root",
		Obj: "d:root", Name: "Root",
	})
	projectTrashOp(t, db, 101, Op{
		Type: OpFileCommit, ProtocolVersion: 1, OpID: "put-a",
		Parent: "d:root", Name: "a.txt", ContentMsgID: 9001, FileSize: 4,
	})
	projectTrashOp(t, db, 102, Op{
		Type: OpTrashTree, ProtocolVersion: 1, OpID: "trash-root",
		Obj: "d:root", ExpectedRevision: 1, DeletedAt: 1000, PurgeAfter: 2000,
	})
	projectTrashOp(t, db, 103, Op{
		Type: OpRestoreTree, ProtocolVersion: 1, OpID: "restore-root",
		Obj: "d:root", Parent: RootParent, Name: "Root Recovered", ExpectedRevision: 2,
	})
	projectTrashOp(t, db, 104, Op{
		Type: OpTrashTree, ProtocolVersion: 1, OpID: "trash-file",
		Obj: "f:101", ExpectedRevision: 3, DeletedAt: 1200, PurgeAfter: 2200,
	})

	live := namespaceSnapshot(t, db)
	if err := RebuildProjection(db, testChan); err != nil {
		t.Fatalf("rebuild: %v", err)
	}
	if replayed := namespaceSnapshot(t, db); replayed != live {
		t.Fatalf("replay diverged\n live:\n%s\nreplayed:\n%s", live, replayed)
	}
}

func TestExpiredPurgeIntentRefusesBeforePurgeAfter(t *testing.T) {
	db := newTestDB(t)
	seedTrashedFile(t, db)
	ctx := context.Background()
	opID := DeterministicOpID("purge", "f:101", 1)

	if _, err := RegisterExpiredTrashPurgeIntent(ctx, db, testChan, opID, "f:101", 1999); !errors.Is(err, ErrTrashEntryNotExpired) {
		t.Fatalf("purge one second early: %v, want %v", err, ErrTrashEntryNotExpired)
	}
	assertHardDeleteIntentCount(t, db, opID, 0)

	revision, err := RegisterExpiredTrashPurgeIntent(ctx, db, testChan, opID, "f:101", 2000)
	if err != nil {
		t.Fatalf("purge at purge_after: %v", err)
	}
	if revision != 2 {
		t.Fatalf("purge revision = %d, want the trashed object's 2", revision)
	}
	// The same deterministic id resumes rather than claiming a second time.
	again, err := RegisterExpiredTrashPurgeIntent(ctx, db, testChan, opID, "f:101", 2000)
	if err != nil || again != revision {
		t.Fatalf("resume purge intent = (%d, %v), want (%d, nil)", again, err, revision)
	}
}

func TestPurgeForeclosesRestoreAndPlansTheBodies(t *testing.T) {
	db := newTestDB(t)
	seedTrashedFile(t, db)
	ctx := context.Background()
	opID := DeterministicOpID("purge", "f:101", 1)

	revision, err := RegisterTrashPurgeIntent(ctx, db, testChan, opID, "f:101")
	if err != nil {
		t.Fatalf("register purge intent: %v", err)
	}
	projectTrashOp(t, db, 120, HardDeleteOp(opID, "f:101", revision))

	assertHardDeletePlan(t, db, opID, []int64{9001}, 1, true)
	if _, err := TrashEntryByID(db, testChan, "f:101"); !errors.Is(err, ErrTrashEntryNotFound) {
		t.Fatalf("purge left a restorable entry: %v", err)
	}
	// Without an entry the object can never be restored again, which is what
	// makes the physical delete safe to run afterwards.
	err = projectTrashOpRejected(t, db, 121, Op{
		Type: OpRestoreTree, ProtocolVersion: 1, OpID: "restore-purged",
		Obj: "f:101", Parent: "d:docs", Name: "report.txt", ExpectedRevision: 2,
	})
	if !strings.Contains(err.Error(), ErrObjectNotFound.Error()) {
		t.Fatalf("restore after purge: %v, want not found", err)
	}
}

func TestRestoreWireRoundTrip(t *testing.T) {
	op := Op{
		Type: OpRestoreTree, ProtocolVersion: 1, OpID: "restore-1",
		Obj: "d:docs", Parent: "d:root", Name: "Docs & more", ExpectedRevision: 4,
	}
	wire := Format(op)
	got, err := Parse(wire)
	if err != nil {
		t.Fatalf("Parse(%q): %v", wire, err)
	}
	if !reflect.DeepEqual(got, op) {
		t.Fatalf("round trip\n got: %#v\nwant: %#v\nwire: %s", got, op, wire)
	}
}
