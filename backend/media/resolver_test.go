package media

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"TDrive/backend/projection"

	_ "modernc.org/sqlite"
)

const testChannelID int64 = 88001

func TestResolverResolveNormalFile(t *testing.T) {
	db := newResolverTestDB(t)
	mustApplyOp(t, db, 10, projection.Op{
		Type:     projection.OpFileUpload,
		Parent:   projection.RootParent,
		Name:     "clip.mp4",
		FileSize: 1234,
	})

	got, err := NewResolver(db).Resolve(context.Background(), testChannelID, 10)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got.ChannelID != testChannelID || got.FileID != 10 || got.Name != "clip.mp4" {
		t.Fatalf("identity = %+v", got)
	}
	if got.Revision != 1 {
		t.Fatalf("revision = %d, want 1", got.Revision)
	}
	if got.StoredSize != 1234 || got.PlaintextSize != 1234 {
		t.Fatalf("sizes stored=%d plain=%d, want 1234/1234", got.StoredSize, got.PlaintextSize)
	}
	if got.Encrypted || got.EncryptionVersion != 0 || got.Multipart {
		t.Fatalf("flags = encrypted:%v version:%d multipart:%v", got.Encrypted, got.EncryptionVersion, got.Multipart)
	}
	assertSegments(t, got.Segments, []Segment{{MsgID: 10, Size: 1234}})
}

func TestResolverCarriesReplacementRevisionIntoContentIdentity(t *testing.T) {
	db := newResolverTestDB(t)
	mustApplyOp(t, db, 20, projection.Op{
		Type: projection.OpFileCommit, ProtocolVersion: 1, OpID: "media-create",
		Name: "same-size.mkv", ContentMsgID: 9001, ContentHash: "old", FileSize: 1024,
	})
	mustApplyOp(t, db, 21, projection.Op{
		Type: projection.OpFileReplace, ProtocolVersion: 1, OpID: "media-replace",
		Obj: projection.FileIDPrefix + "20", ExpectedRevision: 1,
		ContentMsgID: 9002, ContentHash: "new", FileSize: 1024, RetainedUntil: 500,
	})

	got, err := NewResolver(db).Resolve(context.Background(), testChannelID, 20)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got.Revision != 2 {
		t.Fatalf("revision = %d, want 2", got.Revision)
	}
	assertSegments(t, got.Segments, []Segment{{MsgID: 9002, Size: 1024}})
}

func TestResolverResolveEncryptedFile(t *testing.T) {
	db := newResolverTestDB(t)
	mustApplyOp(t, db, 11, projection.Op{
		Type:              projection.OpFileUpload,
		Parent:            projection.RootParent,
		Name:              "private.mov",
		FileSize:          8192,
		Encrypted:         true,
		PlaintextSize:     4096,
		EncryptionVersion: 1,
	})

	got, err := NewResolver(db).Resolve(context.Background(), testChannelID, 11)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if !got.Encrypted || got.EncryptionVersion != 1 {
		t.Fatalf("encryption = encrypted:%v version:%d, want true/1", got.Encrypted, got.EncryptionVersion)
	}
	if got.StoredSize != 8192 || got.PlaintextSize != 4096 {
		t.Fatalf("sizes stored=%d plain=%d, want 8192/4096", got.StoredSize, got.PlaintextSize)
	}
	assertSegments(t, got.Segments, []Segment{{MsgID: 11, Size: 8192}})
}

func TestResolverResolveMultipartFile(t *testing.T) {
	db := newResolverTestDB(t)
	applyMultipart(t, db, "upload-a", []partSpec{
		{msgID: 100, size: 1900},
		{msgID: 101, size: 1900},
		{msgID: 102, size: 700},
	}, 200, projection.Op{
		Type:          projection.OpFileManifest,
		UploadUUID:    "upload-a",
		Parent:        projection.RootParent,
		Name:          "movie.mkv",
		FileSize:      4500,
		PartCount:     3,
		Encrypted:     true,
		PlaintextSize: 4400,
	})

	got, err := NewResolver(db).Resolve(context.Background(), testChannelID, 200)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if !got.Multipart {
		t.Fatalf("Multipart = false, want true")
	}
	if got.FileID != 200 || got.Name != "movie.mkv" {
		t.Fatalf("identity = %+v", got)
	}
	if got.StoredSize != 4500 || got.PlaintextSize != 4400 || !got.Encrypted {
		t.Fatalf("sizes/encryption = stored:%d plain:%d encrypted:%v", got.StoredSize, got.PlaintextSize, got.Encrypted)
	}
	assertSegments(t, got.Segments, []Segment{
		{MsgID: 100, Size: 1900},
		{MsgID: 101, Size: 1900},
		{MsgID: 102, Size: 700},
	})
}

func TestResolverRejectsIncompleteMultipart(t *testing.T) {
	db := newResolverTestDB(t)
	applyMultipart(t, db, "upload-b", []partSpec{
		{msgID: 100, size: 100},
		{msgID: 101, size: 100},
	}, 200, projection.Op{
		Type:       projection.OpFileManifest,
		UploadUUID: "upload-b",
		Parent:     projection.RootParent,
		Name:       "broken.mkv",
		FileSize:   200,
		PartCount:  2,
	})
	if _, err := db.Exec(`DELETE FROM file_parts WHERE channel_id = ? AND msg_id = ?`, testChannelID, 101); err != nil {
		t.Fatalf("delete part: %v", err)
	}

	_, err := NewResolver(db).Resolve(context.Background(), testChannelID, 200)
	if !errors.Is(err, ErrIncompleteMultipart) {
		t.Fatalf("err = %v, want ErrIncompleteMultipart", err)
	}
}

func TestResolverRejectsTombstonedOrMissingFile(t *testing.T) {
	db := newResolverTestDB(t)
	mustApplyOp(t, db, 10, projection.Op{
		Type:     projection.OpFileUpload,
		Parent:   projection.RootParent,
		Name:     "clip.mp4",
		FileSize: 1,
	})
	mustApplyOp(t, db, 11, projection.Op{
		Type: projection.OpTomb,
		Obj:  projection.FileIDPrefix + "10",
	})

	_, err := NewResolver(db).Resolve(context.Background(), testChannelID, 10)
	if !errors.Is(err, ErrFileNotFound) {
		t.Fatalf("tombstoned err = %v, want ErrFileNotFound", err)
	}
	_, err = NewResolver(db).Resolve(context.Background(), testChannelID, 999)
	if !errors.Is(err, ErrFileNotFound) {
		t.Fatalf("missing err = %v, want ErrFileNotFound", err)
	}
}

type partSpec struct {
	msgID int64
	size  int64
}

func applyMultipart(t *testing.T, db *sql.DB, uuid string, parts []partSpec, manifestMsgID int64, manifest projection.Op) {
	t.Helper()
	for i, part := range parts {
		mustApplyOp(t, db, part.msgID, projection.Op{
			Type:       projection.OpFilePart,
			UploadUUID: uuid,
			PartIndex:  i,
			FileSize:   part.size,
		})
	}
	mustApplyOp(t, db, manifestMsgID, manifest)
}

func assertSegments(t *testing.T, got, want []Segment) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("segments = %+v, want %+v", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("segment %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func newResolverTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec(`PRAGMA foreign_keys=ON;`); err != nil {
		t.Fatalf("pragma: %v", err)
	}
	if err := projection.MigratePersonalChannel(db, testChannelID); err != nil {
		t.Fatalf("MigratePersonalChannel: %v", err)
	}
	return db
}

func mustApplyOp(t *testing.T, db *sql.DB, msgID int64, op projection.Op) {
	t.Helper()
	tx, err := db.Begin()
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	if err := projection.ApplyOp(tx, testChannelID, msgID, op, 0); err != nil {
		_ = tx.Rollback()
		t.Fatalf("ApplyOp(%s): %v", op.Type, err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}
}
