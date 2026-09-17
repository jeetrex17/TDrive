package projection

import (
	"context"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

func seedGalleryFiles(t testing.TB, db *sql.DB, channelID int64, count int) {
	t.Helper()
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	stmt, err := tx.Prepare(`INSERT INTO files(channel_id,msg_id,name,size,parent_id,upload_time,content_msg_id,content_hash,revision) VALUES(?,?,?,1048576,'',?,?,?,1)`)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	for i := 1; i <= count; i++ {
		if _, err = stmt.Exec(channelID, i, fmt.Sprintf("image-%d.JPG", i), int64(1700000000+i/5), i+1000000, fmt.Sprintf("hash-%d", i)); err != nil {
			t.Fatal(err)
		}
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

func TestGalleryTimelineAndPagesUseStableSparseAnchors(t *testing.T) {
	db := newTestDB(t)
	seedGalleryFiles(t, db, testChan, 300)
	timeline, err := MediaTimeline(context.Background(), db, testChan)
	if err != nil {
		t.Fatal(err)
	}
	if timeline.TotalCount != 300 || timeline.PageSize != 128 || len(timeline.Anchors) != 3 {
		t.Fatalf("timeline = %+v", timeline)
	}
	if len(timeline.Buckets) != 1 || timeline.Buckets[0].Key != "2023-11" || timeline.Buckets[0].Count != 300 {
		t.Fatalf("buckets=%+v", timeline.Buckets)
	}
	for i, anchor := range timeline.Anchors {
		page, err := MediaPage(context.Background(), db, testChan, anchor.Cursor, 128)
		if err != nil {
			t.Fatal(err)
		}
		want := 128
		if i == 2 {
			want = 44
		}
		if page.StartIndex != i*128 || len(page.Items) != want {
			t.Fatalf("page=%+v", page)
		}
		for j, item := range page.Items {
			if item.MsgID != int64(300-i*128-j) || item.ContentMsgID != item.MsgID+1000000 || item.ContentHash == "" {
				t.Fatalf("item=%+v", item)
			}
		}
	}
	first, err := MediaPage(context.Background(), db, testChan, timeline.Anchors[0].Cursor, 128)
	if err != nil {
		t.Fatal(err)
	}
	second, err := MediaPage(context.Background(), db, testChan, first.NextCursor, 128)
	if err != nil || second.StartIndex != 128 || second.Items[0].MsgID != 172 {
		t.Fatalf("next page=%+v err=%v", second, err)
	}
}

func TestGalleryFiltersAndRespectsProjectedName(t *testing.T) {
	db := newTestDB(t)
	seedGalleryFiles(t, db, testChan, 8)
	statements := []string{
		`UPDATE files SET tombstoned=1 WHERE msg_id=1`,
		`UPDATE files SET upload_uuid='multipart' WHERE msg_id=2`,
		`UPDATE files SET name='document.txt' WHERE msg_id=3`,
		`INSERT INTO dirents(channel_id,object_id,object_kind,parent_id,display_name,name_key) VALUES(1001,'f:4','file','','renamed.txt','renamed.txt')`,
		`INSERT INTO dirents(channel_id,object_id,object_kind,parent_id,display_name,name_key,tombstoned) VALUES(1001,'f:5','file','','trashed.jpg','trashed.jpg',1)`,
		`UPDATE files SET parent_id='d:missing' WHERE msg_id=6`,
	}
	for _, stmt := range statements {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatal(err)
		}
	}
	page, err := MediaPage(context.Background(), db, testChan, "", 128)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 3 {
		t.Fatalf("items=%+v", page.Items)
	}
	if page.Items[2].MsgID != 6 {
		t.Fatal("legacy orphan should remain visible")
	}
}

func TestGalleryCursorRejectsStaleAndWrongScope(t *testing.T) {
	db := newTestDB(t)
	seedGalleryFiles(t, db, testChan, 10)
	timeline, err := MediaTimeline(context.Background(), db, testChan)
	if err != nil {
		t.Fatal(err)
	}
	cursor := timeline.Anchors[0].Cursor
	if _, err := MediaPage(context.Background(), db, testChan+1, cursor, 128); !errors.Is(err, ErrInvalidGalleryCursor) {
		t.Fatalf("wrong channel error=%v", err)
	}
	for _, limit := range []int{-1, 0, 513} {
		if _, err := MediaPage(context.Background(), db, testChan, cursor, limit); !errors.Is(err, ErrInvalidGalleryLimit) {
			t.Fatalf("limit %d error=%v", limit, err)
		}
	}
	for _, bad := range []string{"not-a-cursor", "e30", string(make([]byte, 2049))} {
		if _, err := MediaPage(context.Background(), db, testChan, bad, 128); !errors.Is(err, ErrInvalidGalleryCursor) {
			t.Fatalf("bad cursor error=%v", err)
		}
	}
	if _, err := db.Exec(`UPDATE files SET content_msg_id=999,revision=2 WHERE msg_id=8`); err != nil {
		t.Fatal(err)
	}
	if _, err := MediaPage(context.Background(), db, testChan, cursor, 128); !errors.Is(err, ErrGalleryStale) {
		t.Fatalf("stale error=%v", err)
	}
	other := newTestDB(t)
	seedGalleryFiles(t, other, testChan, 10)
	if _, err := MediaPage(context.Background(), other, testChan, cursor, 128); !errors.Is(err, ErrGalleryStale) {
		t.Fatalf("cross database error=%v", err)
	}
}

func TestGalleryLocateAndNeighbors(t *testing.T) {
	db := newTestDB(t)
	seedGalleryFiles(t, db, testChan, 300)
	timeline, err := MediaTimeline(context.Background(), db, testChan)
	if err != nil {
		t.Fatal(err)
	}
	location, err := LocateMedia(context.Background(), db, testChan, 130, timeline.Generation)
	if err != nil || location.Index != 170 {
		t.Fatalf("location=%+v error=%v", location, err)
	}
	page, err := MediaPage(context.Background(), db, testChan, location.Cursor, 4)
	if err != nil || len(page.Items) != 4 || page.Items[0].MsgID != 130 {
		t.Fatalf("located page=%+v error=%v", page, err)
	}
	neighbors, err := MediaNeighbors(context.Background(), db, testChan, 130, 2, 3, timeline.Generation)
	if err != nil || neighbors.StartIndex != -1 || neighbors.AnchorOffset != 2 || len(neighbors.Items) != 6 {
		t.Fatalf("neighbors=%+v error=%v", neighbors, err)
	}
	for i, item := range neighbors.Items {
		if item.MsgID != int64(132-i) {
			t.Fatalf("neighbor=%+v", item)
		}
	}
	edge, err := MediaNeighbors(context.Background(), db, testChan, 300, 3, 1, timeline.Generation)
	if err != nil || edge.StartIndex != -1 || edge.AnchorOffset != 0 || len(edge.Items) != 2 {
		t.Fatalf("edge=%+v error=%v", edge, err)
	}
	if _, err := LocateMedia(context.Background(), db, testChan, 999, timeline.Generation); !errors.Is(err, ErrGalleryNotFound) {
		t.Fatalf("not found error=%v", err)
	}
	if _, err := MediaNeighbors(context.Background(), db, testChan, 130, 129, 0, timeline.Generation); !errors.Is(err, ErrInvalidGalleryLimit) {
		t.Fatalf("neighbor limit error=%v", err)
	}
}

func TestGalleryGenerationTracksDirentsAndTransactionRollback(t *testing.T) {
	db := newTestDB(t)
	seedGalleryFiles(t, db, testChan, 1)
	before, err := MediaTimeline(context.Background(), db, testChan)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(`DELETE FROM files WHERE msg_id=1`); err != nil {
		t.Fatal(err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	after, err := MediaTimeline(context.Background(), db, testChan)
	if err != nil || before.Generation != after.Generation {
		t.Fatalf("rollback generation changed: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO dirents(channel_id,object_id,object_kind,display_name,name_key) VALUES(1001,'f:1','file','hidden.txt','hidden.txt')`); err != nil {
		t.Fatal(err)
	}
	if _, err := MediaPage(context.Background(), db, testChan, before.Anchors[0].Cursor, 128); !errors.Is(err, ErrGalleryStale) {
		t.Fatalf("rename stale error=%v", err)
	}
}

func TestGalleryEmptyAndContext(t *testing.T) {
	db := newTestDB(t)
	timeline, err := MediaTimeline(context.Background(), db, testChan)
	if err != nil || timeline.TotalCount != 0 || timeline.Anchors == nil || timeline.Buckets == nil {
		t.Fatalf("empty=%+v error=%v", timeline, err)
	}
	if _, err := MediaTimeline(nil, db, testChan); !errors.Is(err, ErrInvalidContext) {
		t.Fatalf("nil context=%v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := MediaTimeline(ctx, db, testChan); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel=%v", err)
	}
	if _, err := MediaPage(ctx, db, testChan, "", 128); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel page=%v", err)
	}
}

func TestGalleryMonthBoundaries(t *testing.T) {
	db := newTestDB(t)
	seedGalleryFiles(t, db, testChan, 3)
	for i, stamp := range []string{"2026-01-01T00:00:00Z", "2025-12-31T23:59:59Z", "2025-12-01T00:00:00Z"} {
		date, _ := time.Parse(time.RFC3339, stamp)
		if _, err := db.Exec(`UPDATE files SET upload_time=? WHERE msg_id=?`, date.Unix(), i+1); err != nil {
			t.Fatal(err)
		}
	}
	timeline, err := MediaTimeline(context.Background(), db, testChan)
	if err != nil || len(timeline.Buckets) != 2 || timeline.Buckets[0].Count != 1 || timeline.Buckets[1].StartIndex != 1 || timeline.Buckets[1].Count != 2 {
		t.Fatalf("timeline=%+v error=%v", timeline, err)
	}
}

func TestGalleryCursorValidationRejectsTrailingDataAndInvalidIdentity(t *testing.T) {
	db := newTestDB(t)
	seedGalleryFiles(t, db, testChan, 1)
	timeline, err := MediaTimeline(context.Background(), db, testChan)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := base64.RawURLEncoding.DecodeString(timeline.Anchors[0].Cursor)
	if err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{string(decoded) + " {}", string(decoded) + " garbage", strings.Replace(string(decoded), `"v":1`, `"v":2`, 1), strings.Replace(string(decoded), `"m":1`, `"m":0`, 1)} {
		cursor := base64.RawURLEncoding.EncodeToString([]byte(raw))
		if _, err := MediaPage(context.Background(), db, testChan, cursor, 128); !errors.Is(err, ErrInvalidGalleryCursor) {
			t.Fatalf("invalid cursor accepted: %v", err)
		}
	}
}

func TestGalleryPagesUseIndexedSeekWithoutSortOrOffset(t *testing.T) {
	db := newTestDB(t)
	rows, err := db.Query(`EXPLAIN QUERY PLAN `+gallerySelect+galleryFrom+` AND (gi.upload_time,gi.msg_id)<=(?,?) ORDER BY gi.upload_time DESC,gi.msg_id DESC LIMIT ?`, testChan, 1700000000, 1000, 129)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	seek := false
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(detail, "idx_gallery_items_channel_order") {
			seek = true
		}
		if strings.Contains(detail, "SCAN gi") || strings.Contains(detail, "TEMP B-TREE") {
			t.Fatalf("unbounded page query: %s", detail)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if !seek {
		t.Fatal("gallery page must use the indexed upload-time seek")
	}
}
