package projection

import (
	"context"
	"database/sql"
	"math"
	"strings"
	"testing"
)

func TestGallerySchemaMaterializesEligibilityAndMonthCounts(t *testing.T) {
	db := newTestDB(t)
	seedGalleryFiles(t, db, testChan, 4)

	if _, err := db.Exec(`UPDATE files SET name='notes.txt' WHERE channel_id=? AND msg_id=1`, testChan); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO dirents(channel_id,object_id,object_kind,display_name,name_key)
		VALUES(?,'f:2','file','renamed.txt','renamed.txt')`, testChan); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE files SET upload_time=1735689600 WHERE channel_id=? AND msg_id=3`, testChan); err != nil {
		t.Fatal(err)
	}

	var items int
	if err := db.QueryRow(`SELECT COUNT(*) FROM gallery_items WHERE channel_id=?`, testChan).Scan(&items); err != nil {
		t.Fatal(err)
	}
	if items != 2 {
		t.Fatalf("materialized items=%d, want 2", items)
	}

	type monthCount struct {
		month string
		count int
	}
	rows, err := db.Query(`SELECT month_key,item_count FROM gallery_months WHERE channel_id=? ORDER BY month_key DESC`, testChan)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var months []monthCount
	for rows.Next() {
		var month monthCount
		if err := rows.Scan(&month.month, &month.count); err != nil {
			t.Fatal(err)
		}
		months = append(months, month)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if len(months) != 2 || months[0] != (monthCount{"2025-01", 1}) || months[1] != (monthCount{"2023-11", 1}) {
		t.Fatalf("month counts=%+v", months)
	}
}

func TestGalleryMaterializationRollsBackWithProjectionMutation(t *testing.T) {
	db := newTestDB(t)
	seedGalleryFiles(t, db, testChan, 1)

	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(`UPDATE files SET name='document.txt' WHERE channel_id=? AND msg_id=1`, testChan); err != nil {
		t.Fatal(err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}

	var name string
	if err := db.QueryRow(`SELECT display_name FROM gallery_items WHERE channel_id=? AND msg_id=1`, testChan).Scan(&name); err != nil {
		t.Fatal(err)
	}
	if name != "image-1.JPG" {
		t.Fatalf("materialized name=%q after rollback", name)
	}
}

func TestGallerySchemaUpgradeBackfillsExistingProjection(t *testing.T) {
	db := newTestDB(t)
	seedGalleryFiles(t, db, testChan, 3)
	for _, name := range galleryMaterializationTriggerNames() {
		if _, err := db.Exec(`DROP TRIGGER IF EXISTS ` + name); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(`DELETE FROM gallery_items`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`DELETE FROM gallery_months`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`DELETE FROM gallery_schema_meta`); err != nil {
		t.Fatal(err)
	}

	if err := EnsureGallerySchema(db); err != nil {
		t.Fatal(err)
	}
	var items, months int
	if err := db.QueryRow(`SELECT COUNT(*) FROM gallery_items WHERE channel_id=?`, testChan).Scan(&items); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM gallery_months WHERE channel_id=?`, testChan).Scan(&months); err != nil {
		t.Fatal(err)
	}
	if items != 3 || months != 1 {
		t.Fatalf("backfill items=%d months=%d, want 3 and 1", items, months)
	}
}

func TestGallerySchemaV2MigrationBackfillsVideos(t *testing.T) {
	db := newTestDB(t)
	if _, err := db.Exec(`INSERT INTO files(channel_id,msg_id,name,size,parent_id,upload_time,content_msg_id,content_hash,revision)
		VALUES(?,1,'clip.mp4',1,'',1700000000,1,'video',1)`, testChan); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`DELETE FROM gallery_items WHERE channel_id=? AND msg_id=1`, testChan); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE gallery_schema_meta SET version=2 WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	if err := EnsureGallerySchema(db); err != nil {
		t.Fatal(err)
	}
	page, err := MediaPage(context.Background(), db, testChan, "", 128)
	if err != nil || len(page.Items) != 1 || page.Items[0].Name != "clip.mp4" {
		t.Fatalf("v2 migration page=%+v error=%v", page, err)
	}
}

func TestGallerySchemaUpgradeHandlesHistoricalOutOfRangeTimestamp(t *testing.T) {
	db := newTestDB(t)
	for _, name := range galleryMaterializationTriggerNames() {
		if _, err := db.Exec(`DROP TRIGGER IF EXISTS ` + name); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(`INSERT INTO files(channel_id,msg_id,name,size,parent_id,upload_time,content_msg_id,content_hash,revision)
		VALUES(?,1,'legacy.jpg',1,'',?,1,'hash',1)`, testChan, int64(math.MaxInt64)); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE gallery_schema_meta SET version=1 WHERE id=1`); err != nil {
		t.Fatal(err)
	}

	if err := EnsureGallerySchema(db); err != nil {
		t.Fatalf("EnsureGallerySchema: %v", err)
	}
	var month string
	if err := db.QueryRow(`SELECT month_key FROM gallery_items WHERE channel_id=? AND msg_id=1`, testChan).Scan(&month); err != nil {
		t.Fatal(err)
	}
	if month != "unknown" {
		t.Fatalf("month_key=%q, want unknown", month)
	}
}

func TestGalleryGenerationIgnoresUnrelatedFileActivity(t *testing.T) {
	db := newTestDB(t)
	revision := func() int64 {
		var value int64
		if err := db.QueryRow(`SELECT COALESCE((SELECT revision FROM gallery_generations WHERE channel_id=?),0)`, testChan).Scan(&value); err != nil {
			t.Fatal(err)
		}
		return value
	}
	before := revision()
	if _, err := db.Exec(`INSERT INTO files(channel_id,msg_id,name,size,parent_id,upload_time,content_msg_id,content_hash,revision)
		VALUES(?,1,'notes.txt',1,'',1700000000,1,'hash',1)`, testChan); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE files SET size=2 WHERE channel_id=? AND msg_id=1`, testChan); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO dirents(channel_id,object_id,object_kind,display_name,name_key)
		VALUES(?,'f:1','file','renamed.txt','renamed.txt')`, testChan); err != nil {
		t.Fatal(err)
	}
	if after := revision(); after != before {
		t.Fatalf("document activity advanced gallery generation from %d to %d", before, after)
	}
	if _, err := db.Exec(`INSERT INTO files(channel_id,msg_id,name,size,parent_id,upload_time,content_msg_id,content_hash,revision)
		VALUES(?,2,'photo.jpg',1,'',1700000001,2,'hash',1)`, testChan); err != nil {
		t.Fatal(err)
	}
	if after := revision(); after <= before {
		t.Fatalf("gallery insert did not advance generation: before=%d after=%d", before, after)
	}
}

func TestGalleryTimelineCacheIsBoundedAndReturnsImmutableCopies(t *testing.T) {
	cache := newGalleryTimelineCache(2)
	one := GalleryTimeline{ChannelID: 1, Generation: "one", Buckets: []GalleryBucket{{Key: "2026-01", Count: 1}}, Anchors: []GalleryAnchor{{Cursor: "one"}}}
	two := GalleryTimeline{ChannelID: 2, Generation: "two", Buckets: []GalleryBucket{}, Anchors: []GalleryAnchor{}}
	three := GalleryTimeline{ChannelID: 3, Generation: "three", Buckets: []GalleryBucket{}, Anchors: []GalleryAnchor{}}

	cache.put(one)
	cache.put(two)
	copyOne, ok := cache.get("one")
	if !ok {
		t.Fatal("first snapshot missing")
	}
	copyOne.Buckets[0].Count = 99
	copyOne.Anchors[0].Cursor = "changed"
	cache.put(three)

	stored, ok := cache.get("one")
	if !ok || stored.Buckets[0].Count != 1 || stored.Anchors[0].Cursor != "one" {
		t.Fatalf("cached timeline was mutated: %+v, ok=%v", stored, ok)
	}
	if _, ok := cache.get("two"); ok {
		t.Fatal("least-recently-used generation was not evicted")
	}
	if cache.len() != 2 {
		t.Fatalf("cache entries=%d, want 2", cache.len())
	}
}

func TestGalleryTimelineCacheEvictsByApproximateBytes(t *testing.T) {
	cache := newGalleryTimelineCacheWithBytes(10, 1200)
	makeTimeline := func(generation string) GalleryTimeline {
		anchors := make([]GalleryAnchor, 10)
		for i := range anchors {
			anchors[i] = GalleryAnchor{StartIndex: i * 128, Cursor: strings.Repeat(generation, 20)}
		}
		return GalleryTimeline{Generation: generation, Anchors: anchors, Buckets: []GalleryBucket{}}
	}
	cache.put(makeTimeline("one"))
	cache.put(makeTimeline("two"))
	if cache.len() != 1 {
		t.Fatalf("cache entries=%d, want byte budget to retain one", cache.len())
	}
	cache.put(GalleryTimeline{Generation: "oversize", Anchors: []GalleryAnchor{{Cursor: strings.Repeat("x", 2000)}}})
	if _, ok := cache.get("oversize"); ok {
		t.Fatal("oversized timeline exceeded byte cap")
	}
}

func TestMediaTimelineCachesGenerationWithoutSharingMutableSlices(t *testing.T) {
	db := newTestDB(t)
	seedGalleryFiles(t, db, testChan, 300)
	original := mediaTimelineCache
	mediaTimelineCache = newGalleryTimelineCache(2)
	t.Cleanup(func() { mediaTimelineCache = original })

	first, err := MediaTimeline(context.Background(), db, testChan)
	if err != nil {
		t.Fatal(err)
	}
	first.Buckets[0].Count = 0
	first.Anchors[0].Cursor = "corrupt caller copy"
	second, err := MediaTimeline(context.Background(), db, testChan)
	if err != nil {
		t.Fatal(err)
	}
	if second.Buckets[0].Count != 300 || second.Anchors[0].Cursor == "corrupt caller copy" {
		t.Fatalf("cached timeline leaked caller mutation: %+v", second)
	}
	if mediaTimelineCache.len() != 1 {
		t.Fatalf("cached generations=%d, want 1", mediaTimelineCache.len())
	}
}

func TestLocateGalleryItemUsesNearestSparseAnchor(t *testing.T) {
	db := newTestDB(t)
	seedGalleryFiles(t, db, testChan, 400)
	// Mixed eligibility ensures an anchor is based on gallery rank rather than
	// the physical files-table row number.
	if _, err := db.Exec(`UPDATE files SET name='document.txt' WHERE channel_id=? AND msg_id%7=0`, testChan); err != nil {
		t.Fatal(err)
	}
	timeline, err := MediaTimeline(context.Background(), db, testChan)
	if err != nil {
		t.Fatal(err)
	}
	tx, _, err := beginGalleryRead(context.Background(), db, testChan, timeline.Generation)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	item, err := galleryItem(context.Background(), tx, testChan, 200)
	if err != nil {
		t.Fatal(err)
	}
	located, index, scanned, err := locateGalleryItemFromTimeline(context.Background(), tx, testChan, item, timeline)
	if err != nil {
		t.Fatal(err)
	}
	if located.MsgID != 200 || index != 171 {
		t.Fatalf("located=%d index=%d, want 200 at 171", located.MsgID, index)
	}
	if scanned > GalleryPageSize {
		t.Fatalf("locate scanned %d eligible rows, want <=%d", scanned, GalleryPageSize)
	}
}

func TestGalleryMaterializedPageUsesCoveringOrderIndex(t *testing.T) {
	db := newTestDB(t)
	rows, err := db.Query(`EXPLAIN QUERY PLAN `+gallerySelect+galleryFrom+
		` AND (gi.upload_time,gi.msg_id)<=(?,?) ORDER BY gi.upload_time DESC,gi.msg_id DESC LIMIT ?`,
		testChan, 1700000000, 1000, 129)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	indexed := false
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(detail, "idx_gallery_items_channel_order") {
			indexed = true
		}
		if strings.Contains(detail, "SCAN gallery_items") || strings.Contains(detail, "TEMP B-TREE") {
			t.Fatalf("unbounded materialized page query: %s", detail)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if !indexed {
		t.Fatal("gallery page must seek the materialized order index")
	}
}

func TestGalleryMonthRecalculationUsesMonthOrderIndex(t *testing.T) {
	db := newTestDB(t)
	rows, err := db.Query(`EXPLAIN QUERY PLAN SELECT MAX(upload_time) FROM gallery_items
		WHERE channel_id=? AND month_key=?`, testChan, "2026-09")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	indexed := false
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(detail, "idx_gallery_items_channel_month_order") {
			indexed = true
		}
		if strings.Contains(detail, "SCAN gallery_items") {
			t.Fatalf("month recalculation scans gallery items: %s", detail)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if !indexed {
		t.Fatal("gallery month recalculation must use the month-order index")
	}
}

func seedMillionGalleryRows(b *testing.B, db *sql.DB, channelID int64, count int) {
	b.Helper()
	tx, err := db.Begin()
	if err != nil {
		b.Fatal(err)
	}
	defer tx.Rollback()
	stmt, err := tx.Prepare(`INSERT INTO files(channel_id,msg_id,name,size,parent_id,upload_time,content_msg_id,content_hash,revision)
		VALUES(?,?,CASE WHEN ?%5=0 THEN 'document.txt' ELSE 'image.jpg' END,1048576,'',?,?,?,1)`)
	if err != nil {
		b.Fatal(err)
	}
	defer stmt.Close()
	for i := 1; i <= count; i++ {
		if _, err := stmt.Exec(channelID, i, i, int64(1700000000+i/5), i+1000000, "hash"); err != nil {
			b.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		b.Fatal(err)
	}
}
