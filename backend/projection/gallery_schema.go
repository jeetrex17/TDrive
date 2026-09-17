package projection

import (
	"database/sql"
	"fmt"
	"strings"
)

const galleryMaterializationVersion = 2

// EnsureGallerySchema installs the local gallery projection. gallery_items is
// derived from files and dirents by triggers, so replay, migrations, and direct
// projection repairs update eligibility in the same transaction.
func EnsureGallerySchema(db *sql.DB) error {
	if !dbTableHasColumns(db, "files", "channel_id", "msg_id", "name", "upload_time", "tombstoned", "upload_uuid") ||
		!dbTableHasColumns(db, "dirents", "channel_id", "object_id", "object_kind", "display_name", "tombstoned") {
		return nil
	}
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("projection: begin gallery schema: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	base := []string{
		// Preview preparation was replaced by bounded, on-demand Telegram
		// thumbnails. Remove its disposable failure table when upgrading a
		// development database from the earlier schema.
		`DROP TRIGGER IF EXISTS gallery_preparation_skip_delete`,
		`DROP TABLE IF EXISTS gallery_preparation_skips`,
		`CREATE TABLE IF NOT EXISTS gallery_epoch (id INTEGER PRIMARY KEY CHECK(id=1), epoch TEXT NOT NULL)`,
		`INSERT OR IGNORE INTO gallery_epoch(id,epoch) VALUES(1,lower(hex(randomblob(16))))`,
		`CREATE TABLE IF NOT EXISTS gallery_generations (channel_id INTEGER PRIMARY KEY, revision INTEGER NOT NULL DEFAULT 0)`,
		`CREATE TABLE IF NOT EXISTS gallery_schema_meta (id INTEGER PRIMARY KEY CHECK(id=1), version INTEGER NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS gallery_items (
  channel_id INTEGER NOT NULL,msg_id INTEGER NOT NULL,upload_time INTEGER NOT NULL,
  month_key TEXT NOT NULL,display_name TEXT NOT NULL,
  PRIMARY KEY(channel_id,msg_id))`,
		`CREATE INDEX IF NOT EXISTS idx_gallery_items_channel_order
  ON gallery_items(channel_id,upload_time DESC,msg_id DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_gallery_items_channel_month_order
  ON gallery_items(channel_id,month_key,upload_time DESC)`,
		`CREATE TABLE IF NOT EXISTS gallery_months (
  channel_id INTEGER NOT NULL,month_key TEXT NOT NULL,item_count INTEGER NOT NULL,
  latest_upload_time INTEGER NOT NULL,
  PRIMARY KEY(channel_id,month_key))`,
	}
	if err := execGalleryStatements(tx, base); err != nil {
		return err
	}

	var version int
	err = tx.QueryRow(`SELECT version FROM gallery_schema_meta WHERE id=1`).Scan(&version)
	if err != nil && err != sql.ErrNoRows {
		return fmt.Errorf("projection: read gallery schema version: %w", err)
	}
	if version != galleryMaterializationVersion {
		if err := rebuildGalleryMaterialization(tx); err != nil {
			return err
		}
	} else if err := installGalleryMaterializationTriggers(tx); err != nil {
		return err
	}
	if err := installGalleryGenerationTriggers(tx); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("projection: commit gallery schema: %w", err)
	}
	return nil
}

func rebuildGalleryMaterialization(tx *sql.Tx) error {
	for _, name := range galleryMaterializationTriggerNames() {
		if _, err := tx.Exec(`DROP TRIGGER IF EXISTS ` + name); err != nil {
			return fmt.Errorf("projection: drop gallery trigger %s: %w", name, err)
		}
	}
	if _, err := tx.Exec(`DELETE FROM gallery_items`); err != nil {
		return fmt.Errorf("projection: clear gallery items: %w", err)
	}
	if _, err := tx.Exec(`DELETE FROM gallery_months`); err != nil {
		return fmt.Errorf("projection: clear gallery months: %w", err)
	}
	if err := installGalleryMaterializationTriggers(tx); err != nil {
		return err
	}
	if _, err := tx.Exec(galleryInsertSQL("1=1")); err != nil {
		return fmt.Errorf("projection: backfill gallery items: %w", err)
	}
	if _, err := tx.Exec(`INSERT INTO gallery_schema_meta(id,version) VALUES(1,?)
ON CONFLICT(id) DO UPDATE SET version=excluded.version`, galleryMaterializationVersion); err != nil {
		return fmt.Errorf("projection: write gallery schema version: %w", err)
	}
	return nil
}

func installGalleryMaterializationTriggers(tx *sql.Tx) error {
	statements := []string{
		`CREATE TRIGGER IF NOT EXISTS gallery_items_month_insert AFTER INSERT ON gallery_items BEGIN
  INSERT INTO gallery_months(channel_id,month_key,item_count,latest_upload_time)
  VALUES(NEW.channel_id,NEW.month_key,1,NEW.upload_time)
  ON CONFLICT(channel_id,month_key) DO UPDATE SET
    item_count=item_count+1,
    latest_upload_time=MAX(latest_upload_time,excluded.latest_upload_time);
END`,
		`CREATE TRIGGER IF NOT EXISTS gallery_items_month_delete AFTER DELETE ON gallery_items BEGIN
  UPDATE gallery_months SET
    item_count=item_count-1,
    latest_upload_time=COALESCE((SELECT MAX(upload_time) FROM gallery_items
      WHERE channel_id=OLD.channel_id AND month_key=OLD.month_key),0)
  WHERE channel_id=OLD.channel_id AND month_key=OLD.month_key;
  DELETE FROM gallery_months
  WHERE channel_id=OLD.channel_id AND month_key=OLD.month_key AND item_count<=0;
END`,
		`CREATE TRIGGER IF NOT EXISTS gallery_items_sync_files_insert AFTER INSERT ON files BEGIN
  ` + galleryRefreshSQL("NEW.channel_id", "NEW.msg_id") + `
END`,
		`CREATE TRIGGER IF NOT EXISTS gallery_items_sync_files_update AFTER UPDATE ON files BEGIN
  ` + galleryDeleteSQL("OLD.channel_id", "OLD.msg_id") + `
  ` + galleryRefreshSQL("NEW.channel_id", "NEW.msg_id") + `
END`,
		`CREATE TRIGGER IF NOT EXISTS gallery_items_sync_files_delete AFTER DELETE ON files BEGIN
  ` + galleryDeleteSQL("OLD.channel_id", "OLD.msg_id") + `
END`,
		`CREATE TRIGGER IF NOT EXISTS gallery_items_sync_dirents_insert AFTER INSERT ON dirents WHEN NEW.object_kind='file' BEGIN
  ` + galleryRefreshDirentSQL("NEW") + `
END`,
		`CREATE TRIGGER IF NOT EXISTS gallery_items_sync_dirents_update AFTER UPDATE ON dirents BEGIN
  ` + galleryRefreshDirentSQL("OLD") + `
  ` + galleryRefreshDirentSQL("NEW") + `
END`,
		`CREATE TRIGGER IF NOT EXISTS gallery_items_sync_dirents_delete AFTER DELETE ON dirents WHEN OLD.object_kind='file' BEGIN
  ` + galleryRefreshDirentSQL("OLD") + `
END`,
	}
	return execGalleryStatements(tx, statements)
}

func installGalleryGenerationTriggers(tx *sql.Tx) error {
	// Generation follows the materialized gallery itself. This avoids rebuilding
	// a million-photo timeline when unrelated documents are synced or renamed.
	// Drop the earlier broad files/dirents triggers during an in-place upgrade.
	statements := []string{
		`DROP TRIGGER IF EXISTS gallery_files_INSERT`,
		`DROP TRIGGER IF EXISTS gallery_files_UPDATE`,
		`DROP TRIGGER IF EXISTS gallery_files_DELETE`,
		`DROP TRIGGER IF EXISTS gallery_dirents_INSERT`,
		`DROP TRIGGER IF EXISTS gallery_dirents_UPDATE`,
		`DROP TRIGGER IF EXISTS gallery_dirents_DELETE`,
		`DROP TRIGGER IF EXISTS gallery_items_generation_insert`,
		`DROP TRIGGER IF EXISTS gallery_items_generation_update`,
		`DROP TRIGGER IF EXISTS gallery_items_generation_delete`,
	}
	for _, event := range []string{"INSERT", "UPDATE", "DELETE"} {
		row := "NEW"
		if event == "DELETE" {
			row = "OLD"
		}
		body := `INSERT INTO gallery_generations(channel_id,revision) VALUES(` + row + `.channel_id,1)
	    ON CONFLICT(channel_id) DO UPDATE SET revision=revision+1;`
		if event == "UPDATE" {
			body += ` INSERT INTO gallery_generations(channel_id,revision)
	     SELECT OLD.channel_id,1 WHERE OLD.channel_id!=NEW.channel_id
	     ON CONFLICT(channel_id) DO UPDATE SET revision=revision+1;`
		}
		statements = append(statements, `CREATE TRIGGER gallery_items_generation_`+strings.ToLower(event)+` AFTER `+event+` ON gallery_items BEGIN `+body+` END`)
	}
	return execGalleryStatements(tx, statements)
}

func galleryRefreshDirentSQL(row string) string {
	channel := row + ".channel_id"
	msgID := `(SELECT f.msg_id FROM files f WHERE f.channel_id=` + channel + ` AND ` + row + `.object_id='f:' || f.msg_id)`
	return galleryDeleteSQL(channel, msgID) + "\n" + galleryInsertSQL(`f.channel_id=`+channel+` AND f.msg_id=`+msgID)
}

func galleryRefreshSQL(channel, msgID string) string {
	return galleryDeleteSQL(channel, msgID) + "\n" + galleryInsertSQL(`f.channel_id=`+channel+` AND f.msg_id=`+msgID)
}

func galleryDeleteSQL(channel, msgID string) string {
	return `DELETE FROM gallery_items WHERE channel_id=` + channel + ` AND msg_id=` + msgID + `;`
}

func galleryInsertSQL(scope string) string {
	name := `COALESCE(d.display_name,f.name)`
	return `INSERT INTO gallery_items(channel_id,msg_id,upload_time,month_key,display_name)
	SELECT f.channel_id,f.msg_id,f.upload_time,COALESCE(strftime('%Y-%m',f.upload_time,'unixepoch'),'unknown'),` + name + `
FROM files f
LEFT JOIN dirents d ON d.channel_id=f.channel_id AND d.object_id='f:' || f.msg_id
WHERE ` + scope + ` AND f.tombstoned=0 AND f.upload_uuid='' AND COALESCE(d.tombstoned,0)=0
AND ` + galleryImageNamePredicate(name) + `
ON CONFLICT(channel_id,msg_id) DO UPDATE SET
 upload_time=excluded.upload_time,month_key=excluded.month_key,display_name=excluded.display_name;`
}

func galleryImageNamePredicate(name string) string {
	return `(` + name + ` LIKE '%.jpg' COLLATE NOCASE
 OR ` + name + ` LIKE '%.jpeg' COLLATE NOCASE
 OR ` + name + ` LIKE '%.png' COLLATE NOCASE
 OR ` + name + ` LIKE '%.gif' COLLATE NOCASE
 OR ` + name + ` LIKE '%.webp' COLLATE NOCASE
 OR ` + name + ` LIKE '%.bmp' COLLATE NOCASE)`
}

func galleryMaterializationTriggerNames() []string {
	return []string{
		"gallery_items_month_insert", "gallery_items_month_delete",
		"gallery_items_sync_files_insert", "gallery_items_sync_files_update", "gallery_items_sync_files_delete",
		"gallery_items_sync_dirents_insert", "gallery_items_sync_dirents_update", "gallery_items_sync_dirents_delete",
	}
}

func execGalleryStatements(tx *sql.Tx, statements []string) error {
	for _, statement := range statements {
		if _, err := tx.Exec(statement); err != nil {
			return fmt.Errorf("projection: gallery schema: %w", err)
		}
	}
	return nil
}
