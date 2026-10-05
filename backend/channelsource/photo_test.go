package channelsource

import (
	"database/sql"
	"testing"

	"TDrive/backend/media"
	"TDrive/backend/tgclient"
)

func TestPhotoIsKeptForAnAddedChannelUntilItChanges(t *testing.T) {
	ctx := t.Context()
	fake := tgclient.NewFake(testAccountID)
	fake.SeedJoinedBroadcastChannels(tgclient.JoinedBroadcastChannel{ID: testChannelID, AccessHash: 77, Title: "Cinema", PhotoID: 501})
	fake.SeedChannelPhoto(501, []byte("first picture"))
	fake.SeedChannelPhoto(502, []byte("second picture"))
	sources, _, _ := sourceFixture(t, fake, fake)
	if _, err := sources.Connect(ctx, testChannelID); err != nil {
		t.Fatal(err)
	}

	for range 2 {
		photo, err := sources.Photo(ctx, testChannelID)
		if err != nil || string(photo) != "first picture" {
			t.Fatalf("photo = %q, %v", photo, err)
		}
	}
	if got := fake.PhotoDownloads(); got != 1 {
		t.Fatalf("downloads after two views = %d, want 1", got)
	}

	// Opening the channel sees its new picture; the next view fetches it.
	fake.SeedJoinedBroadcastChannels(tgclient.JoinedBroadcastChannel{ID: testChannelID, AccessHash: 77, Title: "Cinema", PhotoID: 502})
	if _, err := sources.Page(ctx, testChannelID, 0, 1, "", "all"); err != nil {
		t.Fatal(err)
	}
	photo, err := sources.Photo(ctx, testChannelID)
	if err != nil || string(photo) != "second picture" || fake.PhotoDownloads() != 2 {
		t.Fatalf("photo after change = %q, %v, downloads %d", photo, err, fake.PhotoDownloads())
	}
}

func TestPhotoBackfillsAChannelAddedByAnEarlierBuild(t *testing.T) {
	ctx := t.Context()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	// The table as the first build of this feature created it.
	if _, err := db.Exec(`CREATE TABLE connected_channel_sources (
		account_id INTEGER NOT NULL, channel_id INTEGER NOT NULL, title TEXT NOT NULL,
		username TEXT NOT NULL DEFAULT '', protected INTEGER NOT NULL DEFAULT 0, generation TEXT NOT NULL,
		PRIMARY KEY(account_id, channel_id));
		INSERT INTO connected_channel_sources VALUES(1234, 7711, 'Cinema', '', 0, 'kept');`); err != nil {
		t.Fatal(err)
	}
	fake := tgclient.NewFake(testAccountID)
	fake.SeedJoinedBroadcastChannels(tgclient.JoinedBroadcastChannel{ID: testChannelID, AccessHash: 77, Title: "Cinema", PhotoID: 501})
	fake.SeedChannelPhoto(501, []byte("picture"))
	streams := media.NewService(media.Config{DB: db, Ranges: fake})
	t.Cleanup(func() { _ = streams.Close() })
	sources, err := NewService(db, fake, streams)
	if err != nil {
		t.Fatal(err)
	}

	photo, err := sources.Photo(ctx, testChannelID)
	if err != nil || string(photo) != "picture" {
		t.Fatalf("photo = %q, %v", photo, err)
	}
	var accessHash, photoID int64
	var generation string
	if err := db.QueryRow(`SELECT access_hash, photo_id, generation FROM connected_channel_sources WHERE channel_id=7711`).
		Scan(&accessHash, &photoID, &generation); err != nil || accessHash != 77 || photoID != 501 || generation != "kept" {
		t.Fatalf("stored row = %d, %d, %q, %v", accessHash, photoID, generation, err)
	}
}

func TestPhotoOfAChannelNotAddedYetUsesTheLastWalk(t *testing.T) {
	ctx := t.Context()
	fake := tgclient.NewFake(testAccountID)
	fake.SeedJoinedBroadcastChannels(
		tgclient.JoinedBroadcastChannel{ID: 7801, AccessHash: 81, Title: "News", PhotoID: 601},
		tgclient.JoinedBroadcastChannel{ID: 7802, AccessHash: 82, Title: "Plain"},
	)
	fake.SeedChannelPhoto(601, []byte("news picture"))
	sources, _, db := sourceFixture(t, fake, fake)
	if _, err := sources.ListCandidates(ctx); err != nil {
		t.Fatal(err)
	}

	photo, err := sources.Photo(ctx, 7801)
	if err != nil || string(photo) != "news picture" {
		t.Fatalf("candidate photo = %q, %v", photo, err)
	}
	photo, err = sources.Photo(ctx, 7802)
	if err != nil || photo != nil || fake.PhotoDownloads() != 1 {
		t.Fatalf("channel without a photo = %q, %v, downloads %d", photo, err, fake.PhotoDownloads())
	}
	var rows int
	if err := db.QueryRow(`SELECT COUNT(*) FROM connected_channel_sources`).Scan(&rows); err != nil || rows != 0 {
		t.Fatalf("a candidate photo stored %d rows, %v", rows, err)
	}
}
