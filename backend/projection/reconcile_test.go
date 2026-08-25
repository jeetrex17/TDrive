package projection

import (
	"fmt"
	"slices"
	"testing"
)

func TestLiveFileMessageIDsEmptyChannel(t *testing.T) {
	db := newTestDB(t)
	refs, err := LiveFileMessageIDs(db, testChan)
	if err != nil {
		t.Fatalf("LiveFileMessageIDs: %v", err)
	}
	if len(refs) != 0 {
		t.Fatalf("refs = %+v, want empty", refs)
	}
}

func TestLiveFileMessageIDsSingleFile(t *testing.T) {
	db := newTestDB(t)
	if err := runOp(t, db, testChan, 10, Op{Type: OpFileUpload, Parent: RootParent, Name: "x.png", FileSize: 1}); err != nil {
		t.Fatalf("upload: %v", err)
	}

	refs, err := LiveFileMessageIDs(db, testChan)
	if err != nil {
		t.Fatalf("LiveFileMessageIDs: %v", err)
	}
	if len(refs) != 1 {
		t.Fatalf("refs = %+v, want 1", refs)
	}
	if refs[0].FileMsgID != 10 {
		t.Fatalf("FileMsgID = %d, want 10", refs[0].FileMsgID)
	}
	if len(refs[0].BackingMsgIDs) != 1 || refs[0].BackingMsgIDs[0] != 10 {
		t.Fatalf("BackingMsgIDs = %v, want [10]", refs[0].BackingMsgIDs)
	}
}

func TestLiveFileMessageIDsMultipartFile(t *testing.T) {
	db := newTestDB(t)
	applyMultipartUpload(t, db, "uuid-1", []int64{1, 2, 3}, 4, "movie.bin", RootParent, false)

	refs, err := LiveFileMessageIDs(db, testChan)
	if err != nil {
		t.Fatalf("LiveFileMessageIDs: %v", err)
	}
	if len(refs) != 1 {
		t.Fatalf("refs = %+v, want 1", refs)
	}
	if refs[0].FileMsgID != 4 {
		t.Fatalf("FileMsgID = %d, want 4 (manifest)", refs[0].FileMsgID)
	}
	got := append([]int64(nil), refs[0].BackingMsgIDs...)
	slices.Sort(got)
	want := []int64{1, 2, 3, 4}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("BackingMsgIDs = %v, want %v", got, want)
	}
}

func TestLiveFileMessageIDsExcludesTombstoned(t *testing.T) {
	db := newTestDB(t)
	if err := runOp(t, db, testChan, 10, Op{Type: OpFileUpload, Parent: RootParent, Name: "x.png", FileSize: 1}); err != nil {
		t.Fatalf("upload: %v", err)
	}
	if err := runOp(t, db, testChan, 11, Op{Type: OpTomb, Obj: fmt.Sprintf("%s%d", FileIDPrefix, 10)}); err != nil {
		t.Fatalf("tomb: %v", err)
	}

	refs, err := LiveFileMessageIDs(db, testChan)
	if err != nil {
		t.Fatalf("LiveFileMessageIDs: %v", err)
	}
	if len(refs) != 0 {
		t.Fatalf("refs = %+v, want empty (tombstoned)", refs)
	}
}
