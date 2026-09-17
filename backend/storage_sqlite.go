package backend

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	"TDrive/backend/datadir"
	"TDrive/backend/projection"

	_ "modernc.org/sqlite"
)

var DB *sql.DB

const (
	privateFileMode os.FileMode = 0o600
)

func getDBPath() (string, error) {
	appFolder, err := datadir.Dir()
	if err != nil {
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
	_ = os.Chmod(path, privateFileMode)

	// One pooled connection, kept for the process's lifetime. SQLite admits a
	// single writer, and letting the pool hand concurrent writers their own
	// connections turned overlapping upload commits into SQLITE_BUSY failures
	// (7be2c20); serializing every statement here is what fixed that race.
	//
	// Raising the cap is not just a tuning knob. Every pragma below is set
	// through this handle and all but journal_mode are per-connection, so a
	// second connection would come up with foreign keys off and no busy
	// backoff. Those have to move into the DSN before the cap can move.
	//
	// The cost of one connection is that whoever holds it blocks every other
	// query in the app, so no code path may keep a transaction open across
	// network I/O — see sync.applyInitialHistoryPlan.
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
	if _, err := db.Exec(`PRAGMA temp_store=MEMORY;`); err != nil {
		_ = db.Close()
		return err
	}
	if _, err := db.Exec(`PRAGMA cache_size=-32768;`); err != nil {
		_ = db.Close()
		return err
	}
	// mmap_size is a read optimization on drivers/platforms that support it.
	// Treat it as opportunistic so startup does not depend on mmap support.
	_, _ = db.Exec(`PRAGMA mmap_size=134217728;`)

	DB = db

	return nil
}

// EnsureSchema creates the projection metadata tables (channels, replay_log,
// schema_version, etc.). It is safe to run on every startup.
//
// The folders/files tables are NOT created here — they're created either by
// MigratePersonalChannel for a legacy DB or by createFreshFolders/Files
// inside the migration for a fresh install. Calling code is expected to run
// MigratePersonalChannel as soon as the personal channel ID is known.
func EnsureSchema() error {
	if DB == nil {
		return fmt.Errorf("db not initialized")
	}
	return projection.EnsureSchema(DB)
}

// MigratePersonalChannel finalizes the schema and reshapes any legacy
// folders/files rows so they live under the personal channel. Idempotent.
func MigratePersonalChannel(personalChannelID int64) error {
	if DB == nil {
		return fmt.Errorf("db not initialized")
	}
	return projection.MigratePersonalChannel(DB, personalChannelID)
}
