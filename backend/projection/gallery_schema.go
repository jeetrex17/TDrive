package projection

import (
	"database/sql"
	"fmt"
)

// EnsureGallerySchema adds local-only cursor generations. Triggers make the
// generation part of the same transaction as every projection mutation,
// including legacy writes and full replay; a rolled-back write cannot expire
// a cursor. Rendition readiness deliberately does not touch this metadata epoch.
func EnsureGallerySchema(db *sql.DB) error {
	if !dbTableHasColumns(db, "files", "channel_id", "msg_id", "upload_time", "tombstoned") {
		return nil
	}
	statements := []string{
		`CREATE TABLE IF NOT EXISTS gallery_epoch (id INTEGER PRIMARY KEY CHECK(id=1), epoch TEXT NOT NULL)`,
		`INSERT OR IGNORE INTO gallery_epoch(id,epoch) VALUES(1,lower(hex(randomblob(16))))`,
		`CREATE TABLE IF NOT EXISTS gallery_generations (channel_id INTEGER PRIMARY KEY, revision INTEGER NOT NULL DEFAULT 0)`,
		`CREATE TABLE IF NOT EXISTS gallery_preparation_skips (
  channel_id INTEGER NOT NULL,file_msg_id INTEGER NOT NULL,content_msg_id INTEGER NOT NULL,
  content_hash TEXT NOT NULL,decoder_profile TEXT NOT NULL,generator_version INTEGER NOT NULL,
  PRIMARY KEY(channel_id,file_msg_id))`,
		`CREATE TRIGGER IF NOT EXISTS gallery_preparation_skip_delete AFTER DELETE ON files
  BEGIN DELETE FROM gallery_preparation_skips WHERE channel_id=OLD.channel_id AND file_msg_id=OLD.msg_id; END`,
	}
	for _, table := range []string{"files", "dirents"} {
		for _, event := range []string{"INSERT", "UPDATE", "DELETE"} {
			row := "NEW"
			if event == "DELETE" {
				row = "OLD"
			}
			condition := ""
			if table == "dirents" {
				condition = " WHEN " + row + ".object_kind='file'"
			}
			body := `INSERT INTO gallery_generations(channel_id,revision) VALUES(` + row + `.channel_id,1)
    ON CONFLICT(channel_id) DO UPDATE SET revision=revision+1;`
			if event == "UPDATE" {
				body += ` INSERT INTO gallery_generations(channel_id,revision)
     SELECT OLD.channel_id,1 WHERE OLD.channel_id!=NEW.channel_id
     ON CONFLICT(channel_id) DO UPDATE SET revision=revision+1;`
			}
			statements = append(statements, `CREATE TRIGGER IF NOT EXISTS gallery_`+table+`_`+event+` AFTER `+event+` ON `+table+condition+` BEGIN `+body+` END`)
		}
	}
	for _, statement := range statements {
		if _, err := db.Exec(statement); err != nil {
			return fmt.Errorf("projection: gallery schema: %w", err)
		}
	}
	return nil
}
