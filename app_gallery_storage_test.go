package main

import (
	"TDrive/backend/thumbnail"
	"database/sql"
	"path/filepath"
	"testing"
)

func TestCatalogStorageBytes(t *testing.T) {
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "catalog.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE items(id INTEGER PRIMARY KEY, name TEXT); INSERT INTO items(name) VALUES('photo')`); err != nil {
		t.Fatal(err)
	}
	bytes, err := catalogStorageBytes(db)
	if err != nil || bytes <= 0 {
		t.Fatalf("bytes=%d err=%v", bytes, err)
	}
}

func TestClearGalleryCachePreservesCatalog(t *testing.T) {
	app, db := setupEncryptionApp(t)
	svc, err := app.requireFileService()
	if err != nil {
		t.Fatal(err)
	}
	svc.Thumbs = thumbnail.NewCache(filepath.Join(t.TempDir(), "thumbs"), 1024)
	if err := svc.Thumbs.Put("photo", []byte("cached thumbnail")); err != nil {
		t.Fatal(err)
	}
	before, err := app.GetGalleryStorage()
	if err != nil || before.CacheBytes == 0 || before.CacheLimit != 1024 {
		t.Fatalf("before=%+v err=%v", before, err)
	}
	after, err := app.ClearGalleryCache()
	if err != nil || after.CacheBytes != 0 || after.CacheEntries != 0 {
		t.Fatalf("after=%+v err=%v", after, err)
	}
	var tables int
	if err := db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type='table'`).Scan(&tables); err != nil || tables == 0 {
		t.Fatalf("catalog lost: tables=%d err=%v", tables, err)
	}
}

func TestGalleryStorageRequiresService(t *testing.T) {
	if _, err := (&App{}).GetGalleryStorage(); err == nil {
		t.Fatal("missing service accepted")
	}
	if _, err := (&App{}).ClearGalleryCache(); err == nil {
		t.Fatal("missing service accepted")
	}
}
