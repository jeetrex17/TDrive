package projection

import (
	"database/sql"
	"math"
	"strings"
	"testing"
)

func TestGalleryDirentLookupUsesCompositeFileIndex(t *testing.T) {
	db := newTestDB(t)
	rows, err := db.Query(`EXPLAIN QUERY PLAN SELECT `+galleryDirentFileMsgIDSQL("d")+
		` FROM (SELECT ? AS channel_id, ? AS object_id) d`, testChan, "f:123")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	indexed := false
	var details []string
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		details = append(details, detail)
		if strings.Contains(detail, "SEARCH f ") && strings.Contains(detail, "(channel_id=? AND msg_id=?)") {
			indexed = true
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if !indexed {
		t.Fatalf("dirent lookup must seek both file key columns, plan: %v", details)
	}
}

func TestGalleryDirentLookupPreservesExactFileIDs(t *testing.T) {
	db := newTestDB(t)
	for _, id := range []int64{0, 123, math.MaxInt64} {
		if _, err := db.Exec(`INSERT INTO files(channel_id,msg_id,name,size,upload_time)
			VALUES(?,?,'photo.jpg',1,1700000000)`, testChan, id); err != nil {
			t.Fatal(err)
		}
	}
	for _, test := range []struct {
		name      string
		channelID int64
		objectID  string
		want      sql.NullInt64
	}{
		{"canonical", testChan, "f:123", sql.NullInt64{Int64: 123, Valid: true}},
		{"zero", testChan, "f:0", sql.NullInt64{Int64: 0, Valid: true}},
		{"maximum", testChan, "f:9223372036854775807", sql.NullInt64{Int64: math.MaxInt64, Valid: true}},
		{"other channel", testChan + 1, "f:123", sql.NullInt64{}},
		{"missing file", testChan, "f:124", sql.NullInt64{}},
		{"leading zero", testChan, "f:0123", sql.NullInt64{}},
		{"positive sign", testChan, "f:+123", sql.NullInt64{}},
		{"trailing text", testChan, "f:123junk", sql.NullInt64{}},
		{"whitespace", testChan, "f: 123", sql.NullInt64{}},
		{"folder", testChan, "d:123", sql.NullInt64{}},
		{"empty suffix", testChan, "f:", sql.NullInt64{}},
		{"overflow", testChan, "f:9223372036854775808", sql.NullInt64{}},
	} {
		t.Run(test.name, func(t *testing.T) {
			var got sql.NullInt64
			if err := db.QueryRow(`SELECT `+galleryDirentFileMsgIDSQL("d")+
				` FROM (SELECT ? AS channel_id, ? AS object_id) d`, test.channelID, test.objectID).Scan(&got); err != nil {
				t.Fatal(err)
			}
			if got != test.want {
				t.Fatalf("lookup(%d, %q) = %+v, want %+v", test.channelID, test.objectID, got, test.want)
			}
		})
	}
}

func TestGallerySchemaV3UpgradeReplacesDirentTriggers(t *testing.T) {
	db := newTestDB(t)
	seedGalleryFiles(t, db, testChan, 2)
	// Preserve the v3 lookup so this exercises an installed database upgrade.
	legacyRefresh := func(row string) string {
		channel := row + ".channel_id"
		msgID := `(SELECT f.msg_id FROM files f WHERE f.channel_id=` + channel + ` AND ` + row + `.object_id='f:' || f.msg_id)`
		return galleryDeleteSQL(channel, msgID) + "\n" + galleryInsertSQL(`f.channel_id=`+channel+` AND f.msg_id=`+msgID)
	}
	triggers := []struct {
		name string
		body string
	}{
		{"gallery_items_sync_dirents_insert", `AFTER INSERT ON dirents WHEN NEW.object_kind='file' BEGIN ` + legacyRefresh("NEW") + ` END`},
		{"gallery_items_sync_dirents_update", `AFTER UPDATE ON dirents BEGIN ` + legacyRefresh("OLD") + "\n" + legacyRefresh("NEW") + ` END`},
		{"gallery_items_sync_dirents_delete", `AFTER DELETE ON dirents WHEN OLD.object_kind='file' BEGIN ` + legacyRefresh("OLD") + ` END`},
	}
	for _, trigger := range triggers {
		if _, err := db.Exec(`DROP TRIGGER ` + trigger.name); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`CREATE TRIGGER ` + trigger.name + ` ` + trigger.body); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(`UPDATE gallery_schema_meta SET version=3 WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	if err := EnsureGallerySchema(db); err != nil {
		t.Fatal(err)
	}
	var version int
	if err := db.QueryRow(`SELECT version FROM gallery_schema_meta WHERE id=1`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != 4 {
		t.Fatalf("gallery schema version=%d, want 4", version)
	}
	for _, trigger := range triggers {
		var installed string
		if err := db.QueryRow(`SELECT sql FROM sqlite_schema WHERE name=?`, trigger.name).Scan(&installed); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(installed, "f.msg_id=CAST(substr(") {
			t.Fatalf("upgrade retained unindexed trigger %s", trigger.name)
		}
	}
	if _, err := db.Exec(`INSERT INTO dirents(channel_id,object_id,object_kind,display_name,name_key)
		VALUES(?,'f:1','file','renamed.jpg','renamed.jpg')`, testChan); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE dirents SET display_name='notes.txt',name_key='notes.txt' WHERE channel_id=? AND object_id='f:1'`, testChan); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM gallery_items WHERE channel_id=?`, testChan).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("gallery items after non-media rename=%d, want 1", count)
	}
	if _, err := db.Exec(`DELETE FROM dirents WHERE channel_id=? AND object_id='f:1'`, testChan); err != nil {
		t.Fatal(err)
	}
	// Repeated initialization keeps both the migrated triggers and their output.
	if err := EnsureGallerySchema(db); err != nil {
		t.Fatal(err)
	}
	var name string
	if err := db.QueryRow(`SELECT display_name FROM gallery_items WHERE channel_id=? AND msg_id=1`, testChan).Scan(&name); err != nil {
		t.Fatal(err)
	}
	if name != "image-1.JPG" {
		t.Fatalf("gallery name after deleting dirent=%q", name)
	}
	if err := db.QueryRow(`SELECT item_count FROM gallery_months WHERE channel_id=? AND month_key='2023-11'`, testChan).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("gallery month count after upgrade=%d, want 2", count)
	}
}
