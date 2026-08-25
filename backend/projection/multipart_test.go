package projection

import (
	"context"
	"database/sql"
	"errors"
	"testing"
)

// applyMultipartUpload applies N part ops then the manifest, the way an upload
// emits them: parts first (ascending msg_ids), manifest last (its msg_id becomes
// the file id). Each part is 100 stored bytes.
func applyMultipartUpload(t *testing.T, db *sql.DB, uuid string, partMsgIDs []int64, manifestMsgID int64, name, parent string, encrypted bool) {
	t.Helper()
	for i, msgID := range partMsgIDs {
		op := Op{Type: OpFilePart, UploadUUID: uuid, PartIndex: i, FileSize: 100}
		if err := runOp(t, db, testChan, msgID, op); err != nil {
			t.Fatalf("apply part %d: %v", i, err)
		}
	}
	manifest := Op{
		Type:       OpFileManifest,
		UploadUUID: uuid,
		Parent:     parent,
		Name:       name,
		FileSize:   int64(len(partMsgIDs)) * 100,
		PartCount:  len(partMsgIDs),
	}
	if encrypted {
		manifest.Encrypted = true
		manifest.PlaintextSize = int64(len(partMsgIDs))*100 - 66
		manifest.EncryptionVersion = 1
	}
	if err := runOp(t, db, testChan, manifestMsgID, manifest); err != nil {
		t.Fatalf("apply manifest: %v", err)
	}
}

func countLiveFilesP(t *testing.T, db *sql.DB) int {
	t.Helper()
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM files WHERE channel_id = ? AND tombstoned = 0`, testChan).Scan(&n); err != nil {
		t.Fatalf("count files: %v", err)
	}
	return n
}

func TestManifestCommitsOneFileFromParts(t *testing.T) {
	db := newTestDB(t)
	applyMultipartUpload(t, db, "u1", []int64{1, 2, 3}, 4, "movie.mkv", RootParent, false)

	// Exactly one visible file: the manifest. Parts are not files.
	if n := countLiveFilesP(t, db); n != 1 {
		t.Fatalf("files = %d, want 1 (the manifest only)", n)
	}
	var (
		uuid      string
		partCount int
		size      int64
	)
	if err := db.QueryRow(`SELECT upload_uuid, part_count, size FROM files WHERE channel_id = ? AND msg_id = 4`, testChan).
		Scan(&uuid, &partCount, &size); err != nil {
		t.Fatalf("read manifest file: %v", err)
	}
	if uuid != "u1" || partCount != 3 || size != 300 {
		t.Fatalf("manifest file: uuid=%q partCount=%d size=%d, want u1,3,300", uuid, partCount, size)
	}

	parts, err := MultipartParts(db, testChan, 4)
	if err != nil {
		t.Fatalf("MultipartParts: %v", err)
	}
	if len(parts) != 3 {
		t.Fatalf("parts = %d, want 3", len(parts))
	}
	for i, p := range parts {
		if p.PartIndex != i || p.MsgID != int64(i+1) {
			t.Errorf("part %d = %+v, want index %d msg %d", i, p, i, i+1)
		}
	}

	// A normal single file (no upload_uuid) returns no parts.
	if err := runOp(t, db, testChan, 10, Op{Type: OpFileUpload, Parent: RootParent, Name: "single.txt", FileSize: 5}); err != nil {
		t.Fatalf("single upload: %v", err)
	}
	if parts, err := MultipartParts(db, testChan, 10); err != nil || parts != nil {
		t.Fatalf("single file MultipartParts = %v, %v, want nil,nil", parts, err)
	}
}

func TestMultipartReadContextsHonorCancellation(t *testing.T) {
	db := newTestDB(t)
	applyMultipartUpload(t, db, "u1", []int64{1, 2, 3}, 4, "movie.mkv", RootParent, false)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	parts, err := MultipartPartsContext(ctx, db, testChan, 4)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("MultipartPartsContext error = %v, want context.Canceled", err)
	}
	if parts != nil {
		t.Fatalf("MultipartPartsContext parts = %v, want nil", parts)
	}

	parts, err = PartsForUUIDContext(ctx, db, testChan, "u1")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("PartsForUUIDContext error = %v, want context.Canceled", err)
	}
	if parts != nil {
		t.Fatalf("PartsForUUIDContext parts = %v, want nil", parts)
	}

	err = MultipartCompleteContext(ctx, db, testChan, 4, []FilePart{
		{PartIndex: 0, MsgID: 1, Size: 100},
		{PartIndex: 1, MsgID: 2, Size: 100},
		{PartIndex: 2, MsgID: 3, Size: 100},
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("MultipartCompleteContext error = %v, want context.Canceled", err)
	}
}

func TestMultipartReadContextsRejectNilContext(t *testing.T) {
	db := newTestDB(t)
	applyMultipartUpload(t, db, "u1", []int64{1, 2, 3}, 4, "movie.mkv", RootParent, false)

	parts, err := MultipartPartsContext(nil, db, testChan, 4)
	if !errors.Is(err, ErrInvalidContext) {
		t.Fatalf("MultipartPartsContext error = %v, want ErrInvalidContext", err)
	}
	if parts != nil {
		t.Fatalf("MultipartPartsContext parts = %v, want nil", parts)
	}

	parts, err = PartsForUUIDContext(nil, db, testChan, "u1")
	if !errors.Is(err, ErrInvalidContext) {
		t.Fatalf("PartsForUUIDContext error = %v, want ErrInvalidContext", err)
	}
	if parts != nil {
		t.Fatalf("PartsForUUIDContext parts = %v, want nil", parts)
	}

	err = MultipartCompleteContext(nil, db, testChan, 4, nil)
	if !errors.Is(err, ErrInvalidContext) {
		t.Fatalf("MultipartCompleteContext error = %v, want ErrInvalidContext", err)
	}
}

func TestPartsNeverSurfaceAsFilesOrOrphans(t *testing.T) {
	db := newTestDB(t)
	// Parts only, no manifest — a failed or in-flight upload.
	for i, msgID := range []int64{1, 2, 3} {
		op := Op{Type: OpFilePart, UploadUUID: "u1", PartIndex: i, FileSize: 100}
		if err := runOp(t, db, testChan, msgID, op); err != nil {
			t.Fatalf("apply part: %v", err)
		}
	}
	if n := countLiveFilesP(t, db); n != 0 {
		t.Fatalf("files = %d, want 0 (parts are not files)", n)
	}
	// Parts never appear in a folder listing.
	_, files, err := ListFolderContents(db, testChan, RootParent)
	if err != nil {
		t.Fatalf("ListFolderContents: %v", err)
	}
	if len(files) != 0 {
		t.Fatalf("root listing shows %d files, want 0", len(files))
	}
	// Orphan detection (which queries files) must not surface parts.
	orphans, err := OrphanedFiles(db, testChan)
	if err != nil {
		t.Fatalf("OrphanedFiles: %v", err)
	}
	if len(orphans) != 0 {
		t.Fatalf("orphans = %d, want 0 (parts must never surface)", len(orphans))
	}
	// Parts with no manifest are NOT swept: they could belong to an upload still
	// in flight (another instance/user on a shared drive) whose manifest hasn't
	// landed yet. The sweep only acts on parts behind a tombstone.
	ids, err := OrphanPartMessages(db, testChan)
	if err != nil {
		t.Fatalf("OrphanPartMessages: %v", err)
	}
	if len(ids) != 0 {
		t.Fatalf("orphan parts (no manifest) = %v, want 0 (in-flight protection)", ids)
	}
}

func TestCommittedPartsAreNotOrphans(t *testing.T) {
	db := newTestDB(t)
	applyMultipartUpload(t, db, "u1", []int64{1, 2, 3}, 4, "a.bin", RootParent, false)
	ids, err := OrphanPartMessages(db, testChan)
	if err != nil {
		t.Fatalf("OrphanPartMessages: %v", err)
	}
	if len(ids) != 0 {
		t.Fatalf("orphan parts = %v, want 0 (manifest committed)", ids)
	}
}

func TestTombstonedMultipartPartsBecomeOrphansThenGCd(t *testing.T) {
	db := newTestDB(t)
	applyMultipartUpload(t, db, "u1", []int64{1, 2, 3}, 4, "a.bin", RootParent, false)

	// Delete the file by tombing the manifest.
	if err := runOp(t, db, testChan, 5, Op{Type: OpTomb, Obj: "f:4"}); err != nil {
		t.Fatalf("tomb: %v", err)
	}
	ids, err := OrphanPartMessages(db, testChan)
	if err != nil {
		t.Fatalf("OrphanPartMessages: %v", err)
	}
	if len(ids) != 3 {
		t.Fatalf("orphan parts after delete = %v, want 3", ids)
	}

	if err := DeleteFilePartsByMsgIDs(db, testChan, ids); err != nil {
		t.Fatalf("DeleteFilePartsByMsgIDs: %v", err)
	}
	left, err := OrphanPartMessages(db, testChan)
	if err != nil {
		t.Fatalf("OrphanPartMessages after GC: %v", err)
	}
	if len(left) != 0 {
		t.Fatalf("orphan parts after GC = %v, want 0", left)
	}
}

func TestMultipartWireRoundTrip(t *testing.T) {
	part := Op{Type: OpFilePart, UploadUUID: "abc-123", PartIndex: 5, FileSize: 2048}
	got, err := Parse(Format(part))
	if err != nil {
		t.Fatalf("parse part: %v", err)
	}
	if got.Type != OpFilePart || got.UploadUUID != "abc-123" || got.PartIndex != 5 || got.FileSize != 2048 {
		t.Fatalf("part round-trip = %+v", got)
	}

	man := Op{
		Type: OpFileManifest, UploadUUID: "abc-123", Parent: "d:folder",
		Name: "My File.mkv", FileSize: 5000, PlaintextSize: 4096, PartCount: 3,
		Encrypted: true, EncryptionVersion: 1, FileUploadTime: 1700,
	}
	got, err = Parse(Format(man))
	if err != nil {
		t.Fatalf("parse manifest: %v", err)
	}
	if got.Type != OpFileManifest || got.UploadUUID != "abc-123" || got.Parent != "d:folder" ||
		got.Name != "My File.mkv" || got.PartCount != 3 || got.FileSize != 5000 ||
		got.PlaintextSize != 4096 || !got.Encrypted {
		t.Fatalf("manifest round-trip = %+v", got)
	}
}

func TestMultipartCompleteValidatesParts(t *testing.T) {
	db := newTestDB(t)
	applyMultipartUpload(t, db, "u1", []int64{1, 2, 3}, 4, "movie.mkv", RootParent, false)
	parts, err := MultipartParts(db, testChan, 4)
	if err != nil || len(parts) != 3 {
		t.Fatalf("parts = %+v err %v", parts, err)
	}

	// A complete, contiguous, correctly-sized set passes.
	if err := MultipartComplete(db, testChan, 4, parts); err != nil {
		t.Fatalf("complete set should pass: %v", err)
	}
	// A missing part is rejected.
	if err := MultipartComplete(db, testChan, 4, parts[:2]); err == nil {
		t.Fatal("2 of 3 parts should be rejected")
	}
	// A size mismatch (a truncated part) is rejected.
	short := append([]FilePart(nil), parts...)
	short[1].Size = 1
	if err := MultipartComplete(db, testChan, 4, short); err == nil {
		t.Fatal("size mismatch should be rejected")
	}
}

func TestPartFirstWriteWins(t *testing.T) {
	db := newTestDB(t)
	if err := runOp(t, db, testChan, 1, Op{Type: OpFilePart, UploadUUID: "u1", PartIndex: 0, FileSize: 100}); err != nil {
		t.Fatalf("part: %v", err)
	}
	// A duplicate part op (same uuid+index) with a different msg_id must not
	// repoint the part, which would strand the original body.
	if err := runOp(t, db, testChan, 999, Op{Type: OpFilePart, UploadUUID: "u1", PartIndex: 0, FileSize: 100}); err != nil {
		t.Fatalf("dup part: %v", err)
	}
	parts, err := PartsForUUID(db, testChan, "u1")
	if err != nil || len(parts) != 1 {
		t.Fatalf("parts = %+v err %v, want 1", parts, err)
	}
	if parts[0].MsgID != 1 {
		t.Fatalf("part msg_id = %d, want 1 (first write wins)", parts[0].MsgID)
	}
}

func TestDeleteChannelClearsFileParts(t *testing.T) {
	db := newTestDB(t)
	applyMultipartUpload(t, db, "u1", []int64{1, 2, 3}, 4, "a.bin", RootParent, false)
	if err := QueuePartCleanup(db, testChan, []int64{9}); err != nil {
		t.Fatalf("queue cleanup: %v", err)
	}
	if parts, _ := PartsForUUID(db, testChan, "u1"); len(parts) != 3 {
		t.Fatalf("setup: want 3 parts")
	}

	if err := DeleteChannel(db, testChan); err != nil {
		t.Fatalf("delete channel: %v", err)
	}

	if parts, _ := PartsForUUID(db, testChan, "u1"); len(parts) != 0 {
		t.Fatalf("file_parts survived channel delete = %d, want 0", len(parts))
	}
	if ids, _ := PendingPartCleanup(db, testChan); len(ids) != 0 {
		t.Fatalf("pending_part_cleanup survived channel delete = %v, want none", ids)
	}
}

func TestPendingPartCleanupRoundtrip(t *testing.T) {
	db := newTestDB(t)
	if err := QueuePartCleanup(db, testChan, []int64{5, 6, 7}); err != nil {
		t.Fatalf("queue: %v", err)
	}
	if err := QueuePartCleanup(db, testChan, []int64{7}); err != nil { // idempotent
		t.Fatalf("queue dup: %v", err)
	}
	ids, err := PendingPartCleanup(db, testChan)
	if err != nil || len(ids) != 3 {
		t.Fatalf("pending = %v err %v, want 3", ids, err)
	}
	if err := ClearPartCleanup(db, testChan, []int64{5, 6}); err != nil {
		t.Fatalf("clear: %v", err)
	}
	if ids, _ := PendingPartCleanup(db, testChan); len(ids) != 1 || ids[0] != 7 {
		t.Fatalf("after clear = %v, want [7]", ids)
	}
}
