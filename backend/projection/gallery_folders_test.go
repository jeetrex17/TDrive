package projection

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
)

// seedFolderMedia puts `count` images in one folder, newest last, so the
// caller can assert which one becomes the cover.
func seedFolderMedia(t testing.TB, db *sql.DB, channelID int64, folderID string, firstMsgID, count int, baseTime int64) {
	t.Helper()
	for i := range count {
		msgID := firstMsgID + i
		if _, err := db.Exec(
			`INSERT INTO files(channel_id,msg_id,name,size,parent_id,upload_time,content_msg_id,content_hash,revision)
			 VALUES(?,?,?,1024,?,?,?,?,?)`,
			channelID, msgID, fmt.Sprintf("photo-%d.jpg", msgID), folderID,
			baseTime+int64(i), msgID+1000000, fmt.Sprintf("hash-%d", msgID), 3,
		); err != nil {
			t.Fatal(err)
		}
	}
}

func seedMediaFolder(t testing.TB, db *sql.DB, channelID int64, id, name string) {
	t.Helper()
	if _, err := db.Exec(
		`INSERT INTO folders(channel_id,id,name,parent_id,tombstoned,revision) VALUES(?,?,?,'',0,1)`,
		channelID, id, name,
	); err != nil {
		t.Fatal(err)
	}
}

func TestMediaFoldersGroupsByFolderWithNewestCover(t *testing.T) {
	db := newTestDB(t)
	seedMediaFolder(t, db, testChan, "d:camera", "Camera")
	seedMediaFolder(t, db, testChan, "d:shots", "Screenshots")
	// Camera is older but larger; Screenshots holds the most recent item.
	seedFolderMedia(t, db, testChan, "d:camera", 100, 5, 1_700_000_000)
	seedFolderMedia(t, db, testChan, "d:shots", 200, 2, 1_700_100_000)

	folders, err := MediaFolders(context.Background(), db, testChan)
	if err != nil {
		t.Fatalf("MediaFolders: %v", err)
	}
	if len(folders) != 2 {
		t.Fatalf("folders = %d, want 2: %+v", len(folders), folders)
	}
	// Newest first: the grid opens on what the user just added.
	if folders[0].Name != "Screenshots" || folders[1].Name != "Camera" {
		t.Fatalf("order = %q, %q", folders[0].Name, folders[1].Name)
	}
	if folders[1].ItemCount != 5 || folders[0].ItemCount != 2 {
		t.Fatalf("counts = %d, %d", folders[0].ItemCount, folders[1].ItemCount)
	}
	// The cover is the newest item in its own folder, not the drive's.
	if folders[1].CoverMsgID != 104 {
		t.Fatalf("Camera cover = %d, want the newest of 100..104", folders[1].CoverMsgID)
	}
	if folders[0].CoverMsgID != 201 {
		t.Fatalf("Screenshots cover = %d, want 201", folders[0].CoverMsgID)
	}
	// The revision addresses the thumbnail; a wrong one is refused as stale.
	if folders[1].CoverRevision != 3 {
		t.Fatalf("cover revision = %d, want the file's live 3", folders[1].CoverRevision)
	}
}

func TestMediaFoldersCountsOnlyDirectChildrenAndSkipsEmptyBranches(t *testing.T) {
	db := newTestDB(t)
	// A branch that holds only a subfolder, the shape photo backup creates.
	seedMediaFolder(t, db, testChan, "d:backup", "Photo backup")
	seedMediaFolder(t, db, testChan, "d:roll", "Camera Roll")
	seedFolderMedia(t, db, testChan, "d:roll", 300, 4, 1_700_000_000)

	folders, err := MediaFolders(context.Background(), db, testChan)
	if err != nil {
		t.Fatal(err)
	}
	// The branch gets no tile: it is a route, not a place with photos in it.
	if len(folders) != 1 || folders[0].FolderID != "d:roll" {
		t.Fatalf("folders = %+v, want only the leaf that holds media", folders)
	}
	if folders[0].ItemCount != 4 {
		t.Fatalf("count = %d, want 4 direct children", folders[0].ItemCount)
	}
}

func TestMediaFoldersGivesTheDriveRootATileAndIgnoresDeletedMedia(t *testing.T) {
	db := newTestDB(t)
	seedFolderMedia(t, db, testChan, "", 400, 3, 1_700_000_000)

	folders, err := MediaFolders(context.Background(), db, testChan)
	if err != nil {
		t.Fatal(err)
	}
	if len(folders) != 1 || folders[0].FolderID != "" || folders[0].ItemCount != 3 {
		t.Fatalf("root tile = %+v", folders)
	}

	// Deleting media must take it out of the count. gallery_items is trigger
	// maintained, so this also proves the tile reads the maintained table
	// rather than re-deriving what counts as a picture.
	if _, err := db.Exec(`UPDATE files SET tombstoned=1 WHERE channel_id=? AND msg_id=?`, testChan, 400); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`DELETE FROM gallery_items WHERE channel_id=? AND msg_id=?`, testChan, 400); err != nil {
		t.Fatal(err)
	}
	folders, err = MediaFolders(context.Background(), db, testChan)
	if err != nil {
		t.Fatal(err)
	}
	if len(folders) != 1 || folders[0].ItemCount != 2 {
		t.Fatalf("after delete = %+v, want 2 left", folders)
	}
}

func TestMediaFolderPageAndTimelineSeeOnlyTheirFolder(t *testing.T) {
	db := newTestDB(t)
	seedMediaFolder(t, db, testChan, "d:camera", "Camera")
	seedMediaFolder(t, db, testChan, "d:shots", "Screenshots")
	seedFolderMedia(t, db, testChan, "d:camera", 500, 6, 1_700_000_000)
	seedFolderMedia(t, db, testChan, "d:shots", 600, 9, 1_700_000_000)

	timeline, err := MediaFolderTimeline(context.Background(), db, testChan, "d:camera")
	if err != nil {
		t.Fatalf("MediaFolderTimeline: %v", err)
	}
	if timeline.TotalCount != 6 {
		t.Fatalf("total = %d, want only Camera's 6", timeline.TotalCount)
	}
	if len(timeline.Buckets) == 0 || timeline.Buckets[0].StartIndex != 0 {
		t.Fatalf("buckets = %+v", timeline.Buckets)
	}

	page, err := MediaFolderPage(context.Background(), db, testChan, "d:camera", "", 4)
	if err != nil {
		t.Fatalf("MediaFolderPage: %v", err)
	}
	if len(page.Items) != 4 || page.NextCursor == "" {
		t.Fatalf("first page = %d items, cursor %q", len(page.Items), page.NextCursor)
	}
	for _, item := range page.Items {
		if item.MsgID < 500 || item.MsgID > 505 {
			t.Fatalf("page leaked msg_id %d from another folder", item.MsgID)
		}
	}
	// The cursor keeps counting within the folder, so the second page is the
	// remainder and stops rather than running into the other folder.
	rest, err := MediaFolderPage(context.Background(), db, testChan, "d:camera", page.NextCursor, 4)
	if err != nil {
		t.Fatal(err)
	}
	if len(rest.Items) != 2 || rest.NextCursor != "" {
		t.Fatalf("second page = %d items, cursor %q", len(rest.Items), rest.NextCursor)
	}
	if rest.StartIndex != 4 {
		t.Fatalf("second page starts at %d, want 4", rest.StartIndex)
	}
}
