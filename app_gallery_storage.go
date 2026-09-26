package main

import (
	"database/sql"
	"fmt"
	"os"
)

// GalleryStorage separates disposable media from the durable local catalog.
// Saved downloads are user files and are never included in cache cleanup.
type GalleryStorage struct {
	CacheBytes   int64 `json:"cache_bytes"`
	CacheLimit   int64 `json:"cache_limit"`
	CacheEntries int   `json:"cache_entries"`
	CatalogBytes int64 `json:"catalog_bytes"`
}

func (a *App) GetGalleryStorage() (GalleryStorage, error) {
	svc, err := a.requireFileService()
	if err != nil {
		return GalleryStorage{}, err
	}
	used, limit, entries := svc.Thumbs.Usage()
	catalog, err := catalogStorageBytes(svc.DB)
	if err != nil {
		return GalleryStorage{}, err
	}
	return GalleryStorage{CacheBytes: used, CacheLimit: limit, CacheEntries: entries, CatalogBytes: catalog}, nil
}

func (a *App) ClearGalleryCache() (GalleryStorage, error) {
	svc, err := a.requireFileService()
	if err != nil {
		return GalleryStorage{}, err
	}
	if err := svc.Thumbs.Clear(); err != nil {
		return GalleryStorage{}, fmt.Errorf("could not clear all photo cache files: %w", err)
	}
	return a.GetGalleryStorage()
}

func catalogStorageBytes(db *sql.DB) (int64, error) {
	if db == nil {
		return 0, nil
	}
	rows, err := db.Query(`PRAGMA database_list`)
	if err != nil {
		return 0, err
	}
	var path string
	for rows.Next() {
		var sequence int
		var name, filename string
		if err := rows.Scan(&sequence, &name, &filename); err != nil {
			rows.Close()
			return 0, err
		}
		if name == "main" {
			path = filename
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil || path == "" {
		return 0, err
	}
	var total int64
	// WAL and shared-memory files are part of actual local catalog storage.
	// They may disappear during a checkpoint, which is a normal race.
	for _, suffix := range []string{"", "-wal", "-shm"} {
		info, err := os.Stat(path + suffix)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return 0, fmt.Errorf("read catalog storage: %w", err)
		}
		total += info.Size()
	}
	return total, nil
}
