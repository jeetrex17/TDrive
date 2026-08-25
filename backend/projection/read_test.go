package projection

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"
)

func TestListFolderContentsContextHonorsCanceledContext(t *testing.T) {
	db := newTestDB(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	folders, files, err := ListFolderContentsContext(ctx, db, testChan, RootParent)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("ListFolderContentsContext error = %v, want context.Canceled", err)
	}
	if folders != nil || files != nil {
		t.Fatalf("canceled listing = folders:%v files:%v, want nil results", folders, files)
	}

	files, err = listChildFilesContext(ctx, db, testChan, RootParent)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("listChildFilesContext error = %v, want context.Canceled", err)
	}
	if files != nil {
		t.Fatalf("listChildFilesContext files = %v, want nil", files)
	}
}

func TestListFolderContentsContextRejectsNilContext(t *testing.T) {
	db := newTestDB(t)

	folders, files, err := ListFolderContentsContext(nil, db, testChan, RootParent)
	if !errors.Is(err, ErrInvalidContext) {
		t.Fatalf("ListFolderContentsContext error = %v, want ErrInvalidContext", err)
	}
	if folders != nil || files != nil {
		t.Fatalf("nil-context listing = folders:%v files:%v, want nil results", folders, files)
	}
}

func TestListFolderContentsUsesDeterministicNewestFirstOrder(t *testing.T) {
	db := newTestDB(t)
	for _, messageID := range []int64{7, 9, 8} {
		if _, err := db.Exec(`
			INSERT INTO files (
				channel_id, msg_id, name, size, parent_id, upload_time,
				uploader_user_id, tombstoned, content_msg_id, revision
			) VALUES (?, ?, ?, 1, ?, 100, 0, 0, ?, 1)
		`, testChan, messageID, "same-time", RootParent, messageID); err != nil {
			t.Fatalf("seed message %d: %v", messageID, err)
		}
		if _, err := db.Exec(`
			INSERT INTO dirents (
				channel_id, object_id, object_kind, parent_id, display_name,
				name_key, revision, tombstoned
			) VALUES (?, ?, 'file', ?, ?, ?, 1, 0)
		`, testChan, FileIDPrefix+strconv.FormatInt(messageID, 10), RootParent, "same-time", "same-time-"+strconv.FormatInt(messageID, 10)); err != nil {
			t.Fatalf("seed dirent %d: %v", messageID, err)
		}
	}

	_, files, err := ListFolderContentsContext(context.Background(), db, testChan, RootParent)
	if err != nil {
		t.Fatalf("list folder: %v", err)
	}
	want := []int64{9, 8, 7}
	if len(files) != len(want) {
		t.Fatalf("file count = %d, want %d", len(files), len(want))
	}
	for index, messageID := range want {
		if files[index].MsgID != messageID {
			t.Fatalf("files[%d].MsgID = %d, want %d", index, files[index].MsgID, messageID)
		}
	}
}

func TestListFolderContentsUsesDirentDisplayNamesForWriteRoundTrip(t *testing.T) {
	db := newTestDB(t)
	mustOp(t, db, 1, Op{Type: OpFileUpload, Parent: RootParent, Name: "Report.txt", FileSize: 10})
	mustOp(t, db, 2, Op{Type: OpFileUpload, Parent: RootParent, Name: "report.TXT", FileSize: 20})

	_, files, err := ListFolderContentsContext(context.Background(), db, testChan, RootParent)
	if err != nil {
		t.Fatalf("ListFolderContentsContext: %v", err)
	}
	if len(files) != 2 {
		t.Fatalf("files = %+v", files)
	}
	for _, file := range files {
		dirent, found, err := LiveDirentByName(db, testChan, RootParent, file.Name)
		if err != nil || !found || dirent.ObjectID != FileIDPrefix+strconv.FormatInt(file.MsgID, 10) {
			t.Fatalf("listed name %q did not round-trip: dirent=%+v found=%v err=%v", file.Name, dirent, found, err)
		}
	}
}

func TestUserVisibleReadHelpersUsePortableDirentDisplayNames(t *testing.T) {
	db := newTestDB(t)
	mustOp(t, db, 1, Op{
		Type: OpMkdir, Obj: "d:reserved", Parent: RootParent, Name: "CON",
	})
	mustOp(t, db, 2, Op{
		Type: OpFileUpload, Parent: "d:missing", Name: "opaque.bin", FileSize: 10,
	})

	folderDirent, found, err := DirentByID(db, testChan, "d:reserved")
	if err != nil || !found {
		t.Fatalf("folder dirent: found=%v err=%v", found, err)
	}
	fileKey, err := CanonicalNameKey("Visible.png")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		UPDATE dirents SET display_name='Visible.png', name_key=?
		WHERE channel_id=? AND object_id='f:2'
	`, fileKey, testChan); err != nil {
		t.Fatal(err)
	}

	folders, err := ListAllFolders(db, testChan)
	if err != nil {
		t.Fatal(err)
	}
	if len(folders) != 1 || folders[0].Name != folderDirent.DisplayName {
		t.Fatalf("listed folders = %+v, want display name %q", folders, folderDirent.DisplayName)
	}
	folder, found, err := FolderByID(db, testChan, "d:reserved")
	if err != nil || !found || folder.Name != folderDirent.DisplayName {
		t.Fatalf("FolderByID = %+v found=%v err=%v, want display name %q", folder, found, err, folderDirent.DisplayName)
	}

	hits, err := Search(db, testChan, "Visible", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 || hits[0].Name != "Visible.png" || hits[0].MsgID != 2 {
		t.Fatalf("search hits = %+v, want Visible.png", hits)
	}

	media, err := MediaFiles(db, testChan)
	if err != nil {
		t.Fatal(err)
	}
	if len(media) != 1 || media[0].Name != "Visible.png" {
		t.Fatalf("media = %+v, want Visible.png", media)
	}
	orphans, err := OrphanedFiles(db, testChan)
	if err != nil {
		t.Fatal(err)
	}
	if len(orphans) != 1 || orphans[0].Name != "Visible.png" {
		t.Fatalf("orphans = %+v, want Visible.png", orphans)
	}
}

func TestFolderListingQueriesUseCoveringParentOrderIndexes(t *testing.T) {
	db := newTestDB(t)

	tests := []struct {
		name      string
		query     string
		indexName string
	}{
		{
			name: "files newest first",
			query: `SELECT f.msg_id, f.content_msg_id, f.content_hash, f.revision, f.upload_uuid, f.part_count,
				d.display_name, f.size, f.parent_id, f.upload_time, f.uploader_user_id,
				f.encrypted, f.plaintext_size
				FROM files f
				JOIN dirents d
				  ON d.channel_id = f.channel_id
				 AND d.object_id = 'f:' || f.msg_id
				WHERE f.channel_id = ? AND f.parent_id = ? AND f.tombstoned = 0
				  AND d.tombstoned = 0
				ORDER BY f.upload_time DESC, f.msg_id DESC`,
			indexName: "idx_files_channel_parent_latest",
		},
		{
			name: "folders by name",
			query: `SELECT f.id, d.display_name, f.parent_id, d.revision
				FROM dirents d
				JOIN folders f ON f.channel_id = d.channel_id AND f.id = d.object_id
				WHERE d.channel_id = ? AND d.parent_id = ? AND d.object_kind = 'folder'
				  AND d.tombstoned = 0 AND f.tombstoned = 0
				ORDER BY d.display_name, d.object_id`,
			indexName: "idx_dirents_live_parent",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			rows, err := db.Query("EXPLAIN QUERY PLAN "+test.query, testChan, RootParent)
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
			if !strings.Contains(plan, test.indexName) {
				t.Fatalf("query plan did not use %s:\n%s", test.indexName, plan)
			}
			if strings.Contains(plan, "USE TEMP B-TREE FOR ORDER BY") {
				t.Fatalf("query plan sorts into a temporary b-tree:\n%s", plan)
			}
		})
	}
}

func TestOrphanedFilesSurfacesFilesUnderTombstonedFolder(t *testing.T) {
	db := newTestDB(t)
	mustOp(t, db, 1, Op{Type: OpMkdir, Obj: "d:photos", Parent: RootParent, Name: "Photos"})
	mustOp(t, db, 2, Op{Type: OpFileUpload, Parent: "d:photos", Name: "sunset.jpg"})
	mustOp(t, db, 3, Op{Type: OpFileUpload, Parent: RootParent, Name: "root.txt"})

	// No orphans yet.
	orphans, err := OrphanedFiles(db, testChan)
	if err != nil {
		t.Fatalf("orphans pre-tomb: %v", err)
	}
	if len(orphans) != 0 {
		t.Fatalf("expected no orphans, got %d", len(orphans))
	}

	// Tombstone the parent. The file inside becomes an orphan.
	mustOp(t, db, 4, Op{Type: OpRmdir, Obj: "d:photos"})

	orphans, err = OrphanedFiles(db, testChan)
	if err != nil {
		t.Fatalf("orphans post-tomb: %v", err)
	}
	if len(orphans) != 1 {
		t.Fatalf("expected 1 orphan, got %d", len(orphans))
	}
	if orphans[0].MsgID != 2 {
		t.Fatalf("orphan msg_id = %d, want 2", orphans[0].MsgID)
	}

	// Root files are never orphans.
	for _, o := range orphans {
		if o.MsgID == 3 {
			t.Fatal("root file leaked into orphan list")
		}
	}
}

func TestOrphanedFilesIncludesFilesWithMissingParent(t *testing.T) {
	db := newTestDB(t)
	mustOp(t, db, 1, Op{Type: OpFileUpload, Parent: "d:never-existed", Name: "stray.jpg"})

	orphans, err := OrphanedFiles(db, testChan)
	if err != nil {
		t.Fatalf("orphans: %v", err)
	}
	if len(orphans) != 1 || orphans[0].MsgID != 1 {
		t.Fatalf("expected stray to be orphaned, got %v", orphans)
	}
}

func TestOrphanedFilesDetectsDeepAncestorTombstone(t *testing.T) {
	db := newTestDB(t)
	// A / B / C / file.txt — tombstone A. C still exists, so the file's
	// immediate parent (C) looks fine, but the chain to root is broken.
	mustOp(t, db, 1, Op{Type: OpMkdir, Obj: "d:a", Parent: RootParent, Name: "A"})
	mustOp(t, db, 2, Op{Type: OpMkdir, Obj: "d:b", Parent: "d:a", Name: "B"})
	mustOp(t, db, 3, Op{Type: OpMkdir, Obj: "d:c", Parent: "d:b", Name: "C"})
	mustOp(t, db, 4, Op{Type: OpFileUpload, Parent: "d:c", Name: "deep.txt"})

	// Pre-tomb: nothing orphaned.
	orphans, err := OrphanedFiles(db, testChan)
	if err != nil {
		t.Fatalf("orphans pre-tomb: %v", err)
	}
	if len(orphans) != 0 {
		t.Fatalf("expected 0 orphans pre-tomb, got %d", len(orphans))
	}

	// Tombstone the root of the subtree, three levels above the file.
	mustOp(t, db, 5, Op{Type: OpRmdir, Obj: "d:a"})

	orphans, err = OrphanedFiles(db, testChan)
	if err != nil {
		t.Fatalf("orphans post-tomb: %v", err)
	}
	if len(orphans) != 1 || orphans[0].MsgID != 4 {
		t.Fatalf("expected deep.txt (msg=4) orphaned, got %v", orphans)
	}
}

func TestOrphanedFilesScopedByChannel(t *testing.T) {
	db := newTestDB(t)
	const otherChan int64 = 9999
	if _, err := db.Exec(`
		INSERT INTO channels (channel_id, access_hash, title, kind, joined_at)
		VALUES (?, 0, 'Other', 'shared', 0)
	`, otherChan); err != nil {
		t.Fatalf("seed channel: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO files (channel_id, msg_id, name, size, parent_id, upload_time, uploader_user_id, tombstoned)
		VALUES (?, ?, 'leak.png', 0, 'd:gone', 0, 0, 0)
	`, otherChan, 1); err != nil {
		t.Fatalf("seed file: %v", err)
	}
	mustOp(t, db, 1, Op{Type: OpFileUpload, Parent: RootParent, Name: "mine.png"})

	orphans, err := OrphanedFiles(db, testChan)
	if err != nil {
		t.Fatalf("orphans: %v", err)
	}
	for _, o := range orphans {
		if o.ParentID == "d:gone" {
			t.Fatal("orphan from another channel leaked into result")
		}
	}
}

func TestOrphanedFilesReportsEncryptionMetadata(t *testing.T) {
	db := newTestDB(t)
	// An encrypted file under a missing parent surfaces as an orphan, and must
	// carry its encryption flag + plaintext size through the read path so the
	// orphan view can show a lock badge and the real (decrypted) size rather
	// than the ciphertext length.
	if _, err := db.Exec(`
		INSERT INTO files (channel_id, msg_id, name, size, parent_id, upload_time, uploader_user_id, tombstoned, encrypted, plaintext_size)
		VALUES (?, ?, 'secret.jpg', 5120, 'd:gone', 0, 0, 0, 1, 4096)
	`, testChan, 1); err != nil {
		t.Fatalf("seed encrypted file: %v", err)
	}

	orphans, err := OrphanedFiles(db, testChan)
	if err != nil {
		t.Fatalf("orphans: %v", err)
	}
	if len(orphans) != 1 {
		t.Fatalf("expected 1 orphan, got %d", len(orphans))
	}
	o := orphans[0]
	if !o.Encrypted {
		t.Error("orphan Encrypted = false, want true")
	}
	if o.PlaintextSize != 4096 {
		t.Errorf("orphan PlaintextSize = %d, want 4096", o.PlaintextSize)
	}
	// On-wire size is the ciphertext length, distinct from the plaintext size.
	if o.Size != 5120 {
		t.Errorf("orphan Size = %d, want 5120", o.Size)
	}
}

func TestNextFreeFolderNameSkipsTakenSiblings(t *testing.T) {
	db := newTestDB(t)
	mustOp(t, db, 1, Op{Type: OpMkdir, Obj: "d:photos", Parent: RootParent, Name: "Photos"})
	mustOp(t, db, 2, Op{Type: OpMkdir, Obj: "d:photos2", Parent: RootParent, Name: "Photos (2)"})

	name, err := NextFreeFolderName(db, testChan, RootParent, "Photos")
	if err != nil {
		t.Fatalf("NextFreeFolderName Photos: %v", err)
	}
	if name != "Photos (3)" {
		t.Fatalf("NextFreeFolderName Photos = %q, want %q", name, "Photos (3)")
	}

	name, err = NextFreeFolderName(db, testChan, RootParent, "Unused")
	if err != nil {
		t.Fatalf("NextFreeFolderName Unused: %v", err)
	}
	if name != "Unused" {
		t.Fatalf("NextFreeFolderName Unused = %q, want %q", name, "Unused")
	}
}
