package projection

import (
	"database/sql"
	"errors"
	"testing"
)

func TestApplyFileCommitCreatesStableLogicalRevision(t *testing.T) {
	db := newTestDB(t)
	op := Op{
		Type:            OpFileCommit,
		ProtocolVersion: 1,
		OpID:            "op-create-1",
		Parent:          RootParent,
		Name:            "Report.txt",
		ContentMsgID:    9001,
		ContentHash:     "sha256:create",
		FileSize:        42,
		FileUploadTime:  100,
	}
	if err := runOp(t, db, testChan, 101, op); err != nil {
		t.Fatalf("commit file: %v", err)
	}

	var (
		contentMsgID int64
		revision     int64
		hash         string
	)
	if err := db.QueryRow(`
		SELECT content_msg_id, revision, content_hash
		FROM files WHERE channel_id=? AND msg_id=?
	`, testChan, 101).Scan(&contentMsgID, &revision, &hash); err != nil {
		t.Fatalf("read committed file: %v", err)
	}
	if contentMsgID != 9001 || revision != 1 || hash != "sha256:create" {
		t.Fatalf("committed file = (%d, %d, %q)", contentMsgID, revision, hash)
	}

	var revisions int
	if err := db.QueryRow(`
		SELECT COUNT(*) FROM file_revisions
		WHERE channel_id=? AND file_msg_id=? AND revision=1
	`, testChan, 101).Scan(&revisions); err != nil {
		t.Fatalf("count revisions: %v", err)
	}
	if revisions != 1 {
		t.Fatalf("revision rows = %d, want 1", revisions)
	}

	_, listed, err := ListFolderContents(db, testChan, RootParent)
	if err != nil {
		t.Fatal(err)
	}
	if len(listed) != 1 || listed[0].MsgID != 101 || listed[0].ContentMsgID != 9001 ||
		listed[0].Revision != 1 || listed[0].ContentHash != "sha256:create" {
		t.Fatalf("listed committed file = %+v", listed)
	}
	byID, ok, err := FileByID(db, testChan, 101)
	if err != nil || !ok {
		t.Fatalf("FileByID: ok=%v err=%v", ok, err)
	}
	if byID.MsgID != 101 || byID.ContentMsgID != 9001 || byID.Revision != 1 {
		t.Fatalf("FileByID = %+v", byID)
	}
}

func TestApplyFolderCommitIsStrictAndIdempotent(t *testing.T) {
	db := newTestDB(t)
	op := Op{
		Type: OpFolderCommit, ProtocolVersion: 1, OpID: "mkdir-versioned",
		Obj: "d:docs", Parent: RootParent, Name: "Docs",
	}
	mustOp(t, db, 10, op)
	mustOp(t, db, 11, op)

	entry, ok, err := DirentByID(db, testChan, "d:docs")
	if err != nil || !ok {
		t.Fatalf("DirentByID: ok=%v err=%v", ok, err)
	}
	if entry.ObjectKind != ObjectKindFolder || entry.DisplayName != "Docs" || entry.Revision != 1 {
		t.Fatalf("folder dirent = %+v", entry)
	}
	var folders int
	if err := db.QueryRow(`SELECT COUNT(*) FROM folders WHERE channel_id=? AND id='d:docs'`, testChan).Scan(&folders); err != nil {
		t.Fatal(err)
	}
	if folders != 1 {
		t.Fatalf("folders=%d, want 1", folders)
	}
}

func TestApplyFileCommitSupportsOneHiddenPart(t *testing.T) {
	db := newTestDB(t)
	mustOp(t, db, 90, Op{Type: OpFilePart, UploadUUID: "hidden-single", PartIndex: 0, FileSize: 12})
	mustOp(t, db, 101, Op{
		Type: OpFileCommit, ProtocolVersion: 1, OpID: "one-hidden-part",
		Name: "hidden.txt", UploadUUID: "hidden-single", PartCount: 1, FileSize: 12,
	})

	file, ok, err := FileByID(db, testChan, 101)
	if err != nil || !ok {
		t.Fatalf("FileByID: ok=%v err=%v", ok, err)
	}
	if file.ContentMsgID != 0 || file.UploadUUID != "hidden-single" || file.PartCount != 1 {
		t.Fatalf("hidden-part content reference = %+v", file)
	}
	parts, err := MultipartParts(db, testChan, 101)
	if err != nil {
		t.Fatal(err)
	}
	if len(parts) != 1 || parts[0].MsgID != 90 {
		t.Fatalf("hidden parts = %+v", parts)
	}
}

func TestApplyFileReplaceKeepsLogicalIdentityAndUsesCAS(t *testing.T) {
	db := newTestDB(t)
	mustOp(t, db, 101, Op{
		Type: OpFileCommit, ProtocolVersion: 1, OpID: "op-create",
		Name: "Report.txt", ContentMsgID: 9001, ContentHash: "old", FileSize: 10,
	})
	mustOp(t, db, 102, Op{
		Type: OpFileReplace, ProtocolVersion: 1, OpID: "op-replace",
		Obj: "f:101", ExpectedRevision: 1,
		ContentMsgID: 9002, ContentHash: "new", FileSize: 20, RetainedUntil: 500,
	})

	var contentMsgID, revision, size int64
	if err := db.QueryRow(`
		SELECT content_msg_id, revision, size FROM files
		WHERE channel_id=? AND msg_id=101
	`, testChan).Scan(&contentMsgID, &revision, &size); err != nil {
		t.Fatalf("read replacement: %v", err)
	}
	if contentMsgID != 9002 || revision != 2 || size != 20 {
		t.Fatalf("replacement = (%d, %d, %d)", contentMsgID, revision, size)
	}

	err := runOp(t, db, testChan, 103, Op{
		Type: OpFileReplace, ProtocolVersion: 1, OpID: "op-stale",
		Obj: "f:101", ExpectedRevision: 1,
		ContentMsgID: 9003, ContentHash: "stale", FileSize: 30, RetainedUntil: 600,
	})
	if !errors.Is(err, ErrRevisionConflict) {
		t.Fatalf("stale replace error = %v, want ErrRevisionConflict", err)
	}

	if err := db.QueryRow(`SELECT content_msg_id, revision FROM files WHERE channel_id=? AND msg_id=101`, testChan).
		Scan(&contentMsgID, &revision); err != nil {
		t.Fatalf("read after stale replace: %v", err)
	}
	if contentMsgID != 9002 || revision != 2 {
		t.Fatalf("stale replace mutated file to (%d, %d)", contentMsgID, revision)
	}
}

func TestApplyOpIDIsIdempotentAcrossTelegramMessages(t *testing.T) {
	db := newTestDB(t)
	mustOp(t, db, 101, Op{
		Type: OpFileCommit, ProtocolVersion: 1, OpID: "same-operation",
		Name: "first.txt", ContentMsgID: 9001,
	})
	// A retry may land as another Telegram message. The durable operation id,
	// not the Telegram message id, is the idempotency boundary.
	mustOp(t, db, 102, Op{
		Type: OpFileCommit, ProtocolVersion: 1, OpID: "same-operation",
		Name: "duplicate.txt", ContentMsgID: 9002,
	})

	var files, operations int
	if err := db.QueryRow(`SELECT COUNT(*) FROM files WHERE channel_id=?`, testChan).Scan(&files); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM projection_operations WHERE channel_id=?`, testChan).Scan(&operations); err != nil {
		t.Fatal(err)
	}
	if files != 1 || operations != 1 {
		t.Fatalf("files=%d operations=%d, want 1/1", files, operations)
	}
}

func TestApplyRelocateAtomicallyRenamesAndMoves(t *testing.T) {
	db := newTestDB(t)
	mustOp(t, db, 1, Op{Type: OpMkdir, Obj: "d:dest", Parent: RootParent, Name: "Dest"})
	mustOp(t, db, 101, Op{
		Type: OpFileCommit, ProtocolVersion: 1, OpID: "op-create",
		Name: "old.txt", ContentMsgID: 9001,
	})
	mustOp(t, db, 102, Op{
		Type: OpRelocate, ProtocolVersion: 1, OpID: "op-relocate",
		Obj: "f:101", Parent: "d:dest", Name: "new.txt", ExpectedRevision: 1,
	})

	var name, parent string
	var revision int64
	if err := db.QueryRow(`
		SELECT name, parent_id, revision FROM files WHERE channel_id=? AND msg_id=101
	`, testChan).Scan(&name, &parent, &revision); err != nil {
		t.Fatal(err)
	}
	if name != "new.txt" || parent != "d:dest" || revision != 2 {
		t.Fatalf("relocated = (%q, %q, %d)", name, parent, revision)
	}
	var activeBodyRevision int64
	if err := db.QueryRow(`
		SELECT revision FROM file_revisions
		WHERE channel_id=? AND file_msg_id=101 AND retained_until=0
	`, testChan).Scan(&activeBodyRevision); err != nil {
		t.Fatal(err)
	}
	if activeBodyRevision != revision {
		t.Fatalf("active body revision=%d, object revision=%d", activeBodyRevision, revision)
	}
}

func TestApplyRelocateOverwriteTombstonesExactDestination(t *testing.T) {
	db := newTestDB(t)
	mustOp(t, db, 101, Op{
		Type: OpFileCommit, ProtocolVersion: 1, OpID: "source",
		Name: "temporary.tmp", ContentMsgID: 9001,
	})
	mustOp(t, db, 102, Op{
		Type: OpFileCommit, ProtocolVersion: 1, OpID: "destination",
		Name: "document.txt", ContentMsgID: 9002,
	})
	mustOp(t, db, 103, Op{
		Type: OpRelocate, ProtocolVersion: 1, OpID: "safe-save",
		Obj: "f:101", Parent: RootParent, Name: "document.txt", ExpectedRevision: 1,
		Overwrite: true, DestinationObj: "f:102", ExpectedDestinationRevision: 1,
		DeletedAt: 777, PurgeAfter: 999,
	})

	var sourceName string
	if err := db.QueryRow(`SELECT name FROM files WHERE channel_id=? AND msg_id=101`, testChan).Scan(&sourceName); err != nil {
		t.Fatal(err)
	}
	if sourceName != "document.txt" {
		t.Fatalf("source name = %q", sourceName)
	}
	var tombstoned int
	if err := db.QueryRow(`SELECT tombstoned FROM files WHERE channel_id=? AND msg_id=102`, testChan).Scan(&tombstoned); err != nil {
		t.Fatal(err)
	}
	if tombstoned != 1 {
		t.Fatalf("destination tombstoned = %d", tombstoned)
	}
	var deletedAt int64
	if err := db.QueryRow(`SELECT deleted_at FROM trash_entries WHERE channel_id=? AND object_id='f:102'`, testChan).Scan(&deletedAt); err != nil {
		t.Fatal(err)
	}
	if deletedAt != 777 {
		t.Fatalf("deleted_at = %d", deletedAt)
	}
}

func TestApplyRelocateConflictDoesNotPartiallyMutate(t *testing.T) {
	db := newTestDB(t)
	mustOp(t, db, 101, Op{
		Type: OpFileCommit, ProtocolVersion: 1, OpID: "one",
		Name: "one.txt", ContentMsgID: 9001,
	})
	mustOp(t, db, 102, Op{
		Type: OpFileCommit, ProtocolVersion: 1, OpID: "two",
		Name: "two.txt", ContentMsgID: 9002,
	})

	err := runOp(t, db, testChan, 103, Op{
		Type: OpRelocate, ProtocolVersion: 1, OpID: "conflict",
		Obj: "f:101", Parent: RootParent, Name: "two.txt", ExpectedRevision: 1,
	})
	if !errors.Is(err, ErrNameConflict) {
		t.Fatalf("error = %v, want ErrNameConflict", err)
	}
	var name string
	if err := db.QueryRow(`SELECT name FROM files WHERE channel_id=? AND msg_id=101`, testChan).Scan(&name); err != nil {
		t.Fatal(err)
	}
	if name != "one.txt" {
		t.Fatalf("source partially renamed to %q", name)
	}
}

func TestApplyTrashTreeHidesDescendantsAndRecordsRoot(t *testing.T) {
	db := newTestDB(t)
	mustOp(t, db, 1, Op{Type: OpMkdir, Obj: "d:root", Name: "Folder"})
	mustOp(t, db, 2, Op{Type: OpMkdir, Obj: "d:child", Parent: "d:root", Name: "Child"})
	mustOp(t, db, 101, Op{
		Type: OpFileCommit, ProtocolVersion: 1, OpID: "inside",
		Parent: "d:child", Name: "inside.txt", ContentMsgID: 9001,
	})
	mustOp(t, db, 102, Op{
		Type: OpTrashTree, ProtocolVersion: 1, OpID: "trash-tree",
		Obj: "d:root", ExpectedRevision: 1, DeletedAt: 1234, PurgeAfter: 5678,
	})

	for table, predicate := range map[string]string{
		"folders": `id IN ('d:root','d:child')`,
		"files":   `msg_id=101`,
	} {
		var live int
		q := `SELECT COUNT(*) FROM ` + table + ` WHERE channel_id=? AND tombstoned=0 AND ` + predicate
		if err := db.QueryRow(q, testChan).Scan(&live); err != nil {
			t.Fatal(err)
		}
		if live != 0 {
			t.Fatalf("%s still has %d live descendants", table, live)
		}
	}

	var parent, name string
	var purgeAfter int64
	if err := db.QueryRow(`
		SELECT original_parent_id, original_name, purge_after FROM trash_entries
		WHERE channel_id=? AND object_id='d:root'
	`, testChan).Scan(&parent, &name, &purgeAfter); err != nil {
		t.Fatal(err)
	}
	if parent != RootParent || name != "Folder" || purgeAfter != 5678 {
		t.Fatalf("trash metadata = (%q, %q, %d)", parent, name, purgeAfter)
	}

	var objectRevision, activeBodyRevision int64
	if err := db.QueryRow(`
		SELECT revision FROM files
		WHERE channel_id=? AND msg_id=101
	`, testChan).Scan(&objectRevision); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`
		SELECT revision FROM file_revisions
		WHERE channel_id=? AND file_msg_id=101 AND retained_until=0
	`, testChan).Scan(&activeBodyRevision); err != nil {
		t.Fatal(err)
	}
	if activeBodyRevision != objectRevision {
		t.Fatalf("trashed descendant active body revision=%d, object revision=%d", activeBodyRevision, objectRevision)
	}
}

func TestReplacedRevisionRetentionAndPruningKeepsLatestFive(t *testing.T) {
	db := newTestDB(t)
	mustOp(t, db, 101, Op{
		Type: OpFileCommit, ProtocolVersion: 1, OpID: "retention-create",
		Name: "history.txt", ContentMsgID: 9001,
	})
	for revision := int64(1); revision <= 6; revision++ {
		mustOp(t, db, 101+revision, Op{
			Type: OpFileReplace, ProtocolVersion: 1, OpID: "retention-" + string(rune('a'+revision)),
			Obj: "f:101", ExpectedRevision: revision,
			ContentMsgID: 9001 + revision, RetainedUntil: 100,
		})
	}

	refs, err := ListPrunableFileRevisions(db, 101, 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(refs) != 2 || refs[0].Revision != 1 || refs[1].Revision != 2 {
		t.Fatalf("prunable revisions = %+v, want revisions 1 and 2", refs)
	}
	deleted, err := DeletePrunableFileRevision(db, refs[0], 101)
	if err != nil || !deleted {
		t.Fatalf("delete prunable = %v, %v", deleted, err)
	}
	if _, err := db.Exec(`UPDATE file_revisions SET retained_until=100 WHERE channel_id=? AND file_msg_id=101 AND revision=7`, testChan); err != nil {
		t.Fatal(err)
	}
	current := FileRevisionRef{ChannelID: testChan, FileMsgID: 101, Revision: 7}
	if deleted, err := DeletePrunableFileRevision(db, current, 101); err != nil || deleted {
		t.Fatalf("deleted current revision: deleted=%v err=%v", deleted, err)
	}
}

func TestPortableNamespaceRejectsCaseAndUnicodeEquivalentSiblings(t *testing.T) {
	db := newTestDB(t)
	mustOp(t, db, 101, Op{
		Type: OpFileCommit, ProtocolVersion: 1, OpID: "first",
		Name: "Résumé.txt", ContentMsgID: 9001,
	})

	for i, name := range []string{"RÉSUMÉ.TXT", "Résumé.txt"} {
		err := runOp(t, db, testChan, int64(102+i), Op{
			Type: OpFileCommit, ProtocolVersion: 1, OpID: "collision-" + name,
			Name: name, ContentMsgID: int64(9002 + i),
		})
		if !errors.Is(err, ErrNameConflict) {
			t.Fatalf("name %q error = %v, want ErrNameConflict", name, err)
		}
	}
}

func TestLiveDirentLookupUsesCanonicalPortableKey(t *testing.T) {
	db := newTestDB(t)
	mustOp(t, db, 101, Op{
		Type: OpFileCommit, ProtocolVersion: 1, OpID: "dirent-lookup",
		Name: "Résumé.txt", ContentMsgID: 9001,
	})

	byID, ok, err := DirentByID(db, testChan, "f:101")
	if err != nil || !ok {
		t.Fatalf("DirentByID: ok=%v err=%v", ok, err)
	}
	if byID.Revision != 1 || byID.DisplayName != "Résumé.txt" || byID.ObjectKind != "file" {
		t.Fatalf("dirent by id = %+v", byID)
	}
	byName, ok, err := LiveDirentByName(db, testChan, RootParent, "RÉSUMÉ.TXT")
	if err != nil || !ok {
		t.Fatalf("LiveDirentByName: ok=%v err=%v", ok, err)
	}
	if byName.ObjectID != "f:101" {
		t.Fatalf("dirent by name = %+v", byName)
	}
}

func TestProjectionOperationLookup(t *testing.T) {
	db := newTestDB(t)
	mustOp(t, db, 101, Op{
		Type: OpFileCommit, ProtocolVersion: 1, OpID: "lookup-me",
		Name: "file.txt", ContentMsgID: 9001,
	})

	got, ok, err := ProjectionOperationByID(db, testChan, "lookup-me")
	if err != nil {
		t.Fatal(err)
	}
	if !ok || got.OpID != "lookup-me" || got.MsgID != 101 || got.Outcome != OperationApplied {
		t.Fatalf("operation = %+v, ok=%v", got, ok)
	}
	if _, ok, err := ProjectionOperationByID(db, testChan, "missing"); err != nil || ok {
		t.Fatalf("missing operation ok=%v err=%v", ok, err)
	}
}

func TestRejectedOperationIDRemainsIdempotentlyRejected(t *testing.T) {
	db := newTestDB(t)
	create := Op{
		Type: OpFileCommit, ProtocolVersion: 1, OpID: "reject-create",
		Name: "file.txt", ContentMsgID: 9001,
	}
	if _, err := ProjectFromOp(db, testChan, 101, create, 0, Format(create)); err != nil {
		t.Fatal(err)
	}
	rejected := Op{
		Type: OpFileReplace, ProtocolVersion: 1, OpID: "terminal-rejection",
		Obj: "f:101", ExpectedRevision: 99, ContentMsgID: 9002, RetainedUntil: 500,
	}
	if _, err := ProjectFromOp(db, testChan, 102, rejected, 0, Format(rejected)); err != nil {
		t.Fatal(err)
	}
	// A later message cannot reuse the rejected operation id to mutate state,
	// even if its payload would otherwise pass the revision CAS.
	retry := rejected
	retry.ExpectedRevision = 1
	retry.ContentMsgID = 9003
	if _, err := ProjectFromOp(db, testChan, 103, retry, 0, Format(retry)); err != nil {
		t.Fatal(err)
	}

	file, ok, err := FileByID(db, testChan, 101)
	if err != nil || !ok {
		t.Fatalf("FileByID: ok=%v err=%v", ok, err)
	}
	if file.Revision != 1 || file.ContentMsgID != 9001 {
		t.Fatalf("rejected retry mutated file: %+v", file)
	}
	operation, ok, err := ProjectionOperationByID(db, testChan, rejected.OpID)
	if err != nil || !ok || operation.Outcome != OperationRejected {
		t.Fatalf("rejected operation = %+v ok=%v err=%v", operation, ok, err)
	}
}

func TestRebuildReplaysWritableProjectionDeterministically(t *testing.T) {
	db := newTestDB(t)
	ops := []struct {
		msgID int64
		op    Op
	}{
		{101, Op{
			Type: OpFileCommit, ProtocolVersion: 1, OpID: "rebuild-create",
			Name: "old.txt", ContentMsgID: 9001, ContentHash: "one",
		}},
		{102, Op{
			Type: OpFileReplace, ProtocolVersion: 1, OpID: "rebuild-replace",
			Obj: "f:101", ExpectedRevision: 1, ContentMsgID: 9002,
			ContentHash: "two", RetainedUntil: 1000,
		}},
		{103, Op{
			Type: OpRelocate, ProtocolVersion: 1, OpID: "rebuild-relocate",
			Obj: "f:101", Name: "new.txt", ExpectedRevision: 2,
		}},
	}
	for _, item := range ops {
		if _, err := ProjectFromOp(db, testChan, item.msgID, item.op, 0, Format(item.op)); err != nil {
			t.Fatalf("project %d: %v", item.msgID, err)
		}
	}
	if err := RebuildProjection(db, testChan); err != nil {
		t.Fatalf("rebuild: %v", err)
	}
	file, ok, err := FileByID(db, testChan, 101)
	if err != nil || !ok {
		t.Fatalf("FileByID: ok=%v err=%v", ok, err)
	}
	if file.Name != "new.txt" || file.Revision != 3 || file.ContentMsgID != 9002 || file.ContentHash != "two" {
		t.Fatalf("rebuilt file = %+v", file)
	}
	for _, item := range ops {
		operation, ok, err := ProjectionOperationByID(db, testChan, item.op.OpID)
		if err != nil || !ok || operation.Outcome != OperationApplied {
			t.Fatalf("rebuilt operation %q = %+v ok=%v err=%v", item.op.OpID, operation, ok, err)
		}
	}
}

func requireNoRow(t *testing.T, err error) {
	t.Helper()
	if !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("error = %v, want sql.ErrNoRows", err)
	}
}
