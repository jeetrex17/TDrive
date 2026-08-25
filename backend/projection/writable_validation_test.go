package projection

import (
	"errors"
	"strings"
	"testing"
)

func TestFileCommitRejectsIncompleteMultipartContent(t *testing.T) {
	db := newTestDB(t)
	mustOp(t, db, 90, Op{Type: OpFilePart, UploadUUID: "partial", PartIndex: 0, FileSize: 5})

	for _, test := range []struct {
		name string
		op   Op
	}{
		{
			name: "missing second part",
			op: Op{Type: OpFileCommit, ProtocolVersion: 1, OpID: "partial-count",
				Name: "partial.bin", UploadUUID: "partial", PartCount: 2, FileSize: 5},
		},
		{
			name: "stored size mismatch",
			op: Op{Type: OpFileCommit, ProtocolVersion: 1, OpID: "partial-size",
				Name: "partial.bin", UploadUUID: "partial", PartCount: 1, FileSize: 6},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := runOp(t, db, testChan, 101, test.op)
			if !errors.Is(err, ErrContentIncomplete) {
				t.Fatalf("error=%v, want ErrContentIncomplete", err)
			}
		})
	}
	var files int
	if err := db.QueryRow(`SELECT COUNT(*) FROM files WHERE channel_id=?`, testChan).Scan(&files); err != nil {
		t.Fatal(err)
	}
	if files != 0 {
		t.Fatalf("incomplete content exposed %d files", files)
	}
}

func TestFileContentReferenceCannotBeCommittedTwice(t *testing.T) {
	db := newTestDB(t)
	mustOp(t, db, 101, Op{Type: OpFileCommit, ProtocolVersion: 1, OpID: "content-owner", Name: "one", ContentMsgID: 9001})
	err := runOp(t, db, testChan, 102, Op{Type: OpFileCommit, ProtocolVersion: 1, OpID: "content-reuse", Name: "two", ContentMsgID: 9001})
	if !errors.Is(err, ErrContentAlreadyCommitted) {
		t.Fatalf("reused content error=%v", err)
	}
}

func TestWritableCommitValidation(t *testing.T) {
	db := newTestDB(t)
	tests := []struct {
		name string
		op   Op
		want error
	}{
		{"bad version", Op{Type: OpFolderCommit, ProtocolVersion: 2, OpID: "op", Obj: "d:x", Name: "x"}, ErrBadOp},
		{"empty op id", Op{Type: OpFolderCommit, ProtocolVersion: 1, Obj: "d:x", Name: "x"}, ErrBadOp},
		{"control op id", Op{Type: OpFolderCommit, ProtocolVersion: 1, OpID: "bad\n", Obj: "d:x", Name: "x"}, ErrBadOp},
		{"bad folder id", Op{Type: OpFolderCommit, ProtocolVersion: 1, OpID: "op-a", Obj: "x", Name: "x"}, ErrBadOp},
		{"missing parent", Op{Type: OpFolderCommit, ProtocolVersion: 1, OpID: "op-b", Obj: "d:x", Parent: "d:missing", Name: "x"}, ErrObjectNotFound},
		{"file id mismatch", Op{Type: OpFileCommit, ProtocolVersion: 1, OpID: "op-c", Obj: "f:999", Name: "x", ContentMsgID: 3}, ErrBadOp},
		{"no content", Op{Type: OpFileCommit, ProtocolVersion: 1, OpID: "op-d", Name: "x"}, ErrBadOp},
		{"two content forms", Op{Type: OpFileCommit, ProtocolVersion: 1, OpID: "op-e", Name: "x", ContentMsgID: 3, UploadUUID: "u", PartCount: 1}, ErrBadOp},
		{"invalid portable name", Op{Type: OpFileCommit, ProtocolVersion: 1, OpID: "op-f", Name: "CON", ContentMsgID: 3}, ErrBadOp},
		{"invalid parent prefix", Op{Type: OpFileCommit, ProtocolVersion: 1, OpID: "op-g", Parent: "raw", Name: "x", ContentMsgID: 3}, ErrBadOp},
		{"overlong upload uuid", Op{Type: OpFileCommit, ProtocolVersion: 1, OpID: "op-h", Name: "x", UploadUUID: strings.Repeat("u", 129), PartCount: 1}, ErrBadOp},
		{"too many parts", Op{Type: OpFileCommit, ProtocolVersion: 1, OpID: "op-i", Name: "x", UploadUUID: "u", PartCount: 33}, ErrBadOp},
	}
	for index, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := runOp(t, db, testChan, int64(200+index), test.op)
			if !errors.Is(err, test.want) {
				t.Fatalf("error=%v, want %v", err, test.want)
			}
		})
	}
}

func TestFolderCommitRejectsReusedObjectIdentity(t *testing.T) {
	db := newTestDB(t)
	mustOp(t, db, 1, Op{Type: OpFolderCommit, ProtocolVersion: 1, OpID: "folder-first", Obj: "d:x", Name: "X"})
	err := runOp(t, db, testChan, 2, Op{Type: OpFolderCommit, ProtocolVersion: 1, OpID: "folder-second", Obj: "d:x", Name: "Other"})
	if !errors.Is(err, ErrObjectExists) {
		t.Fatalf("reused folder error=%v", err)
	}
}

func TestEncryptedCommitAndReplaceExposeCurrentContent(t *testing.T) {
	db := newTestDB(t)
	mustOp(t, db, 101, Op{
		Type: OpFileCommit, ProtocolVersion: 1, OpID: "encrypted-create",
		Name: "secret.bin", ContentMsgID: 9001, FileSize: 20,
		Encrypted: true, PlaintextSize: 10,
	})
	mustOp(t, db, 102, Op{
		Type: OpFileReplace, ProtocolVersion: 1, OpID: "encrypted-replace",
		Obj: "f:101", ExpectedRevision: 1, ContentMsgID: 9002,
		FileSize: 30, Encrypted: true, PlaintextSize: 15,
		EncryptionVersion: 2, RetainedUntil: 500,
	})
	file, ok, err := FileByID(db, testChan, 101)
	if err != nil || !ok {
		t.Fatalf("FileByID: ok=%v err=%v", ok, err)
	}
	if !file.Encrypted || file.EncryptionVersion != 2 || file.PlaintextSize != 15 || file.ContentMsgID != 9002 {
		t.Fatalf("encrypted replacement = %+v", file)
	}
	var retainedUntil int64
	if err := db.QueryRow(`
		SELECT retained_until FROM file_revisions
		WHERE channel_id=? AND file_msg_id=101 AND revision=1
	`, testChan).Scan(&retainedUntil); err != nil {
		t.Fatal(err)
	}
	if retainedUntil != 500 {
		t.Fatalf("retained_until=%d", retainedUntil)
	}
}

func TestMultipartReplacementUsesCommittedParts(t *testing.T) {
	db := newTestDB(t)
	mustOp(t, db, 101, Op{Type: OpFileCommit, ProtocolVersion: 1, OpID: "multipart-base", Name: "x", ContentMsgID: 1})
	mustOp(t, db, 110, Op{Type: OpFilePart, UploadUUID: "replacement-parts", PartIndex: 0, FileSize: 4})
	mustOp(t, db, 111, Op{Type: OpFilePart, UploadUUID: "replacement-parts", PartIndex: 1, FileSize: 6})
	mustOp(t, db, 112, Op{
		Type: OpFileReplace, ProtocolVersion: 1, OpID: "multipart-replace",
		Obj: "f:101", ExpectedRevision: 1, UploadUUID: "replacement-parts",
		PartCount: 2, FileSize: 10, RetainedUntil: 500,
	})
	file, ok, err := FileByID(db, testChan, 101)
	if err != nil || !ok || file.ContentMsgID != 0 || file.UploadUUID != "replacement-parts" || file.PartCount != 2 {
		t.Fatalf("multipart replacement=%+v ok=%v err=%v", file, ok, err)
	}
}

func TestFileReplaceValidation(t *testing.T) {
	db := newTestDB(t)
	mustOp(t, db, 101, Op{Type: OpFileCommit, ProtocolVersion: 1, OpID: "replace-validation-create", Name: "x", ContentMsgID: 1})
	for index, test := range []struct {
		op   Op
		want error
	}{
		{Op{Type: OpFileReplace, ProtocolVersion: 1, OpID: "no-revision", Obj: "f:101", ContentMsgID: 2, RetainedUntil: 3}, ErrBadOp},
		{Op{Type: OpFileReplace, ProtocolVersion: 1, OpID: "no-retention", Obj: "f:101", ExpectedRevision: 1, ContentMsgID: 2}, ErrBadOp},
		{Op{Type: OpFileReplace, ProtocolVersion: 1, OpID: "missing-file", Obj: "f:999", ExpectedRevision: 1, ContentMsgID: 2, RetainedUntil: 3}, ErrObjectNotFound},
	} {
		err := runOp(t, db, testChan, int64(120+index), test.op)
		if !errors.Is(err, test.want) {
			t.Fatalf("replace %q error=%v, want %v", test.op.OpID, err, test.want)
		}
	}
}

func TestRelocateFolderAndCaseOnlyRename(t *testing.T) {
	db := newTestDB(t)
	mustOp(t, db, 1, Op{Type: OpFolderCommit, ProtocolVersion: 1, OpID: "parent", Obj: "d:parent", Name: "Parent"})
	mustOp(t, db, 2, Op{Type: OpFolderCommit, ProtocolVersion: 1, OpID: "child", Obj: "d:child", Parent: "d:parent", Name: "Child"})
	mustOp(t, db, 3, Op{
		Type: OpRelocate, ProtocolVersion: 1, OpID: "case-rename",
		Obj: "d:child", Parent: "d:parent", Name: "CHILD", ExpectedRevision: 1,
	})
	entry, ok, err := DirentByID(db, testChan, "d:child")
	if err != nil || !ok || entry.DisplayName != "CHILD" || entry.Revision != 2 {
		t.Fatalf("case rename = %+v ok=%v err=%v", entry, ok, err)
	}
	err = runOp(t, db, testChan, 4, Op{
		Type: OpRelocate, ProtocolVersion: 1, OpID: "cycle",
		Obj: "d:parent", Parent: "d:child", Name: "Parent", ExpectedRevision: 1,
	})
	if !errors.Is(err, ErrCycleRejected) {
		t.Fatalf("cycle error=%v", err)
	}
}

func TestRelocateValidationAndDirectoryOverwriteRejection(t *testing.T) {
	db := newTestDB(t)
	mustOp(t, db, 1, Op{Type: OpFolderCommit, ProtocolVersion: 1, OpID: "dir-a", Obj: "d:a", Name: "A"})
	mustOp(t, db, 2, Op{Type: OpFolderCommit, ProtocolVersion: 1, OpID: "dir-b", Obj: "d:b", Name: "B"})
	for index, test := range []struct {
		op   Op
		want error
	}{
		{Op{Type: OpRelocate, ProtocolVersion: 1, OpID: "no-source-revision", Obj: "d:a", Name: "A"}, ErrBadOp},
		{Op{Type: OpRelocate, ProtocolVersion: 1, OpID: "missing-source", Obj: "d:missing", Name: "M", ExpectedRevision: 1}, ErrObjectNotFound},
		{Op{Type: OpRelocate, ProtocolVersion: 1, OpID: "self-parent", Obj: "d:a", Parent: "d:a", Name: "A", ExpectedRevision: 1}, ErrCycleRejected},
		{Op{Type: OpRelocate, ProtocolVersion: 1, OpID: "dir-overwrite", Obj: "d:a", Name: "B", ExpectedRevision: 1, Overwrite: true, DestinationObj: "d:b", ExpectedDestinationRevision: 1, DeletedAt: 1, PurgeAfter: 2}, ErrBadOp},
	} {
		err := runOp(t, db, testChan, int64(20+index), test.op)
		if !errors.Is(err, test.want) {
			t.Fatalf("relocate %q error=%v, want %v", test.op.OpID, err, test.want)
		}
	}
}

func TestRelocateValidatesExactOverwriteDestination(t *testing.T) {
	db := newTestDB(t)
	mustOp(t, db, 101, Op{Type: OpFileCommit, ProtocolVersion: 1, OpID: "src", Name: "src", ContentMsgID: 1})
	mustOp(t, db, 102, Op{Type: OpFileCommit, ProtocolVersion: 1, OpID: "dst", Name: "dst", ContentMsgID: 2})
	for _, op := range []Op{
		{Type: OpRelocate, ProtocolVersion: 1, OpID: "wrong-dst", Obj: "f:101", Name: "dst", ExpectedRevision: 1, Overwrite: true, DestinationObj: "f:999", ExpectedDestinationRevision: 1, DeletedAt: 1, PurgeAfter: 2},
		{Type: OpRelocate, ProtocolVersion: 1, OpID: "missing-dst", Obj: "f:101", Name: "free", ExpectedRevision: 1, Overwrite: true, DestinationObj: "f:102", ExpectedDestinationRevision: 1, DeletedAt: 1, PurgeAfter: 2},
	} {
		err := runOp(t, db, testChan, 103, op)
		if !errors.Is(err, ErrDestinationMismatch) {
			t.Fatalf("op %q error=%v", op.OpID, err)
		}
	}
}

func TestRelocateOverwriteUsesDestinationRevisionCAS(t *testing.T) {
	db := newTestDB(t)
	mustOp(t, db, 101, Op{Type: OpFileCommit, ProtocolVersion: 1, OpID: "cas-src", Name: "src", ContentMsgID: 1})
	mustOp(t, db, 102, Op{Type: OpFileCommit, ProtocolVersion: 1, OpID: "cas-dst", Name: "dst", ContentMsgID: 2})
	mustOp(t, db, 103, Op{
		Type: OpFileReplace, ProtocolVersion: 1, OpID: "cas-dst-change",
		Obj: "f:102", ExpectedRevision: 1, ContentMsgID: 3, RetainedUntil: 100,
	})
	err := runOp(t, db, testChan, 104, Op{
		Type: OpRelocate, ProtocolVersion: 1, OpID: "cas-overwrite",
		Obj: "f:101", Name: "dst", ExpectedRevision: 1,
		Overwrite: true, DestinationObj: "f:102", ExpectedDestinationRevision: 1,
		DeletedAt: 10, PurgeAfter: 20,
	})
	if !errors.Is(err, ErrRevisionConflict) {
		t.Fatalf("stale destination error=%v", err)
	}
	destination, ok, err := FileByID(db, testChan, 102)
	if err != nil || !ok || destination.Revision != 2 || destination.ContentMsgID != 3 {
		t.Fatalf("destination mutated: %+v ok=%v err=%v", destination, ok, err)
	}
}

func TestTrashSingleFileAndValidation(t *testing.T) {
	db := newTestDB(t)
	mustOp(t, db, 101, Op{Type: OpFileCommit, ProtocolVersion: 1, OpID: "trash-file-create", Name: "x", ContentMsgID: 1})
	err := runOp(t, db, testChan, 100, Op{
		Type: OpTrashTree, ProtocolVersion: 1, OpID: "trash-missing",
		Obj: "f:999", ExpectedRevision: 1, DeletedAt: 10, PurgeAfter: 20,
	})
	if !errors.Is(err, ErrObjectNotFound) {
		t.Fatalf("missing trash error=%v", err)
	}
	err = runOp(t, db, testChan, 102, Op{
		Type: OpTrashTree, ProtocolVersion: 1, OpID: "bad-retention",
		Obj: "f:101", ExpectedRevision: 1, DeletedAt: 10, PurgeAfter: 10,
	})
	if !errors.Is(err, ErrBadOp) {
		t.Fatalf("bad retention error=%v", err)
	}
	err = runOp(t, db, testChan, 102, Op{
		Type: OpTrashTree, ProtocolVersion: 1, OpID: "stale-trash",
		Obj: "f:101", ExpectedRevision: 2, DeletedAt: 10, PurgeAfter: 20,
	})
	if !errors.Is(err, ErrRevisionConflict) {
		t.Fatalf("stale trash error=%v", err)
	}
	mustOp(t, db, 103, Op{
		Type: OpTrashTree, ProtocolVersion: 1, OpID: "trash-file",
		Obj: "f:101", ExpectedRevision: 1, DeletedAt: 10, PurgeAfter: 20,
	})
	if _, ok, err := FileByID(db, testChan, 101); err != nil || ok {
		t.Fatalf("trashed FileByID ok=%v err=%v", ok, err)
	}
	entry, ok, err := DirentByID(db, testChan, "f:101")
	if err != nil || !ok || !entry.Tombstoned || entry.Revision != 2 {
		t.Fatalf("trashed dirent=%+v ok=%v err=%v", entry, ok, err)
	}
}

func TestNamespaceAndRetentionAPIBoundaries(t *testing.T) {
	db := newTestDB(t)
	if _, ok, err := DirentByID(db, testChan, "f:999"); err != nil || ok {
		t.Fatalf("missing DirentByID ok=%v err=%v", ok, err)
	}
	if _, _, err := DirentByID(db, testChan, "bad"); err == nil {
		t.Fatal("invalid DirentByID succeeded")
	}
	if _, _, err := LiveDirentByName(db, testChan, RootParent, "CON"); !errors.Is(err, ErrInvalidPortableName) {
		t.Fatalf("invalid name error=%v", err)
	}
	if refs, err := ListPrunableFileRevisions(db, 1, 1); err != nil || len(refs) != 0 {
		t.Fatalf("empty prunable refs=%v err=%v", refs, err)
	}
	for _, input := range []struct {
		now   int64
		limit int
	}{{0, 1}, {1, 0}, {1, 1001}} {
		if _, err := ListPrunableFileRevisions(db, input.now, input.limit); err == nil {
			t.Fatalf("ListPrunableFileRevisions(%d,%d) succeeded", input.now, input.limit)
		}
	}
	if deleted, err := DeletePrunableFileRevision(db, FileRevisionRef{}, 1); err == nil || deleted {
		t.Fatalf("invalid delete deleted=%v err=%v", deleted, err)
	}
	if _, _, err := DirentByID(nil, testChan, "f:1"); err == nil {
		t.Fatal("nil DB DirentByID succeeded")
	}
	if _, _, err := LiveDirentByName(nil, testChan, RootParent, "x"); err == nil {
		t.Fatal("nil DB LiveDirentByName succeeded")
	}
	if _, _, err := LiveDirentByName(db, testChan, "raw", "x"); err == nil {
		t.Fatal("invalid parent LiveDirentByName succeeded")
	}
	if _, err := ListPrunableFileRevisions(nil, 1, 1); err == nil {
		t.Fatal("nil DB ListPrunableFileRevisions succeeded")
	}
	if _, err := DeletePrunableFileRevision(nil, FileRevisionRef{ChannelID: 1, FileMsgID: 1, Revision: 1}, 1); err == nil {
		t.Fatal("nil DB DeletePrunableFileRevision succeeded")
	}
}

func TestCanonicalNameKeyLengthControlAndNormalization(t *testing.T) {
	if _, err := CanonicalNameKey(strings.Repeat("a", maxPortableNameBytes+1)); !errors.Is(err, ErrInvalidPortableName) {
		t.Fatalf("long name error=%v", err)
	}
	if _, err := CanonicalNameKey("bad\x00name"); !errors.Is(err, ErrInvalidPortableName) {
		t.Fatalf("control name error=%v", err)
	}
	first, err := CanonicalNameKey("Å.txt")
	if err != nil {
		t.Fatal(err)
	}
	second, err := CanonicalNameKey("Å.TXT")
	if err != nil || first != second {
		t.Fatalf("normalized keys=%q/%q err=%v", first, second, err)
	}
}
