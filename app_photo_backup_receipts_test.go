package main

import (
	"context"
	"database/sql"
	"reflect"
	"testing"

	_ "modernc.org/sqlite"
)

func photoBackupProjectionDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err := db.Exec(`
		CREATE TABLE files(channel_id INTEGER, msg_id INTEGER, tombstoned INTEGER, PRIMARY KEY(channel_id,msg_id));
		CREATE TABLE trash_entries(channel_id INTEGER, object_id TEXT, PRIMARY KEY(channel_id,object_id));
	`); err != nil {
		t.Fatal(err)
	}
	return db
}

func TestPhotoBackupLostFilesCountsOnlyUnrecoverableUploads(t *testing.T) {
	db := photoBackupProjectionDB(t)
	if _, err := db.Exec(`
		INSERT INTO files VALUES(41,100,0),(41,101,1),(41,102,1),(41,105,0);
		INSERT INTO trash_entries VALUES(41,'f:101');
		INSERT INTO files VALUES(42,102,1);
	`); err != nil {
		t.Fatal(err)
	}
	lost, err := photoBackupLostFiles(context.Background(), db, 41, []int64{100, 101, 102, 103, 105, 900})
	if err != nil {
		t.Fatal(err)
	}
	// 100 and 105 are live, 101 is in the trash and still the user's to
	// restore, 900 is above everything the projection has indexed. 102 was
	// purged and 103 never made it into this drive at all.
	if !reflect.DeepEqual(lost, []int64{102, 103}) {
		t.Fatalf("lost=%v", lost)
	}
}

func TestPhotoBackupLostFilesTreatsAnUnbuiltIndexAsNothingLost(t *testing.T) {
	db := photoBackupProjectionDB(t)
	if _, err := db.Exec(`INSERT INTO files VALUES(42,1,0)`); err != nil {
		t.Fatal(err)
	}
	// A drive the projection has never populated must never be read as proof
	// that every backed-up file disappeared.
	lost, err := photoBackupLostFiles(context.Background(), db, 41, []int64{1, 2, 3})
	if err != nil || lost != nil {
		t.Fatalf("lost=%v err=%v", lost, err)
	}
	if lost, err := photoBackupLostFiles(context.Background(), db, 42, nil); err != nil || lost != nil {
		t.Fatalf("empty batch lost=%v err=%v", lost, err)
	}
}
