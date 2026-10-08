package core

import (
	"database/sql"
	"testing"

	"TDrive/backend"
	"TDrive/backend/projection"

	_ "modernc.org/sqlite"
)

func TestNewFileServiceWiresUploadConcurrencyLimit(t *testing.T) {
	engine := &Engine{maxUploads: 5}
	service := engine.newFileService()
	if service.MaxConcurrentUploads != 5 {
		t.Fatalf("MaxConcurrentUploads = %d, want 5", service.MaxConcurrentUploads)
	}
}

func TestFreshDatabaseDownloadJournalAfterPersonalDriveMigration(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := backend.TuneSQLite(db); err != nil {
		t.Fatalf("configure database: %v", err)
	}
	previousDB := backend.DB
	backend.DB = db
	t.Cleanup(func() { backend.DB = previousDB })
	if err := projection.EnsureSchema(db); err != nil {
		t.Fatalf("initialize database: %v", err)
	}

	// Engine.New constructs the file service before personal-drive recovery
	// migrates the fresh projection. That same service must remain usable.
	service := (&Engine{}).newFileService()
	if service.CacheNamespace == "" {
		t.Fatal("file service has no cache namespace before personal-drive migration")
	}
	if err := projection.MigratePersonalChannel(db, 12345); err != nil {
		t.Fatalf("migrate personal drive: %v", err)
	}
	var epoch string
	if err := db.QueryRow(`SELECT epoch FROM gallery_epoch WHERE id=1`).Scan(&epoch); err != nil {
		t.Fatalf("read gallery epoch after migration: %v", err)
	}
	if service.CacheNamespace != epoch {
		t.Fatalf("file service namespace changed across migration: got %q, want %q", service.CacheNamespace, epoch)
	}
	jobs, err := service.ListResumableDownloads(t.Context())
	if err != nil {
		t.Fatalf("list download journal after migration: %v", err)
	}
	if len(jobs) != 0 {
		t.Fatalf("fresh download journal has %d jobs, want 0", len(jobs))
	}
}
