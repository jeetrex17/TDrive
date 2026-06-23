package projection

import (
	"database/sql"
	"fmt"
	"time"
)

const currentSchemaVersion = 7

func EnsureSchema(db *sql.DB) error {
	if db == nil {
		return fmt.Errorf("projection: db is nil")
	}

	stmts := []string{
		`CREATE TABLE IF NOT EXISTS schema_version (
			version INTEGER PRIMARY KEY
		);`,
		`CREATE TABLE IF NOT EXISTS channels (
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
		`CREATE TABLE IF NOT EXISTS replay_log (
			channel_id      INTEGER NOT NULL,
			msg_id          INTEGER NOT NULL,
			op_type         TEXT NOT NULL,
			op_payload_json TEXT NOT NULL,
			raw_header      TEXT NOT NULL,
			first_seen_hash TEXT NOT NULL,
			actor_user_id   INTEGER NOT NULL DEFAULT 0,
			seen_at         INTEGER NOT NULL,
			PRIMARY KEY (channel_id, msg_id)
		);`,
		`CREATE INDEX IF NOT EXISTS idx_replay_log_channel_msg ON replay_log(channel_id, msg_id);`,
		`CREATE TABLE IF NOT EXISTS replay_log_tamper (
			channel_id  INTEGER NOT NULL,
			msg_id      INTEGER NOT NULL,
			old_hash    TEXT NOT NULL,
			new_hash    TEXT NOT NULL,
			detected_at INTEGER NOT NULL,
			PRIMARY KEY (channel_id, msg_id)
		);`,
		`CREATE TABLE IF NOT EXISTS replay_log_rejects (
			channel_id  INTEGER NOT NULL,
			msg_id      INTEGER NOT NULL,
			error       TEXT NOT NULL,
			detected_at INTEGER NOT NULL,
			PRIMARY KEY (channel_id, msg_id)
		);`,
		`CREATE TABLE IF NOT EXISTS backfill_progress (
			channel_id    INTEGER PRIMARY KEY,
			cursor_obj_id TEXT NOT NULL,
			cursor_kind   TEXT NOT NULL,
			started_at    INTEGER NOT NULL,
			updated_at    INTEGER NOT NULL
		);`,
		`CREATE TABLE IF NOT EXISTS pending_joins (
			invite_hash     TEXT PRIMARY KEY,
			invite_link     TEXT NOT NULL,
			title           TEXT NOT NULL DEFAULT '',
			requested_at    INTEGER NOT NULL,
			last_checked_at INTEGER NOT NULL DEFAULT 0,
			status          TEXT NOT NULL,
			last_error      TEXT NOT NULL DEFAULT ''
		);`,
		`CREATE TABLE IF NOT EXISTS encryption (
			channel_id          INTEGER PRIMARY KEY,
			enabled             INTEGER NOT NULL DEFAULT 0,
			kdf_salt            BLOB    NOT NULL,
			kdf_params_json     TEXT    NOT NULL,
			wrapped_master_key  BLOB    NOT NULL,
			key_check           BLOB    NOT NULL,
			hint                TEXT    NOT NULL DEFAULT '',
			created_at          INTEGER NOT NULL,
			version             INTEGER NOT NULL DEFAULT 1
		);`,
		`CREATE TABLE IF NOT EXISTS file_parts (
			channel_id   INTEGER NOT NULL,
			upload_uuid  TEXT NOT NULL,
			part_index   INTEGER NOT NULL,
			msg_id       INTEGER NOT NULL,
			size         INTEGER NOT NULL DEFAULT 0,
			PRIMARY KEY (channel_id, upload_uuid, part_index)
			);`,
		`CREATE INDEX IF NOT EXISTS idx_file_parts_msg ON file_parts(channel_id, msg_id);`,
		`CREATE TABLE IF NOT EXISTS pending_part_cleanup (
				channel_id  INTEGER NOT NULL,
				msg_id      INTEGER NOT NULL,
				PRIMARY KEY (channel_id, msg_id)
			);`,
	}

	for _, s := range stmts {
		if _, err := db.Exec(s); err != nil {
			return fmt.Errorf("projection: ensure schema: %w", err)
		}
	}
	if err := ensureCompatibleIndexes(db); err != nil {
		return err
	}
	return nil
}

func ensureCompatibleIndexes(db *sql.DB) error {
	if dbTableHasColumns(db, "files", "channel_id", "uploader_user_id", "upload_time", "msg_id", "tombstoned") {
		if _, err := db.Exec(`CREATE INDEX IF NOT EXISTS idx_files_uploader_latest
			ON files(channel_id, uploader_user_id, upload_time DESC, msg_id DESC)
			WHERE tombstoned = 0 AND uploader_user_id > 0;`); err != nil {
			return fmt.Errorf("projection: create files uploader index: %w", err)
		}
	}
	if dbTableHasColumns(db, "files", "channel_id", "upload_time", "msg_id", "tombstoned") {
		if _, err := db.Exec(`CREATE INDEX IF NOT EXISTS idx_files_channel_upload_time
			ON files(channel_id, upload_time DESC, msg_id DESC)
			WHERE tombstoned = 0;`); err != nil {
			return fmt.Errorf("projection: create files upload-time index: %w", err)
		}
	}
	if dbTableHasColumns(db, "files", "channel_id", "name", "tombstoned") {
		if _, err := db.Exec(`CREATE INDEX IF NOT EXISTS idx_files_channel_name_nocase
			ON files(channel_id, name COLLATE NOCASE)
			WHERE tombstoned = 0;`); err != nil {
			return fmt.Errorf("projection: create files name index: %w", err)
		}
	}
	if dbTableHasColumns(db, "folders", "channel_id", "name", "tombstoned") {
		if _, err := db.Exec(`CREATE INDEX IF NOT EXISTS idx_folders_channel_name_nocase
			ON folders(channel_id, name COLLATE NOCASE)
			WHERE tombstoned = 0;`); err != nil {
			return fmt.Errorf("projection: create folders name index: %w", err)
		}
	}
	return nil
}

func dbTableHasColumns(db *sql.DB, table string, names ...string) bool {
	rows, err := db.Query(fmt.Sprintf(`PRAGMA table_info(%s)`, table))
	if err != nil {
		return false
	}
	defer rows.Close()

	needed := make(map[string]struct{}, len(names))
	for _, name := range names {
		needed[name] = struct{}{}
	}
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
			return false
		}
		delete(needed, cname)
	}
	return rows.Err() == nil && len(needed) == 0
}

func currentVersion(db *sql.DB) (int, error) {
	var v int
	err := db.QueryRow(`SELECT COALESCE(MAX(version), 0) FROM schema_version`).Scan(&v)
	if err != nil {
		return 0, err
	}
	return v, nil
}

func MigratePersonalChannel(db *sql.DB, personalChannelID int64) error {
	if db == nil {
		return fmt.Errorf("projection: db is nil")
	}
	if personalChannelID == 0 {
		return fmt.Errorf("projection: personal channel id required")
	}

	if err := EnsureSchema(db); err != nil {
		return err
	}

	v, err := currentVersion(db)
	if err != nil {
		return fmt.Errorf("projection: read schema version: %w", err)
	}
	if v >= currentSchemaVersion {
		return nil
	}

	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("projection: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if err := upsertChannel(tx, personalChannelID); err != nil {
		return err
	}

	if v < 1 {
		if err := reshapeFolders(tx, personalChannelID); err != nil {
			return err
		}
		if err := reshapeFiles(tx, personalChannelID); err != nil {
			return err
		}
	}

	if v < 2 {
		// v1 shipped folders with PRIMARY KEY (id) by mistake. Rebuild it as
		// PRIMARY KEY (channel_id, id) so identical folder ids in different
		// drives don't collide.
		if err := repairFoldersPK(tx); err != nil {
			return err
		}
	}
	if v < 3 {
		// pending_joins is created by EnsureSchema above. Nothing to backfill.
	}
	if v < 4 {
		if err := addEncryptionColumnsToFiles(tx); err != nil {
			return err
		}
	}
	if v < 5 {
		if err := addEncryptionHintColumn(tx); err != nil {
			return err
		}
	}
	if v < 6 {
		// replay_log_rejects is created by EnsureSchema above. Nothing to backfill.
	}
	if v < 7 {
		if err := addMultipartColumnsToFiles(tx); err != nil {
			return err
		}
	}

	if _, err := tx.Exec(`DELETE FROM schema_version`); err != nil {
		return fmt.Errorf("projection: clear schema version: %w", err)
	}
	if _, err := tx.Exec(`INSERT INTO schema_version (version) VALUES (?)`, currentSchemaVersion); err != nil {
		return fmt.Errorf("projection: write schema version: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("projection: commit migration: %w", err)
	}
	return nil
}

func repairFoldersPK(tx *sql.Tx) error {
	if !tableExists(tx, "folders") {
		return createFreshFolders(tx)
	}

	// Already on the composite PK shape? Skip. Detect by inspecting pk flags.
	pkCols, err := tablePKCols(tx, "folders")
	if err != nil {
		return err
	}
	if len(pkCols) == 2 && pkCols[0] == "channel_id" && pkCols[1] == "id" {
		return nil
	}

	if _, err := tx.Exec(`DROP INDEX IF EXISTS idx_folders_channel_parent`); err != nil {
		return fmt.Errorf("projection: drop folders parent index for pk repair: %w", err)
	}
	if _, err := tx.Exec(`ALTER TABLE folders RENAME TO folders_pk_old`); err != nil {
		return fmt.Errorf("projection: rename folders for pk repair: %w", err)
	}
	if err := createFreshFolders(tx); err != nil {
		return err
	}
	if _, err := tx.Exec(`
		INSERT INTO folders (channel_id, id, name, parent_id, tombstoned)
		SELECT channel_id, id, name, parent_id, COALESCE(tombstoned, 0) FROM folders_pk_old
	`); err != nil {
		return fmt.Errorf("projection: copy folders for pk repair: %w", err)
	}
	if _, err := tx.Exec(`DROP TABLE folders_pk_old`); err != nil {
		return fmt.Errorf("projection: drop folders_pk_old: %w", err)
	}
	return nil
}

func upsertChannel(tx *sql.Tx, channelID int64) error {
	now := time.Now().Unix()
	_, err := tx.Exec(`
		INSERT INTO channels (channel_id, access_hash, title, kind, joined_at, personal_backfill_done)
		VALUES (?, 0, 'My Drive', ?, ?, 0)
		ON CONFLICT(channel_id) DO NOTHING
	`, channelID, KindPersonal, now)
	if err != nil {
		return fmt.Errorf("projection: insert personal channel: %w", err)
	}
	return nil
}

func reshapeFolders(tx *sql.Tx, channelID int64) error {
	if !tableExists(tx, "folders") {
		return createFreshFolders(tx)
	}

	cols, err := tableColumnSet(tx, "folders")
	if err != nil {
		return err
	}
	if _, hasChannel := cols["channel_id"]; hasChannel {
		return nil
	}

	if _, err := tx.Exec(`ALTER TABLE folders RENAME TO folders_old`); err != nil {
		return fmt.Errorf("projection: rename folders: %w", err)
	}

	if err := createFreshFolders(tx); err != nil {
		return err
	}

	rows, err := tx.Query(`SELECT id, name, parent_id FROM folders_old`)
	if err != nil {
		return fmt.Errorf("projection: read legacy folders: %w", err)
	}
	defer rows.Close()

	type fRow struct{ id, name, parent string }
	var legacy []fRow
	for rows.Next() {
		var r fRow
		if err := rows.Scan(&r.id, &r.name, &r.parent); err != nil {
			return fmt.Errorf("projection: scan legacy folder: %w", err)
		}
		legacy = append(legacy, r)
	}
	if err := rows.Err(); err != nil {
		return err
	}

	for _, r := range legacy {
		newID := FolderIDPrefix + r.id
		newParent := RootParent
		if r.parent != "" {
			newParent = FolderIDPrefix + r.parent
		}
		if _, err := tx.Exec(`
			INSERT INTO folders (id, channel_id, name, parent_id, tombstoned)
			VALUES (?, ?, ?, ?, 0)
		`, newID, channelID, r.name, newParent); err != nil {
			return fmt.Errorf("projection: insert migrated folder: %w", err)
		}
	}

	if _, err := tx.Exec(`DROP TABLE folders_old`); err != nil {
		return fmt.Errorf("projection: drop folders_old: %w", err)
	}
	return nil
}

func reshapeFiles(tx *sql.Tx, channelID int64) error {
	if !tableExists(tx, "files") {
		return createFreshFiles(tx)
	}

	cols, err := tableColumnSet(tx, "files")
	if err != nil {
		return err
	}
	if _, hasChannel := cols["channel_id"]; hasChannel {
		return nil
	}

	if _, err := tx.Exec(`ALTER TABLE files RENAME TO files_old`); err != nil {
		return fmt.Errorf("projection: rename files: %w", err)
	}

	if err := createFreshFiles(tx); err != nil {
		return err
	}

	rows, err := tx.Query(`SELECT msg_id, name, size, parent_id, upload_time FROM files_old`)
	if err != nil {
		return fmt.Errorf("projection: read legacy files: %w", err)
	}
	defer rows.Close()

	type fRow struct {
		msgID      int64
		name       string
		size       int64
		parent     string
		uploadTime int64
	}
	var legacy []fRow
	for rows.Next() {
		var r fRow
		if err := rows.Scan(&r.msgID, &r.name, &r.size, &r.parent, &r.uploadTime); err != nil {
			return fmt.Errorf("projection: scan legacy file: %w", err)
		}
		legacy = append(legacy, r)
	}
	if err := rows.Err(); err != nil {
		return err
	}

	for _, r := range legacy {
		newParent := RootParent
		if r.parent != "" {
			newParent = FolderIDPrefix + r.parent
		}
		if _, err := tx.Exec(`
			INSERT INTO files (channel_id, msg_id, name, size, parent_id, upload_time, uploader_user_id, tombstoned)
			VALUES (?, ?, ?, ?, ?, ?, 0, 0)
		`, channelID, r.msgID, r.name, r.size, newParent, r.uploadTime); err != nil {
			return fmt.Errorf("projection: insert migrated file: %w", err)
		}
	}

	if _, err := tx.Exec(`DROP TABLE files_old`); err != nil {
		return fmt.Errorf("projection: drop files_old: %w", err)
	}
	return nil
}

func createFreshFolders(tx *sql.Tx) error {
	stmts := []string{
		`CREATE TABLE folders (
			channel_id  INTEGER NOT NULL,
			id          TEXT NOT NULL,
			name        TEXT NOT NULL,
			parent_id   TEXT NOT NULL DEFAULT '',
			tombstoned  INTEGER NOT NULL DEFAULT 0,
			PRIMARY KEY (channel_id, id)
		);`,
		`CREATE INDEX IF NOT EXISTS idx_folders_channel_parent
			ON folders(channel_id, parent_id) WHERE tombstoned = 0;`,
		`CREATE INDEX IF NOT EXISTS idx_folders_channel_name_nocase
			ON folders(channel_id, name COLLATE NOCASE)
			WHERE tombstoned = 0;`,
	}
	for _, s := range stmts {
		if _, err := tx.Exec(s); err != nil {
			return fmt.Errorf("projection: create folders: %w", err)
		}
	}
	return nil
}

func createFreshFiles(tx *sql.Tx) error {
	stmts := []string{
		`CREATE TABLE files (
			channel_id          INTEGER NOT NULL,
			msg_id              INTEGER NOT NULL,
			name                TEXT NOT NULL,
			size                INTEGER NOT NULL,
			parent_id           TEXT NOT NULL DEFAULT '',
			upload_time         INTEGER NOT NULL,
			uploader_user_id    INTEGER NOT NULL DEFAULT 0,
			tombstoned          INTEGER NOT NULL DEFAULT 0,
			encrypted           INTEGER NOT NULL DEFAULT 0,
			plaintext_size      INTEGER NOT NULL DEFAULT 0,
			encryption_version  INTEGER NOT NULL DEFAULT 0,
			upload_uuid         TEXT NOT NULL DEFAULT '',
			part_count          INTEGER NOT NULL DEFAULT 0,
			PRIMARY KEY (channel_id, msg_id)
		);`,
		`CREATE INDEX IF NOT EXISTS idx_files_channel_parent
			ON files(channel_id, parent_id) WHERE tombstoned = 0;`,
		`CREATE INDEX IF NOT EXISTS idx_files_uploader_latest
			ON files(channel_id, uploader_user_id, upload_time DESC, msg_id DESC)
			WHERE tombstoned = 0 AND uploader_user_id > 0;`,
		`CREATE INDEX IF NOT EXISTS idx_files_channel_upload_time
			ON files(channel_id, upload_time DESC, msg_id DESC)
			WHERE tombstoned = 0;`,
		`CREATE INDEX IF NOT EXISTS idx_files_channel_name_nocase
			ON files(channel_id, name COLLATE NOCASE)
			WHERE tombstoned = 0;`,
	}
	for _, s := range stmts {
		if _, err := tx.Exec(s); err != nil {
			return fmt.Errorf("projection: create files: %w", err)
		}
	}
	return nil
}

// addEncryptionColumnsToFiles tops up an existing files table with the
// encryption metadata columns. SQLite ADD COLUMN is constant-time and
// non-destructive, so this is a fast v3→v4 migration. Skips when the
// files table doesn't exist yet — that path runs createFreshFiles
// during the v0→v1 reshape, which already includes these columns.
func addEncryptionColumnsToFiles(tx *sql.Tx) error {
	if !tableExists(tx, "files") {
		return nil
	}
	cols, err := tableColumnSet(tx, "files")
	if err != nil {
		return err
	}
	stmts := map[string]string{
		"encrypted":          `ALTER TABLE files ADD COLUMN encrypted INTEGER NOT NULL DEFAULT 0`,
		"plaintext_size":     `ALTER TABLE files ADD COLUMN plaintext_size INTEGER NOT NULL DEFAULT 0`,
		"encryption_version": `ALTER TABLE files ADD COLUMN encryption_version INTEGER NOT NULL DEFAULT 0`,
	}
	for col, stmt := range stmts {
		if _, present := cols[col]; present {
			continue
		}
		if _, err := tx.Exec(stmt); err != nil {
			return fmt.Errorf("projection: add files.%s: %w", col, err)
		}
	}
	return nil
}

func addEncryptionHintColumn(tx *sql.Tx) error {
	if !tableExists(tx, "encryption") {
		return nil
	}
	cols, err := tableColumnSet(tx, "encryption")
	if err != nil {
		return err
	}
	if _, present := cols["hint"]; present {
		return nil
	}
	if _, err := tx.Exec(`ALTER TABLE encryption ADD COLUMN hint TEXT NOT NULL DEFAULT ''`); err != nil {
		return fmt.Errorf("projection: add encryption.hint: %w", err)
	}
	return nil
}

// addMultipartColumnsToFiles tops up an existing files table with the multipart
// large-file columns. SQLite ADD COLUMN is constant-time and non-destructive.
// The file_parts table itself is created by EnsureSchema (CREATE IF NOT EXISTS),
// so this only handles the files-table columns. Idempotent. Skips when files
// doesn't exist yet — the v0→v1 reshape's createFreshFiles already includes them.
func addMultipartColumnsToFiles(tx *sql.Tx) error {
	if !tableExists(tx, "files") {
		return nil
	}
	cols, err := tableColumnSet(tx, "files")
	if err != nil {
		return err
	}
	stmts := map[string]string{
		"upload_uuid": `ALTER TABLE files ADD COLUMN upload_uuid TEXT NOT NULL DEFAULT ''`,
		"part_count":  `ALTER TABLE files ADD COLUMN part_count INTEGER NOT NULL DEFAULT 0`,
	}
	for col, stmt := range stmts {
		if _, present := cols[col]; present {
			continue
		}
		if _, err := tx.Exec(stmt); err != nil {
			return fmt.Errorf("projection: add files.%s: %w", col, err)
		}
	}
	return nil
}

func tableExists(tx *sql.Tx, name string) bool {
	var got string
	err := tx.QueryRow(`SELECT name FROM sqlite_master WHERE type='table' AND name=?`, name).Scan(&got)
	return err == nil && got == name
}

func tablePKCols(tx *sql.Tx, name string) ([]string, error) {
	rows, err := tx.Query(fmt.Sprintf(`PRAGMA table_info(%s)`, name))
	if err != nil {
		return nil, fmt.Errorf("projection: pragma table_info: %w", err)
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
			return nil, fmt.Errorf("projection: scan table_info: %w", err)
		}
		if pk > 0 {
			cols = append(cols, pkCol{cname, pk})
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
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

func tableColumnSet(tx *sql.Tx, name string) (map[string]struct{}, error) {
	rows, err := tx.Query(fmt.Sprintf(`PRAGMA table_info(%s)`, name))
	if err != nil {
		return nil, fmt.Errorf("projection: pragma table_info: %w", err)
	}
	defer rows.Close()

	out := make(map[string]struct{})
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
			return nil, fmt.Errorf("projection: scan table_info: %w", err)
		}
		out[cname] = struct{}{}
	}
	return out, rows.Err()
}
