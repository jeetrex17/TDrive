package projection

import (
	"context"
	"errors"
	"testing"
)

func TestGalleryPreparationOnlyIncludesMissingCurrentRenditions(t *testing.T) {
	db := newTestDB(t)
	seedGalleryFiles(t, db, testChan, 6)
	if _, err := db.Exec(`UPDATE files SET size=? WHERE msg_id=1`, 31<<20); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE files SET name='unsupported.heic' WHERE msg_id=6`); err != nil {
		t.Fatal(err)
	}
	add := func(id, content int, kind string) {
		t.Helper()
		if _, err := db.Exec(`INSERT INTO file_renditions(channel_id,msg_id,file_msg_id,content_msg_id,kind,version,size,plaintext_size,width,height,encrypted) VALUES(?,?,?,?,?,1,100,100,100,100,0)`, testChan, id*10+len(kind), id, content, kind); err != nil {
			t.Fatal(err)
		}
	}
	// Fully ready file2 is excluded. One missing kind, obsolete content, and an
	// encryption-state mismatch each require preparation again.
	add(2, 1000002, RenditionThumbnail)
	add(2, 1000002, RenditionPreview)
	add(3, 1000003, RenditionThumbnail)
	add(4, 999, RenditionThumbnail)
	add(4, 999, RenditionPreview)
	add(5, 1000005, RenditionThumbnail)
	add(5, 1000005, RenditionPreview)
	if _, err := db.Exec(`UPDATE files SET encrypted=1 WHERE msg_id=5`); err != nil {
		t.Fatal(err)
	}
	estimate, err := GalleryPreparationSummary(context.Background(), db, testChan)
	if err != nil || estimate.Total != 3 || estimate.BytesTotal != 3<<20 {
		t.Fatalf("summary=%+v error=%v", estimate, err)
	}
	page, err := GalleryPreparationPage(context.Background(), db, testChan, 0, 2)
	if err != nil || len(page) != 2 || page[0].MsgID != 3 || page[1].MsgID != 4 {
		t.Fatalf("page=%+v error=%v", page, err)
	}
	add(3, 1000003, RenditionPreview)
	next, err := GalleryPreparationPage(context.Background(), db, testChan, page[1].MsgID, 2)
	if err != nil || len(next) != 1 || next[0].MsgID != 5 {
		t.Fatalf("next=%+v error=%v", next, err)
	}
}

func TestGalleryPreparationLimitsAndCancellation(t *testing.T) {
	db := newTestDB(t)
	seedGalleryFiles(t, db, testChan, 130)
	page, err := GalleryPreparationPage(context.Background(), db, testChan, 0, 0)
	if err != nil || len(page) != 64 {
		t.Fatalf("default page len=%d error=%v", len(page), err)
	}
	for _, limit := range []int{-1, 129} {
		if _, err := GalleryPreparationPage(context.Background(), db, testChan, 0, limit); !errors.Is(err, ErrInvalidGalleryLimit) {
			t.Fatalf("limit error=%v", err)
		}
	}
	if _, err := GalleryPreparationPage(context.Background(), db, testChan, -1, 64); !errors.Is(err, ErrInvalidGalleryCursor) {
		t.Fatalf("cursor error=%v", err)
	}
	if _, err := GalleryPreparationSummary(nil, db, testChan); !errors.Is(err, ErrInvalidContext) {
		t.Fatalf("context error=%v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := GalleryPreparationSummary(ctx, db, testChan); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation error=%v", err)
	}
	if _, err := GalleryPreparationPage(ctx, db, testChan, 0, 64); !errors.Is(err, context.Canceled) {
		t.Fatalf("page cancellation error=%v", err)
	}
}

func TestGalleryPreparationSkipIsContentAndDecoderScoped(t *testing.T) {
	db := newTestDB(t)
	seedGalleryFiles(t, db, testChan, 2)
	source, found, err := FileByID(db, testChan, 1)
	if err != nil || !found {
		t.Fatalf("file=%+v found=%v err=%v", source, found, err)
	}
	if err := RecordGalleryPreparationSkip(context.Background(), db, source); err != nil {
		t.Fatal(err)
	}
	estimate, err := GalleryPreparationSummary(context.Background(), db, testChan)
	if err != nil || estimate.Total != 1 {
		t.Fatalf("skip estimate=%+v err=%v", estimate, err)
	}
	if _, err := db.Exec(`UPDATE gallery_preparation_skips SET decoder_profile='another-decoder'`); err != nil {
		t.Fatal(err)
	}
	estimate, err = GalleryPreparationSummary(context.Background(), db, testChan)
	if err != nil || estimate.Total != 2 {
		t.Fatalf("decoder estimate=%+v err=%v", estimate, err)
	}
	if err := RecordGalleryPreparationSkip(context.Background(), db, source); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE files SET content_msg_id=99,revision=revision+1 WHERE msg_id=1`); err != nil {
		t.Fatal(err)
	}
	estimate, err = GalleryPreparationSummary(context.Background(), db, testChan)
	if err != nil || estimate.Total != 2 {
		t.Fatalf("new content estimate=%+v err=%v", estimate, err)
	}
	if err := RecordGalleryPreparationSkip(context.Background(), db, source); err != nil {
		t.Fatal(err)
	}
	estimate, err = GalleryPreparationSummary(context.Background(), db, testChan)
	if err != nil || estimate.Total != 2 {
		t.Fatal("stale failure must not skip replacement")
	}
	if _, err := db.Exec(`DELETE FROM files WHERE msg_id=1`); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM gallery_preparation_skips`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("skip cleanup=%d err=%v", count, err)
	}
}

func TestGalleryPreparationSharedDriveCandidatesBelongToActor(t *testing.T) {
	db := newTestDB(t)
	seedGalleryFiles(t, db, testChan, 2)
	if _, err := db.Exec(`UPDATE channels SET kind='shared' WHERE channel_id=?`, testChan); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE files SET uploader_user_id=CASE WHEN msg_id=1 THEN 99 ELSE 7 END`); err != nil {
		t.Fatal(err)
	}
	estimate, err := GalleryPreparationSummary(context.Background(), db, testChan, 7)
	if err != nil || estimate.Total != 1 {
		t.Fatalf("owned estimate=%+v error=%v", estimate, err)
	}
	page, err := GalleryPreparationPage(context.Background(), db, testChan, 0, 64, 7)
	if err != nil || len(page) != 1 || page[0].MsgID != 2 {
		t.Fatalf("owned page=%+v error=%v", page, err)
	}
}
