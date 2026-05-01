package projection

import (
	"database/sql"
	"testing"

	_ "modernc.org/sqlite"
)

const (
	migPersonalChan int64 = 12345
)

func newLegacyDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	stmts := []string{
		`CREATE TABLE folders (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			parent_id TEXT NOT NULL DEFAULT ''
		);`,
		`CREATE INDEX idx_folders_parent ON folders(parent_id);`,
		`CREATE TABLE files (
			msg_id INTEGER PRIMARY KEY,
			name TEXT NOT NULL,
			size INTEGER NOT NULL,
			parent_id TEXT NOT NULL DEFAULT '',
			upload_time INTEGER NOT NULL
		);`,
		`CREATE INDEX idx_files_parent ON files(parent_id);`,
	}
	for _, s := range stmts {
		if _, err := db.Exec(s); err != nil {
			t.Fatalf("legacy schema: %v", err)
		}
	}
	return db
}

func TestMigrateAddsSchemaTables(t *testing.T) {
	db := newLegacyDB(t)
	if err := MigratePersonalChannel(db, migPersonalChan); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	for _, name := range []string{"channels", "replay_log", "replay_log_tamper", "backfill_progress", "pending_joins", "schema_version"} {
		var got string
		err := db.QueryRow(`SELECT name FROM sqlite_master WHERE type='table' AND name=?`, name).Scan(&got)
		if err != nil {
			t.Fatalf("table %s missing: %v", name, err)
		}
	}
}

func TestMigrateInsertsPersonalChannel(t *testing.T) {
	db := newLegacyDB(t)
	if err := MigratePersonalChannel(db, migPersonalChan); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	var (
		channelID int64
		kind      string
		title     string
	)
	err := db.QueryRow(`SELECT channel_id, kind, title FROM channels`).Scan(&channelID, &kind, &title)
	if err != nil {
		t.Fatalf("channel row: %v", err)
	}
	if channelID != migPersonalChan {
		t.Fatalf("channel_id = %d", channelID)
	}
	if kind != KindPersonal {
		t.Fatalf("kind = %q", kind)
	}
	if title == "" {
		t.Fatalf("title empty")
	}
}

func TestMigrateMovesLegacyRowsAndPrefixesIDs(t *testing.T) {
	db := newLegacyDB(t)
	if _, err := db.Exec(`INSERT INTO folders (id, name, parent_id) VALUES ('parent-uuid', 'Parent', '')`); err != nil {
		t.Fatalf("seed parent: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO folders (id, name, parent_id) VALUES ('child-uuid', 'Child', 'parent-uuid')`); err != nil {
		t.Fatalf("seed child: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO files (msg_id, name, size, parent_id, upload_time) VALUES (?, ?, ?, ?, ?)`,
		1234, "hello.txt", 200, "parent-uuid", 9999); err != nil {
		t.Fatalf("seed file: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO files (msg_id, name, size, parent_id, upload_time) VALUES (?, ?, ?, ?, ?)`,
		5678, "root.bin", 50, "", 8888); err != nil {
		t.Fatalf("seed root file: %v", err)
	}

	if err := MigratePersonalChannel(db, migPersonalChan); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	type folderRow struct {
		id, name, parent string
		channelID        int64
	}
	rows, err := db.Query(`SELECT id, channel_id, name, parent_id FROM folders ORDER BY id`)
	if err != nil {
		t.Fatalf("query folders: %v", err)
	}
	var folders []folderRow
	for rows.Next() {
		var f folderRow
		if err := rows.Scan(&f.id, &f.channelID, &f.name, &f.parent); err != nil {
			t.Fatalf("scan: %v", err)
		}
		folders = append(folders, f)
	}
	rows.Close()

	if len(folders) != 2 {
		t.Fatalf("folder count = %d", len(folders))
	}
	for _, f := range folders {
		if f.channelID != migPersonalChan {
			t.Fatalf("folder %q channel_id = %d", f.id, f.channelID)
		}
		if !IsFolderID(f.id) {
			t.Fatalf("folder id %q lacks d: prefix", f.id)
		}
		if f.parent != RootParent && !IsFolderID(f.parent) {
			t.Fatalf("folder %q parent_id %q lacks d: prefix", f.id, f.parent)
		}
	}

	type fileRow struct {
		msgID      int64
		channelID  int64
		name, par  string
		size, time int64
	}
	frows, err := db.Query(`SELECT channel_id, msg_id, name, size, parent_id, upload_time FROM files ORDER BY msg_id`)
	if err != nil {
		t.Fatalf("query files: %v", err)
	}
	var files []fileRow
	for frows.Next() {
		var fr fileRow
		if err := frows.Scan(&fr.channelID, &fr.msgID, &fr.name, &fr.size, &fr.par, &fr.time); err != nil {
			t.Fatalf("scan: %v", err)
		}
		files = append(files, fr)
	}
	frows.Close()

	if len(files) != 2 {
		t.Fatalf("file count = %d", len(files))
	}
	for _, fr := range files {
		if fr.channelID != migPersonalChan {
			t.Fatalf("file %d channel_id = %d", fr.msgID, fr.channelID)
		}
		if fr.par != RootParent && !IsFolderID(fr.par) {
			t.Fatalf("file %d parent_id %q lacks d: prefix", fr.msgID, fr.par)
		}
	}
}

func TestMigrateIsIdempotent(t *testing.T) {
	db := newLegacyDB(t)
	if _, err := db.Exec(`INSERT INTO folders (id, name, parent_id) VALUES ('x', 'X', '')`); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := MigratePersonalChannel(db, migPersonalChan); err != nil {
		t.Fatalf("first: %v", err)
	}
	if err := MigratePersonalChannel(db, migPersonalChan); err != nil {
		t.Fatalf("second: %v", err)
	}

	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM folders`).Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 1 {
		t.Fatalf("count = %d", count)
	}
}

func TestMigrateRepairsBuggyV1FoldersPK(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	// Simulate the buggy v1 DB: schema_version=1 with folders.id as the sole
	// primary key. Two channels with the same folder id would have collided
	// under that shape; this test confirms the v1->v2 repair separates them.
	stmts := []string{
		`CREATE TABLE schema_version (version INTEGER PRIMARY KEY);`,
		`INSERT INTO schema_version (version) VALUES (1);`,
		`CREATE TABLE channels (
			channel_id             INTEGER PRIMARY KEY,
			access_hash            INTEGER NOT NULL DEFAULT 0,
			title                  TEXT NOT NULL,
			kind                   TEXT NOT NULL,
			invite_link            TEXT,
			joined_at              INTEGER NOT NULL,
			last_synced_msg        INTEGER NOT NULL DEFAULT 0,
			last_viewed_msg        INTEGER NOT NULL DEFAULT 0,
			has_unseen_content     INTEGER NOT NULL DEFAULT 0,
			initial_sync_done      INTEGER NOT NULL DEFAULT 0,
			personal_backfill_done INTEGER NOT NULL DEFAULT 0
		);`,
		`INSERT INTO channels (channel_id, title, kind, joined_at) VALUES (12345, 'Mine', 'personal', 0);`,
		`CREATE TABLE folders (
			id          TEXT PRIMARY KEY,
			channel_id  INTEGER NOT NULL,
			name        TEXT NOT NULL,
			parent_id   TEXT NOT NULL DEFAULT '',
			tombstoned  INTEGER NOT NULL DEFAULT 0
		);`,
		`INSERT INTO folders (id, channel_id, name, parent_id) VALUES ('d:goa', 12345, 'Goa', '');`,
	}
	for _, s := range stmts {
		if _, err := db.Exec(s); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}

	if err := MigratePersonalChannel(db, 12345); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	pk, err := pragmaPKColsTopLevel(db, "folders")
	if err != nil {
		t.Fatalf("pk inspect: %v", err)
	}
	if len(pk) != 2 || pk[0] != "channel_id" || pk[1] != "id" {
		t.Fatalf("folders PK = %v, want [channel_id id]", pk)
	}

	// Existing row should have survived the repair.
	var name string
	if err := db.QueryRow(`SELECT name FROM folders WHERE channel_id=? AND id=?`, 12345, "d:goa").Scan(&name); err != nil {
		t.Fatalf("query repaired row: %v", err)
	}
	if name != "Goa" {
		t.Fatalf("name = %q want Goa", name)
	}

	var v int
	if err := db.QueryRow(`SELECT version FROM schema_version`).Scan(&v); err != nil {
		t.Fatalf("version: %v", err)
	}
	if v != currentSchemaVersion {
		t.Fatalf("version = %d want %d", v, currentSchemaVersion)
	}
}

func pragmaPKColsTopLevel(db *sql.DB, name string) ([]string, error) {
	rows, err := db.Query(`PRAGMA table_info(` + name + `)`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	type pkCol struct {
		name string
		pk   int
	}
	var cols []pkCol
	for rows.Next() {
		var (
			cid     int
			cname   string
			ctype   string
			notnull int
			dflt    sql.NullString
			pk      int
		)
		if err := rows.Scan(&cid, &cname, &ctype, &notnull, &dflt, &pk); err != nil {
			return nil, err
		}
		if pk > 0 {
			cols = append(cols, pkCol{cname, pk})
		}
	}
	for i := 0; i < len(cols); i++ {
		for j := i + 1; j < len(cols); j++ {
			if cols[j].pk < cols[i].pk {
				cols[i], cols[j] = cols[j], cols[i]
			}
		}
	}
	out := make([]string, 0, len(cols))
	for _, c := range cols {
		out = append(out, c.name)
	}
	return out, nil
}

func TestMigrateOnFreshInstall(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	if err := MigratePersonalChannel(db, migPersonalChan); err != nil {
		t.Fatalf("migrate fresh: %v", err)
	}

	var v int
	if err := db.QueryRow(`SELECT version FROM schema_version`).Scan(&v); err != nil {
		t.Fatalf("version: %v", err)
	}
	if v != currentSchemaVersion {
		t.Fatalf("version = %d, want %d", v, currentSchemaVersion)
	}
}

func TestMigrateAddsEncryptionHintColumn(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	stmts := []string{
		`CREATE TABLE schema_version (version INTEGER PRIMARY KEY);`,
		`INSERT INTO schema_version (version) VALUES (4);`,
		`CREATE TABLE channels (
			channel_id             INTEGER PRIMARY KEY,
			access_hash            INTEGER NOT NULL DEFAULT 0,
			title                  TEXT NOT NULL,
			kind                   TEXT NOT NULL,
			invite_link            TEXT,
			joined_at              INTEGER NOT NULL,
			last_synced_msg        INTEGER NOT NULL DEFAULT 0,
			last_viewed_msg        INTEGER NOT NULL DEFAULT 0,
			has_unseen_content     INTEGER NOT NULL DEFAULT 0,
			initial_sync_done      INTEGER NOT NULL DEFAULT 0,
			personal_backfill_done INTEGER NOT NULL DEFAULT 0
		);`,
		`INSERT INTO channels (channel_id, title, kind, joined_at) VALUES (12345, 'Mine', 'personal', 0);`,
		`CREATE TABLE encryption (
			channel_id          INTEGER PRIMARY KEY,
			enabled             INTEGER NOT NULL DEFAULT 0,
			kdf_salt            BLOB    NOT NULL,
			kdf_params_json     TEXT    NOT NULL,
			wrapped_master_key  BLOB    NOT NULL,
			key_check           BLOB    NOT NULL,
			created_at          INTEGER NOT NULL,
			version             INTEGER NOT NULL DEFAULT 1
		);`,
		`INSERT INTO encryption (channel_id, enabled, kdf_salt, kdf_params_json, wrapped_master_key, key_check, created_at, version)
		 VALUES (12345, 1, X'01', '{}', X'02', X'03', 99, 1);`,
	}
	for _, s := range stmts {
		if _, err := db.Exec(s); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}

	if err := MigratePersonalChannel(db, 12345); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	cols, err := topLevelColumnSet(db, "encryption")
	if err != nil {
		t.Fatalf("columns: %v", err)
	}
	if _, ok := cols["hint"]; !ok {
		t.Fatalf("hint column missing")
	}
	var hint string
	if err := db.QueryRow(`SELECT hint FROM encryption WHERE channel_id=12345`).Scan(&hint); err != nil {
		t.Fatalf("query hint: %v", err)
	}
	if hint != "" {
		t.Fatalf("hint = %q, want empty", hint)
	}
}

func topLevelColumnSet(db *sql.DB, table string) (map[string]bool, error) {
	rows, err := db.Query(`PRAGMA table_info(` + table + `)`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[string]bool)
	for rows.Next() {
		var (
			cid     int
			name    string
			typ     string
			notnull int
			dflt    sql.NullString
			pk      int
		)
		if err := rows.Scan(&cid, &name, &typ, &notnull, &dflt, &pk); err != nil {
			return nil, err
		}
		out[name] = true
	}
	return out, rows.Err()
}
