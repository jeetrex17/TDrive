package backend

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"
)

var DB *sql.DB

func getDBPath() (string, error) {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}

	appFolder := filepath.Join(configDir, "TDrive")

	if err := os.MkdirAll(appFolder, 0o755); err != nil {
		return "", err
	}

	return filepath.Join(appFolder, "tdrive.db"), nil
}

func InitDB() error {
	path, err := getDBPath()
	if err != nil {
		return err
	}

	db, err := sql.Open("sqlite", path)
	if err != nil {
		return err
	}

	if err := db.Ping(); err != nil {
		_ = db.Close()
		return err
	}

	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(0)

	if _, err := db.Exec(`PRAGMA journal_mode=WAL;`); err != nil {
		_ = db.Close()
		return err
	}
	if _, err := db.Exec(`PRAGMA synchronous=NORMAL;`); err != nil {
		_ = db.Close()
		return err
	}
	if _, err := db.Exec(`PRAGMA busy_timeout=5000;`); err != nil {
		_ = db.Close()
		return err
	}
	if _, err := db.Exec(`PRAGMA foreign_keys=ON;`); err != nil {
		_ = db.Close()
		return err
	}

	DB = db

	return nil
}

func EnsureSchema() error {
	if DB == nil {
		return fmt.Errorf("db not initialized")
	}

	stmts := []string{
		`CREATE TABLE IF NOT EXISTS folders (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			parent_id TEXT NOT NULL DEFAULT ''
		);`,
		`CREATE INDEX IF NOT EXISTS idx_folders_parent ON folders(parent_id);`,
		`CREATE TABLE IF NOT EXISTS files (
			msg_id INTEGER PRIMARY KEY,
			name TEXT NOT NULL,
			size INTEGER NOT NULL,
			parent_id TEXT NOT NULL DEFAULT '',
			upload_time INTEGER NOT NULL
		);`,
		`CREATE INDEX IF NOT EXISTS idx_files_parent ON files(parent_id);`,
	}

	for _, stmt := range stmts {
		if _, err := DB.Exec(stmt); err != nil {
			return err
		}
	}

	return nil
}
