package projection

import (
	"database/sql"
	"strings"
	"testing"
	"unicode"

	_ "modernc.org/sqlite"
)

func TestMigrateV7AddsWritableProjectionAndBackfillsLegacyFile(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })

	if err := seedV7Projection(t, db); err != nil {
		t.Fatal(err)
	}
	if err := MigratePersonalChannel(db, migPersonalChan); err != nil {
		t.Fatalf("migrate v7: %v", err)
	}

	for _, table := range []string{"dirents", "file_revisions", "projection_operations", "trash_entries"} {
		var got string
		if err := db.QueryRow(`SELECT name FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&got); err != nil {
			t.Fatalf("table %s missing: %v", table, err)
		}
	}

	var contentMsgID, revision int64
	if err := db.QueryRow(`SELECT content_msg_id, revision FROM files WHERE channel_id=? AND msg_id=41`, migPersonalChan).
		Scan(&contentMsgID, &revision); err != nil {
		t.Fatalf("legacy file columns: %v", err)
	}
	if contentMsgID != 41 || revision != 1 {
		t.Fatalf("legacy content/revision = %d/%d", contentMsgID, revision)
	}

	var objectID, displayName, key string
	if err := db.QueryRow(`
		SELECT object_id, display_name, name_key FROM dirents
		WHERE channel_id=? AND object_id='f:41'
	`, migPersonalChan).Scan(&objectID, &displayName, &key); err != nil {
		t.Fatalf("legacy dirent: %v", err)
	}
	wantKey, err := CanonicalNameKey("Legacy.txt")
	if err != nil {
		t.Fatal(err)
	}
	if objectID != "f:41" || displayName != "Legacy.txt" || key != wantKey {
		t.Fatalf("dirent = (%q, %q, %q), want f:41/Legacy.txt/%q", objectID, displayName, key, wantKey)
	}

	var revisionRows int
	if err := db.QueryRow(`SELECT COUNT(*) FROM file_revisions WHERE channel_id=? AND file_msg_id=41`, migPersonalChan).
		Scan(&revisionRows); err != nil {
		t.Fatal(err)
	}
	if revisionRows != 1 {
		t.Fatalf("legacy revision rows = %d", revisionRows)
	}
}

func TestMigrateV7ReplaysLegacyRevisionHistoryBeforeWritableCAS(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := seedV7Projection(t, db); err != nil {
		t.Fatal(err)
	}
	if err := EnsureSchema(db); err != nil {
		t.Fatalf("ensure replay schema: %v", err)
	}

	seedReplay(t, db, migPersonalChan, 41, Op{
		Type: OpFileUpload, Parent: RootParent, Name: "Original.txt",
		FileSize: 12, FileUploadTime: 99,
	})
	seedReplay(t, db, migPersonalChan, 42, Op{
		Type: OpRename, Obj: "f:41", Name: "Legacy.txt",
	})

	if err := MigratePersonalChannel(db, migPersonalChan); err != nil {
		t.Fatalf("migrate v7: %v", err)
	}

	var fileRevision, direntRevision int64
	if err := db.QueryRow(`
		SELECT f.revision, d.revision
		FROM files f JOIN dirents d
		  ON d.channel_id=f.channel_id AND d.object_id='f:' || f.msg_id
		WHERE f.channel_id=? AND f.msg_id=41
	`, migPersonalChan).Scan(&fileRevision, &direntRevision); err != nil {
		t.Fatalf("read migrated revisions: %v", err)
	}
	if fileRevision != 2 || direntRevision != 2 {
		t.Fatalf("migrated revisions = file:%d dirent:%d, want 2/2", fileRevision, direntRevision)
	}

	// Rename and move do not duplicate content, but the active body row follows
	// the object revision so MAX(revision) always identifies current content.
	var contentRevision int64
	if err := db.QueryRow(`
		SELECT revision FROM file_revisions
		WHERE channel_id=? AND file_msg_id=41
	`, migPersonalChan).Scan(&contentRevision); err != nil {
		t.Fatalf("read migrated content revision: %v", err)
	}
	if contentRevision != 2 {
		t.Fatalf("migrated content revision = %d, want 2", contentRevision)
	}

	replace := Op{
		Type: OpFileReplace, ProtocolVersion: 1, OpID: "migrated-replace",
		Obj: "f:41", ExpectedRevision: 2, ContentMsgID: 9002,
		ContentHash: "sha256:new", FileSize: 20, RetainedUntil: 1000,
	}
	if _, err := ProjectFromOp(db, migPersonalChan, 43, replace, 7, Format(replace)); err != nil {
		t.Fatalf("replace migrated file: %v", err)
	}
	assertMigratedReplaceApplied(t, db)

	if err := RebuildProjection(db, migPersonalChan); err != nil {
		t.Fatalf("rebuild migrated projection: %v", err)
	}
	assertMigratedReplaceApplied(t, db)
}

func TestMigrateV7LegacyMoveAndDeleteRebuildWithSameRevisionAndOutcome(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := seedV7Projection(t, db); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		INSERT INTO folders(channel_id,id,name,parent_id,tombstoned)
		VALUES (?, 'd:dest', 'Destination', '', 0)
	`, migPersonalChan); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		UPDATE files SET name='Renamed.txt', parent_id='d:dest', tombstoned=1
		WHERE channel_id=? AND msg_id=41
	`, migPersonalChan); err != nil {
		t.Fatal(err)
	}
	if err := EnsureSchema(db); err != nil {
		t.Fatalf("ensure replay schema: %v", err)
	}

	seedReplay(t, db, migPersonalChan, 40, Op{
		Type: OpMkdir, Obj: "d:dest", Parent: RootParent, Name: "Destination",
	})
	seedReplay(t, db, migPersonalChan, 41, Op{
		Type: OpFileUpload, Parent: RootParent, Name: "Original.txt",
		FileSize: 12, FileUploadTime: 99,
	})
	seedReplay(t, db, migPersonalChan, 42, Op{
		Type: OpRename, Obj: "f:41", Name: "Renamed.txt",
	})
	seedReplay(t, db, migPersonalChan, 43, Op{
		Type: OpMove, Obj: "f:41", Parent: "d:dest",
	})
	seedReplay(t, db, migPersonalChan, 44, Op{
		Type: OpTomb, Obj: "f:41",
	})

	if err := MigratePersonalChannel(db, migPersonalChan); err != nil {
		t.Fatalf("migrate v7: %v", err)
	}
	assertDeletedLegacyRevision(t, db, 4)

	rejected := Op{
		Type: OpFileReplace, ProtocolVersion: 1, OpID: "replace-deleted",
		Obj: "f:41", ExpectedRevision: 4, ContentMsgID: 9003,
		RetainedUntil: 1000,
	}
	if _, err := ProjectFromOp(db, migPersonalChan, 45, rejected, 7, Format(rejected)); err != nil {
		t.Fatalf("project rejected replacement: %v", err)
	}
	assertOperationOutcome(t, db, rejected.OpID, OperationRejected)

	if err := RebuildProjection(db, migPersonalChan); err != nil {
		t.Fatalf("rebuild migrated projection: %v", err)
	}
	assertDeletedLegacyRevision(t, db, 4)
	assertOperationOutcome(t, db, rejected.OpID, OperationRejected)
}

func TestFileRevisionUploadUUIDLookupUsesPartialIndex(t *testing.T) {
	db := newTestDB(t)

	var partial int
	if err := db.QueryRow(`
		SELECT partial FROM pragma_index_list('file_revisions')
		WHERE name='idx_file_revisions_upload_uuid'
	`).Scan(&partial); err != nil {
		t.Fatalf("upload uuid index: %v", err)
	}
	if partial != 1 {
		t.Fatalf("upload uuid index partial=%d, want 1", partial)
	}

	rows, err := db.Query(`
		EXPLAIN QUERY PLAN
		SELECT 1 FROM file_revisions
		WHERE channel_id=? AND upload_uuid=? AND upload_uuid!=''
		LIMIT 1
	`, testChan, "upload-1")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var details []string
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		details = append(details, detail)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	plan := strings.Join(details, "\n")
	if !strings.Contains(plan, "idx_file_revisions_upload_uuid") {
		t.Fatalf("upload uuid lookup missed partial index:\n%s", plan)
	}
}

func assertMigratedReplaceApplied(t *testing.T, db *sql.DB) {
	t.Helper()
	var name, hash string
	var revision, contentMsgID int64
	if err := db.QueryRow(`
		SELECT name, revision, content_msg_id, content_hash
		FROM files WHERE channel_id=? AND msg_id=41 AND tombstoned=0
	`, migPersonalChan).Scan(&name, &revision, &contentMsgID, &hash); err != nil {
		t.Fatalf("read replaced file: %v", err)
	}
	if name != "Legacy.txt" || revision != 3 || contentMsgID != 9002 || hash != "sha256:new" {
		t.Fatalf("replaced file = (%q,%d,%d,%q), want Legacy.txt/3/9002/sha256:new", name, revision, contentMsgID, hash)
	}
	assertFileRevisionHistory(t, db, []int64{2, 3}, 3)
	assertOperationOutcome(t, db, "migrated-replace", OperationApplied)
}

func assertDeletedLegacyRevision(t *testing.T, db *sql.DB, wantRevision int64) {
	t.Helper()
	var fileRevision, direntRevision int64
	var fileTombstoned, direntTombstoned int
	if err := db.QueryRow(`
		SELECT f.revision, f.tombstoned, d.revision, d.tombstoned
		FROM files f JOIN dirents d
		  ON d.channel_id=f.channel_id AND d.object_id='f:' || f.msg_id
		WHERE f.channel_id=? AND f.msg_id=41
	`, migPersonalChan).Scan(
		&fileRevision, &fileTombstoned, &direntRevision, &direntTombstoned,
	); err != nil {
		t.Fatalf("read deleted legacy object: %v", err)
	}
	if fileRevision != wantRevision || direntRevision != wantRevision || fileTombstoned != 1 || direntTombstoned != 1 {
		t.Fatalf("deleted object = file:%d/%d dirent:%d/%d, want %d/1 %d/1",
			fileRevision, fileTombstoned, direntRevision, direntTombstoned,
			wantRevision, wantRevision)
	}
	assertFileRevisionHistory(t, db, []int64{wantRevision}, wantRevision)
}

func assertFileRevisionHistory(t *testing.T, db *sql.DB, wantRevisions []int64, wantActive int64) {
	t.Helper()
	rows, err := db.Query(`
		SELECT revision, retained_until FROM file_revisions
		WHERE channel_id=? AND file_msg_id=41
		ORDER BY revision
	`, migPersonalChan)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var revisions []int64
	var active int64
	for rows.Next() {
		var revision, retainedUntil int64
		if err := rows.Scan(&revision, &retainedUntil); err != nil {
			t.Fatal(err)
		}
		revisions = append(revisions, revision)
		if retainedUntil == 0 {
			active = revision
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if len(revisions) != len(wantRevisions) {
		t.Fatalf("file revisions=%v, want %v", revisions, wantRevisions)
	}
	for index := range wantRevisions {
		if revisions[index] != wantRevisions[index] {
			t.Fatalf("file revisions=%v, want %v", revisions, wantRevisions)
		}
	}
	if active != wantActive {
		t.Fatalf("active body revision=%d, want %d", active, wantActive)
	}
}

func assertOperationOutcome(t *testing.T, db *sql.DB, opID, wantOutcome string) {
	t.Helper()
	operation, found, err := ProjectionOperationByID(db, migPersonalChan, opID)
	if err != nil || !found {
		t.Fatalf("operation %q: found=%v err=%v", opID, found, err)
	}
	if operation.Outcome != wantOutcome {
		t.Fatalf("operation %q outcome=%q, want %q (%s)", opID, operation.Outcome, wantOutcome, operation.Error)
	}
}

func TestMigrateSanitizesInvalidAndOverlongLegacyNames(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := seedV7Projection(t, db); err != nil {
		t.Fatal(err)
	}
	for index, name := range []string{"CON", "bad:name", "trailing. ", strings.Repeat("é", 130), "report\u202efdp.exe"} {
		if _, err := db.Exec(`
			INSERT INTO folders(channel_id,id,name,parent_id,tombstoned)
			VALUES (?, ?, ?, '', 0)
		`, migPersonalChan, "d:invalid-"+string(rune('a'+index)), name); err != nil {
			t.Fatal(err)
		}
	}
	if err := MigratePersonalChannel(db, migPersonalChan); err != nil {
		t.Fatal(err)
	}
	rows, err := db.Query(`SELECT display_name, name_key FROM dirents WHERE channel_id=?`, migPersonalChan)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var name, key string
		if err := rows.Scan(&name, &key); err != nil {
			t.Fatal(err)
		}
		canonical, err := CanonicalNameKey(name)
		if err != nil || canonical != key || len([]byte(name)) > maxPortableNameBytes {
			t.Fatalf("migrated name=%q key=%q canonical=%q err=%v", name, key, canonical, err)
		}
		for _, character := range name {
			if unicode.Is(unicode.Bidi_Control, character) {
				t.Fatalf("migrated name retained bidi control U+%04X: %q", character, name)
			}
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
}

func TestMigrateLegacyPortableNameCollisionsUsesDeterministicAliases(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := seedV7Projection(t, db); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		INSERT INTO folders (channel_id,id,name,parent_id,tombstoned)
		VALUES (?, 'd:collision', 'LEGACY.TXT', '', 0)
	`, migPersonalChan); err != nil {
		t.Fatal(err)
	}

	if err := MigratePersonalChannel(db, migPersonalChan); err != nil {
		t.Fatalf("migrate collisions: %v", err)
	}

	rows, err := db.Query(`
		SELECT display_name, name_key FROM dirents
		WHERE channel_id=? AND parent_id='' AND tombstoned=0
		ORDER BY object_id
	`, migPersonalChan)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	seen := map[string]bool{}
	count := 0
	for rows.Next() {
		var displayName, key string
		if err := rows.Scan(&displayName, &key); err != nil {
			t.Fatal(err)
		}
		if seen[key] {
			t.Fatalf("duplicate migrated name key %q", key)
		}
		seen[key] = true
		canonical, err := CanonicalNameKey(displayName)
		if err != nil {
			t.Fatalf("alias %q is not portable: %v", displayName, err)
		}
		if canonical != key {
			t.Fatalf("alias %q key=%q canonical=%q", displayName, key, canonical)
		}
		count++
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("dirents = %d, want 2", count)
	}
}

func TestCanonicalNameKeyRejectsNonPortableWindowsNames(t *testing.T) {
	for _, name := range []string{
		"", ".", "..", "CON", "aux.txt", "CLOCK$", "COM¹.txt", "lpt³",
		"bad:name", "trailing. ", "slash/name",
	} {
		if _, err := CanonicalNameKey(name); err == nil {
			t.Fatalf("CanonicalNameKey(%q) succeeded", name)
		}
	}
	for _, name := range []string{"report.txt", "数据.bin", "hello world"} {
		if _, err := CanonicalNameKey(name); err != nil {
			t.Fatalf("CanonicalNameKey(%q): %v", name, err)
		}
	}
}

func seedV7Projection(t *testing.T, db *sql.DB) error {
	t.Helper()
	stmts := []string{
		`CREATE TABLE schema_version (version INTEGER PRIMARY KEY);`,
		`INSERT INTO schema_version(version) VALUES (7);`,
		`CREATE TABLE channels (
			channel_id INTEGER PRIMARY KEY, access_hash INTEGER NOT NULL DEFAULT 0,
			title TEXT NOT NULL, kind TEXT NOT NULL, invite_link TEXT, joined_at INTEGER NOT NULL,
			last_synced_msg INTEGER NOT NULL DEFAULT 0, last_viewed_msg INTEGER NOT NULL DEFAULT 0,
			has_unseen_content INTEGER NOT NULL DEFAULT 0, initial_sync_done INTEGER NOT NULL DEFAULT 0,
			personal_backfill_done INTEGER NOT NULL DEFAULT 0
		);`,
		`INSERT INTO channels(channel_id,title,kind,joined_at) VALUES (12345,'Mine','personal',0);`,
		`CREATE TABLE folders (
			channel_id INTEGER NOT NULL, id TEXT NOT NULL, name TEXT NOT NULL,
			parent_id TEXT NOT NULL DEFAULT '', tombstoned INTEGER NOT NULL DEFAULT 0,
			PRIMARY KEY(channel_id,id)
		);`,
		`CREATE TABLE files (
			channel_id INTEGER NOT NULL, msg_id INTEGER NOT NULL, name TEXT NOT NULL,
			size INTEGER NOT NULL, parent_id TEXT NOT NULL DEFAULT '', upload_time INTEGER NOT NULL,
			uploader_user_id INTEGER NOT NULL DEFAULT 0, tombstoned INTEGER NOT NULL DEFAULT 0,
			encrypted INTEGER NOT NULL DEFAULT 0, plaintext_size INTEGER NOT NULL DEFAULT 0,
			encryption_version INTEGER NOT NULL DEFAULT 0, upload_uuid TEXT NOT NULL DEFAULT '',
			part_count INTEGER NOT NULL DEFAULT 0, PRIMARY KEY(channel_id,msg_id)
		);`,
		`INSERT INTO files(channel_id,msg_id,name,size,parent_id,upload_time)
		 VALUES (12345,41,'Legacy.txt',12,'',99);`,
	}
	for _, stmt := range stmts {
		if _, err := db.Exec(stmt); err != nil {
			return err
		}
	}
	return nil
}
