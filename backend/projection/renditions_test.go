package projection

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
)

func renditionFixture() FileRendition {
	return FileRendition{ChannelID: testChan, FileMsgID: 10, ContentMsgID: 10, Kind: "thumbnail", Version: 1, Width: 320, Height: 240, Size: 100, PlaintextSize: 100}
}
func renditionOp(ref FileRendition) Op {
	return Op{Type: OpFilePart, UploadUUID: "rendition-unique", PartIndex: 0, FileSize: ref.Size, Rendition: &ref}
}

func TestRenditionWireRemainsLegacyHiddenPart(t *testing.T) {
	ref := renditionFixture()
	header := Format(renditionOp(ref))
	parsed, err := Parse(header)
	if err != nil || parsed.Rendition == nil || *parsed.Rendition != ref {
		t.Fatalf("round trip: %+v, %v", parsed, err)
	}
	// The historical parser reads only t/u/pix/sz for parts and ignores every
	// unrecognized key. Removing the extension exercises its exact old branch.
	fields := strings.Split(header, "|")
	legacy := []string{}
	for _, field := range fields {
		if !strings.HasPrefix(field, "rend=") {
			legacy = append(legacy, field)
		}
	}
	old, err := Parse(strings.Join(legacy, "|"))
	if err != nil || old.Type != OpFilePart || old.Rendition != nil {
		t.Fatalf("legacy parser: %+v, %v", old, err)
	}
	db := newTestDB(t)
	if err := runOp(t, db, testChan, 11, old); err != nil {
		t.Fatal(err)
	}
	if countLiveFilesP(t, db) != 0 {
		t.Fatal("legacy readers exposed derivative as a file")
	}
}

func TestRenditionCurrentContentAndReplay(t *testing.T) {
	db := newTestDB(t)
	original := Op{Type: OpFileUpload, Name: "photo.jpg", FileSize: 1000}
	if _, err := ProjectFromOp(db, testChan, 10, original, 7, Format(original)); err != nil {
		t.Fatal(err)
	}
	op := renditionOp(renditionFixture())
	for _, id := range []int64{11, 12} {
		if _, err := ProjectFromOp(db, testChan, id, op, 7, Format(op)); err != nil {
			t.Fatal(err)
		}
	}
	assert := func() {
		t.Helper()
		got, err := CurrentFileRendition(context.Background(), db, testChan, 10, "thumbnail")
		if err != nil || got.MsgID != 11 {
			t.Fatalf("current: %+v %v", got, err)
		}
	}
	assert()
	if err := RebuildProjection(db, testChan); err != nil {
		t.Fatal(err)
	}
	assert()
	if err := runOp(t, db, testChan, 13, Op{Type: OpRename, Obj: "f:10", Name: "renamed.jpg"}); err != nil {
		t.Fatal(err)
	}
	assert()
	if _, err := db.Exec(`UPDATE files SET content_msg_id=99 WHERE channel_id=? AND msg_id=10`, testChan); err != nil {
		t.Fatal(err)
	}
	if _, err := CurrentFileRendition(context.Background(), db, testChan, 10, "thumbnail"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("stale rendition returned: %v", err)
	}
}

func TestRenditionRejectsMalformedBinding(t *testing.T) {
	for _, mutate := range []func(*FileRendition){
		func(r *FileRendition) { r.FileMsgID = 0 }, func(r *FileRendition) { r.ContentMsgID = 0 },
		func(r *FileRendition) { r.Kind = "original" }, func(r *FileRendition) { r.Version = 2 },
		func(r *FileRendition) { r.Width = 100000 }, func(r *FileRendition) { r.Size = 10 << 20 },
		func(r *FileRendition) { r.UploadUUID = "both" },
	} {
		ref := renditionFixture()
		mutate(&ref)
		if _, err := Parse(Format(renditionOp(ref))); err == nil {
			t.Fatalf("accepted %+v", ref)
		}
	}
	db := newTestDB(t)
	ref := renditionFixture()
	ref.ChannelID++
	if err := runOp(t, db, testChan, 11, renditionOp(ref)); err == nil {
		t.Fatal("accepted another channel's descriptor")
	}
}

func TestHardDeleteIncludesEveryRendition(t *testing.T) {
	db := newTestDB(t)
	if err := runOp(t, db, testChan, 10, Op{Type: OpFileUpload, Name: "photo.jpg", FileSize: 1000}); err != nil {
		t.Fatal(err)
	}
	for _, id := range []int64{11, 12} {
		if err := runOp(t, db, testChan, id, renditionOp(renditionFixture())); err != nil {
			t.Fatal(err)
		}
	}
	mustOp(t, db, 13, Op{Type: OpHardDeleteTree, ProtocolVersion: 1, OpID: "purge-renditions", Obj: "f:10", ExpectedRevision: 1})
	assertHardDeletePlan(t, db, "purge-renditions", []int64{10, 11, 12}, 3, true)
}

func TestRenditionUpgradeRecoversHeaderIgnoredByOldReader(t *testing.T) {
	db := newTestDB(t)
	original := Op{Type: OpFileUpload, Name: "photo.jpg", FileSize: 1000}
	if _, err := ProjectFromOp(db, testChan, 10, original, 7, Format(original)); err != nil {
		t.Fatal(err)
	}
	full := renditionOp(renditionFixture())
	old := full
	old.Rendition = nil
	if _, err := ProjectFromOp(db, testChan, 11, old, 7, Format(full)); err != nil {
		t.Fatal(err)
	}
	if _, err := CurrentFileRendition(context.Background(), db, testChan, 10, "thumbnail"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("legacy fixture unexpectedly has extension")
	}
	if _, err := db.Exec(`UPDATE schema_version SET version=13`); err != nil {
		t.Fatal(err)
	}
	if err := MigratePersonalChannel(db, testChan); err != nil {
		t.Fatal(err)
	}
	if got, err := CurrentFileRendition(context.Background(), db, testChan, 10, "thumbnail"); err != nil || got.MsgID != 11 {
		t.Fatalf("upgrade lost old reader's derivative: %+v %v", got, err)
	}
	if err := RebuildProjection(db, testChan); err != nil {
		t.Fatal(err)
	}
	if got, err := CurrentFileRendition(context.Background(), db, testChan, 10, "thumbnail"); err != nil || got.MsgID != 11 {
		t.Fatalf("rebuild lost derivative: %+v %v", got, err)
	}
}

func TestSharedRenditionRejectsAnotherUploaderIncludingOutOfOrder(t *testing.T) {
	db := newTestDB(t)
	if _, err := db.Exec(`UPDATE channels SET kind='shared' WHERE channel_id=?`, testChan); err != nil {
		t.Fatal(err)
	}
	original := Op{Type: OpFileUpload, Name: "photo.jpg", FileSize: 1000}
	if _, err := ProjectFromOp(db, testChan, 10, original, 42, Format(original)); err != nil {
		t.Fatal(err)
	}
	bad := renditionOp(renditionFixture())
	if _, err := ProjectFromOp(db, testChan, 11, bad, 99, Format(bad)); err != nil {
		t.Fatal(err)
	}
	if _, err := CurrentFileRendition(context.Background(), db, testChan, 10, "thumbnail"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("another uploader poisoned thumbnail")
	}
	if _, err := ProjectFromOp(db, testChan, 12, bad, 42, Format(bad)); err != nil {
		t.Fatal(err)
	}
	if got, err := CurrentFileRendition(context.Background(), db, testChan, 10, "thumbnail"); err != nil || got.MsgID != 12 {
		t.Fatalf("owner cannot prepare: %+v %v", got, err)
	}
	// A part can arrive before its parent during reverse history sync. Trust
	// still derives from the eventual original's sender, not arrival ordering.
	ref := renditionFixture()
	ref.FileMsgID = 20
	ref.ContentMsgID = 20
	early := renditionOp(ref)
	if _, err := ProjectFromOp(db, testChan, 21, early, 99, Format(early)); err != nil {
		t.Fatal(err)
	}
	if _, err := ProjectFromOp(db, testChan, 20, original, 42, Format(original)); err != nil {
		t.Fatal(err)
	}
	if _, err := CurrentFileRendition(context.Background(), db, testChan, 20, "thumbnail"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("out-of-order attacker accepted")
	}
}
func TestRenditionQuarantineAllowsRepairAndSurvivesRebuild(t *testing.T) {
	db := newTestDB(t)
	original := Op{Type: OpFileUpload, Name: "photo.jpg", FileSize: 1000}
	if _, err := ProjectFromOp(db, testChan, 10, original, 7, Format(original)); err != nil {
		t.Fatal(err)
	}
	for _, id := range []int64{11, 12} {
		op := renditionOp(renditionFixture())
		if _, err := ProjectFromOp(db, testChan, id, op, 7, Format(op)); err != nil {
			t.Fatal(err)
		}
	}
	if err := InvalidateRendition(context.Background(), db, testChan, 11); err != nil {
		t.Fatal(err)
	}
	if err := RebuildProjection(db, testChan); err != nil {
		t.Fatal(err)
	}
	if got, err := CurrentFileRendition(context.Background(), db, testChan, 10, "thumbnail"); err != nil || got.MsgID != 12 {
		t.Fatalf("repaired reference not selected: %+v %v", got, err)
	}
	ids, err := RenditionMessageIDsForFiles(db, testChan, []int64{10})
	if err != nil || len(ids) != 2 {
		t.Fatalf("quarantined blob omitted from cleanup: %v %v", ids, err)
	}
}
