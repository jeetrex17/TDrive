package projection

import (
	"database/sql"
	"errors"
	"fmt"
)

func migrateWritableProjection(tx *sql.Tx) error {
	if !tableExists(tx, "folders") {
		if err := createFreshFolders(tx); err != nil {
			return err
		}
	}
	if !tableExists(tx, "files") {
		if err := createFreshFiles(tx); err != nil {
			return err
		}
	}
	if err := addWritableColumns(tx, "folders", map[string]string{
		"revision": `ALTER TABLE folders ADD COLUMN revision INTEGER NOT NULL DEFAULT 1`,
	}); err != nil {
		return err
	}
	if err := addWritableColumns(tx, "files", map[string]string{
		"content_msg_id": `ALTER TABLE files ADD COLUMN content_msg_id INTEGER NOT NULL DEFAULT 0`,
		"content_hash":   `ALTER TABLE files ADD COLUMN content_hash TEXT NOT NULL DEFAULT ''`,
		"revision":       `ALTER TABLE files ADD COLUMN revision INTEGER NOT NULL DEFAULT 1`,
	}); err != nil {
		return err
	}

	// Before writable commits, the logical identity and Telegram body identity
	// were the same for single-message files. Multipart manifests already carry
	// upload_uuid and deliberately keep content_msg_id at zero.
	if _, err := tx.Exec(`
		UPDATE files SET content_msg_id = msg_id
		WHERE content_msg_id = 0 AND upload_uuid = ''
	`); err != nil {
		return fmt.Errorf("projection: backfill file content ids: %w", err)
	}
	if err := backfillLegacyRevisionsFromReplay(tx); err != nil {
		return err
	}
	if err := backfillDirents(tx); err != nil {
		return err
	}
	if _, err := tx.Exec(`
		INSERT OR IGNORE INTO file_revisions
		  (channel_id, file_msg_id, revision, content_msg_id, upload_uuid,
		   part_count, size, plaintext_size, content_hash, encrypted,
		   encryption_version, committed_msg_id, actor_user_id, op_id)
		SELECT channel_id, msg_id, revision, content_msg_id, upload_uuid,
		       part_count, size, plaintext_size, content_hash, encrypted,
		       encryption_version, msg_id, uploader_user_id, ''
		FROM files
	`); err != nil {
		return fmt.Errorf("projection: backfill file revisions: %w", err)
	}
	return nil
}

func addWritableColumns(tx *sql.Tx, table string, statements map[string]string) error {
	cols, err := tableColumnSet(tx, table)
	if err != nil {
		return err
	}
	for column, statement := range statements {
		if _, exists := cols[column]; exists {
			continue
		}
		if _, err := tx.Exec(statement); err != nil {
			return fmt.Errorf("projection: add %s.%s: %w", table, column, err)
		}
	}
	return nil
}

type legacyDirentRow struct {
	channelID   int64
	objectID    string
	objectKind  string
	parentID    string
	displayName string
	revision    int64
	tombstoned  int
}

func backfillDirents(tx *sql.Tx) error {
	rows, err := tx.Query(`
		SELECT channel_id, id, 'folder', parent_id, name, revision, tombstoned
		FROM folders
		UNION ALL
		SELECT channel_id, 'f:' || CAST(msg_id AS TEXT), 'file', parent_id, name, revision, tombstoned
		FROM files
		ORDER BY 1, 4, 3, 2
	`)
	if err != nil {
		return fmt.Errorf("projection: read legacy namespace: %w", err)
	}
	var entries []legacyDirentRow
	for rows.Next() {
		var entry legacyDirentRow
		if err := rows.Scan(
			&entry.channelID,
			&entry.objectID, &entry.objectKind, &entry.parentID,
			&entry.displayName, &entry.revision, &entry.tombstoned,
		); err != nil {
			_ = rows.Close()
			return fmt.Errorf("projection: scan legacy namespace: %w", err)
		}
		entries = append(entries, entry)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return fmt.Errorf("projection: iterate legacy namespace: %w", err)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("projection: close legacy namespace: %w", err)
	}

	for _, entry := range entries {
		if err := insertLegacyDirent(tx, entry); err != nil {
			return err
		}
	}
	return nil
}

func insertLegacyDirent(tx *sql.Tx, entry legacyDirentRow) error {
	name := legacyPortableName(entry.displayName, entry.objectKind, entry.objectID)
	for attempt := -1; ; attempt++ {
		if attempt >= 0 {
			name = legacyCollisionAlias(entry.displayName, entry.objectKind, entry.objectID, attempt)
		}
		key, err := CanonicalNameKey(name)
		if err != nil {
			return fmt.Errorf("projection: canonicalize legacy alias %q: %w", name, err)
		}
		if entry.tombstoned == 0 {
			var occupied int
			err := tx.QueryRow(`
				SELECT 1 FROM dirents
				WHERE channel_id=? AND parent_id=? AND name_key=? AND tombstoned=0
				LIMIT 1
			`, entry.channelID, entry.parentID, key).Scan(&occupied)
			if err == nil {
				continue
			}
			if err != nil && !errors.Is(err, sql.ErrNoRows) {
				return fmt.Errorf("projection: inspect legacy sibling: %w", err)
			}
		}
		_, err = tx.Exec(`
			INSERT INTO dirents
			  (channel_id, object_id, object_kind, parent_id, display_name,
			   name_key, revision, tombstoned)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(channel_id, object_id) DO NOTHING
		`, entry.channelID, entry.objectID, entry.objectKind, entry.parentID,
			name, key, entry.revision, entry.tombstoned)
		if err != nil {
			return fmt.Errorf("projection: insert legacy dirent %s: %w", entry.objectID, err)
		}
		return nil
	}
}

func syncLegacyFolderDirent(tx *sql.Tx, channelID int64, folderID string) error {
	entry := legacyDirentRow{
		channelID: channelID, objectID: folderID, objectKind: "folder",
	}
	err := tx.QueryRow(`
		SELECT parent_id, name, revision, tombstoned
		FROM folders WHERE channel_id=? AND id=?
	`, channelID, folderID).Scan(
		&entry.parentID, &entry.displayName, &entry.revision, &entry.tombstoned,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("projection: read folder namespace row: %w", err)
	}
	return replaceLegacyDirent(tx, entry)
}

func syncLegacyFileDirent(tx *sql.Tx, channelID, fileMsgID int64) error {
	entry := legacyDirentRow{
		channelID:  channelID,
		objectID:   FileIDPrefix + fmt.Sprint(fileMsgID),
		objectKind: "file",
	}
	err := tx.QueryRow(`
		SELECT parent_id, name, revision, tombstoned
		FROM files WHERE channel_id=? AND msg_id=?
	`, channelID, fileMsgID).Scan(
		&entry.parentID, &entry.displayName, &entry.revision, &entry.tombstoned,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("projection: read file namespace row: %w", err)
	}
	return replaceLegacyDirent(tx, entry)
}

func replaceLegacyDirent(tx *sql.Tx, entry legacyDirentRow) error {
	if _, err := tx.Exec(`DELETE FROM dirents WHERE channel_id=? AND object_id=?`, entry.channelID, entry.objectID); err != nil {
		return fmt.Errorf("projection: replace legacy dirent: %w", err)
	}
	return insertLegacyDirent(tx, entry)
}

func ensureLegacyFileRevision(tx *sql.Tx, channelID, fileMsgID, actorID int64) error {
	_, err := tx.Exec(`
		INSERT OR IGNORE INTO file_revisions
		  (channel_id, file_msg_id, revision, content_msg_id, upload_uuid,
		   part_count, size, plaintext_size, content_hash, encrypted,
		   encryption_version, committed_msg_id, actor_user_id, op_id)
		SELECT channel_id, msg_id, revision, content_msg_id, upload_uuid,
		       part_count, size, plaintext_size, content_hash, encrypted,
		       encryption_version, msg_id, ?, ''
		FROM files WHERE channel_id=? AND msg_id=?
	`, actorID, channelID, fileMsgID)
	if err != nil {
		return fmt.Errorf("projection: ensure legacy file revision: %w", err)
	}
	return nil
}
