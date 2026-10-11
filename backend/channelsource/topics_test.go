package channelsource

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"TDrive/backend/tgclient"
)

func TestForumMediaScopesGeneralSearchAndPages(t *testing.T) {
	fake := tgclient.NewFake(testAccountID)
	fake.SeedMediaSourcePeers(tgclient.SourcePeer{Kind: tgclient.PeerSupergroup, ID: 77, AccessHash: 99, Title: "Forum", Forum: true})
	fake.SeedForumTopics(77, tgclient.ForumTopic{ID: 1, Title: "Lobby"}, tgclient.ForumTopic{ID: 42, Title: "Movies", Closed: true})
	for _, message := range []tgclient.HistoryMessage{
		{MsgID: 50, TopicID: 42, Text: "match", DocumentID: 50},
		{MsgID: 49, TopicID: 0, Text: "match", DocumentID: 49},
		{MsgID: 48, TopicID: 42, Text: "match", DocumentID: 48},
		{MsgID: 47, TopicID: 0, Text: "other", DocumentID: 47},
	} {
		message.ChannelID, message.PeerKind, message.HasMedia = 77, tgclient.PeerSupergroup, true
		message.DocumentName, message.MimeType, message.MediaSize = "clip.mp4", "video/mp4", 100
		fake.SeedHistory(message)
	}
	sources, _, _ := sourceFixture(t, fake, fake)
	connected := connectTyped(t, sources, tgclient.PeerSupergroup, 77, "")
	for _, test := range []struct {
		topic  int64
		search string
		offset int64
		want   []int64
	}{
		{0, "", 0, []int64{50, 49, 48, 47}},
		{1, "", 0, []int64{49, 47}},
		{1, "match", 0, []int64{49}},
		{42, "", 49, []int64{48}},
		{42, "match", 0, []int64{50, 48}},
	} {
		page, err := sources.PageSourceByTopic(t.Context(), "supergroup", 77, test.topic, test.offset, 20, test.search, "all")
		if err != nil {
			t.Fatal(err)
		}
		ids := make([]int64, len(page.Items))
		for i, item := range page.Items {
			ids[i] = item.MsgID
			if test.topic > 0 && item.TopicID != test.topic {
				t.Fatalf("topic leaked: %#v", item)
			}
		}
		if !page.Forum || page.TopicID != test.topic || !slices.Equal(ids, test.want) {
			t.Fatalf("scope %d: page %#v, want %v", test.topic, page, test.want)
		}
	}
	topics, err := sources.ListSourceTopics(t.Context(), "supergroup", 77, 0, 0, 0, 20, "", testAccountID, connected.Generation)
	if err != nil || len(topics.Topics) != 2 || topics.Topics[0].Title != "Lobby" {
		t.Fatalf("topics = %#v, %v", topics, err)
	}
	if _, err := sources.PageSourceByTopic(t.Context(), "supergroup", 77, 999, 0, 20, "", "all"); !errors.Is(err, ErrTopicUnavailable) {
		t.Fatalf("deleted topic = %v", err)
	}
	if _, err := sources.ListSourceTopics(t.Context(), "supergroup", 77, 0, 0, 0, 20, "", testAccountID, "stale"); !errors.Is(err, ErrNotConnected) {
		t.Fatalf("stale generation = %v", err)
	}
	for _, test := range []struct {
		account    int64
		generation string
		want       error
	}{
		{testAccountID, "stale", ErrNotConnected},
		{testAccountID + 1, connected.Generation, ErrUnavailable},
		{testAccountID, "", ErrInvalidPage},
		{-1, "", ErrInvalidPage},
	} {
		if _, err := sources.PageSourceByTopicScoped(t.Context(), "supergroup", 77, 1, 0, 20, "", "all", test.account, test.generation); !errors.Is(err, test.want) {
			t.Fatalf("media request scope (%d, %q): %v, want %v", test.account, test.generation, err, test.want)
		}
	}
}

func TestForumColumnMigrationPreservesConnectedSources(t *testing.T) {
	fake := tgclient.NewFake(testAccountID)
	sources, streams, db := sourceFixture(t, fake, fake)
	// Recreate the pre-topic schema with an existing connection and cached photo.
	if _, err := db.Exec(`DROP TABLE connected_media_sources;
		CREATE TABLE connected_media_sources (
			account_id INTEGER NOT NULL, peer_kind TEXT NOT NULL, peer_id INTEGER NOT NULL,
			title TEXT NOT NULL, username TEXT NOT NULL DEFAULT '', protected INTEGER NOT NULL DEFAULT 0,
			generation TEXT NOT NULL, access_hash INTEGER NOT NULL DEFAULT 0,
			photo_id INTEGER NOT NULL DEFAULT 0, photo BLOB, public_resolved INTEGER NOT NULL DEFAULT 0,
			PRIMARY KEY(account_id, peer_kind, peer_id));
		INSERT INTO connected_media_sources VALUES(1234, 'supergroup', 77, 'Forum', '', 1, 'retained', 99, 500, x'0102', 0)`); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		var err error
		sources, err = NewService(db, fake, streams, fake.SelfID)
		if err != nil {
			t.Fatal(err)
		}
	}
	stored, _, err := sources.storedSource(t.Context(), testAccountID, "supergroup", 77)
	if err != nil || stored.Generation != "retained" || stored.accessHash != 99 || !stored.Protected || stored.Forum {
		t.Fatalf("migrated connection: %#v, %v", stored, err)
	}
	var photo []byte
	if err := db.QueryRow(`SELECT photo FROM connected_media_sources WHERE account_id=1234 AND peer_kind='supergroup' AND peer_id=77`).Scan(&photo); err != nil || !slices.Equal(photo, []byte{1, 2}) {
		t.Fatalf("cached photo: %v, %v", photo, err)
	}
	fake.SeedMediaSourcePeers(tgclient.SourcePeer{Kind: tgclient.PeerSupergroup, ID: 77, AccessHash: 99, Title: "Forum", Forum: true, Protected: true, PhotoID: 500})
	if _, err := sources.PageSource(t.Context(), "supergroup", 77, 0, 20, "", "all"); err != nil {
		t.Fatal(err)
	}
	connected, err := sources.ListAllConnected(t.Context())
	if err != nil || len(connected) != 1 || !connected[0].Forum || connected[0].Generation != "retained" {
		t.Fatalf("refreshed capability: %#v, %v", connected, err)
	}
}

// This wrapper injects changes at the actual remote-read boundary while the
// service owns a real single-connection SQLite database.
type forumReadClient struct {
	*tgclient.Fake
	historyReads  int
	metadataReads int
	metadataErr   error
	afterHistory  func()
}

func (client *forumReadClient) GetForumHistory(ctx context.Context, peer tgclient.InputPeer, topicID, offsetID int64, limit int) ([]tgclient.HistoryMessage, error) {
	client.historyReads++
	messages, err := client.Fake.GetForumHistory(ctx, peer, topicID, offsetID, limit)
	if client.afterHistory != nil {
		client.afterHistory()
	}
	return messages, err
}

func (client *forumReadClient) GetForumTopicsByID(ctx context.Context, peer tgclient.InputPeer, ids []int64) ([]tgclient.ForumTopic, error) {
	client.metadataReads++
	if client.metadataErr != nil {
		return nil, client.metadataErr
	}
	return client.Fake.GetForumTopicsByID(ctx, peer, ids)
}

func TestForumPagingBoundsSparseHistoryAndRejectsChangedScope(t *testing.T) {
	for _, change := range []string{"none", "account", "connection"} {
		t.Run(change, func(t *testing.T) {
			fake := tgclient.NewFake(testAccountID)
			fake.SeedMediaSourcePeers(tgclient.SourcePeer{Kind: tgclient.PeerSupergroup, ID: 77, AccessHash: 99, Forum: true, Title: "Forum"})
			fake.SeedForumTopics(77, tgclient.ForumTopic{ID: 42, Title: "Movies"})
			for id := range 451 {
				message := tgclient.HistoryMessage{PeerKind: tgclient.PeerSupergroup, ChannelID: 77, MsgID: int64(id + 1), TopicID: 42}
				if id == 0 {
					message.HasMedia, message.DocumentID, message.MediaSize, message.MimeType = true, 99, 100, "video/mp4"
				}
				fake.SeedHistory(message)
			}
			client := &forumReadClient{Fake: fake}
			sources, _, _ := sourceFixture(t, client, fake)
			connected := connectTyped(t, sources, tgclient.PeerSupergroup, 77, "")
			if change == "account" {
				client.afterHistory = func() { fake.SetSelfID(testAccountID + 1) }
			}
			if change == "connection" {
				client.afterHistory = func() {
					client.afterHistory = nil
					if _, err := sources.DisconnectSourceWithGate(t.Context(), "supergroup", 77, testAccountID, connected.Generation, func(remove func() error) error { return remove() }); err != nil {
						t.Fatal(err)
					}
					connectTyped(t, sources, tgclient.PeerSupergroup, 77, "")
				}
			}
			page, err := sources.PageSourceByTopic(t.Context(), "supergroup", 77, 42, 0, 20, "", "all")
			switch change {
			case "account":
				if !errors.Is(err, ErrUnavailable) {
					t.Fatalf("changed account: %v", err)
				}
				return
			case "connection":
				if !errors.Is(err, ErrNotConnected) {
					t.Fatalf("replaced connection: %v", err)
				}
				return
			}
			if err != nil || client.historyReads != 4 || len(page.Items) != 0 || page.NextOffsetID != 52 || !page.HasMore {
				t.Fatalf("sparse page %#v, reads %d, error %v", page, client.historyReads, err)
			}
			older, err := sources.PageSourceByTopic(t.Context(), "supergroup", 77, 42, page.NextOffsetID, 20, "", "all")
			if err != nil || len(older.Items) != 1 || older.Items[0].MsgID != 1 || older.HasMore {
				t.Fatalf("older page %#v, %v", older, err)
			}
			client.metadataErr = tgclient.NewFloodWaitError(time.Millisecond)
			metadataReads := client.metadataReads
			all, err := sources.PageSource(t.Context(), "supergroup", 77, 0, 20, "", "all")
			// The first page is sparse too; continue to the media using its cursor.
			if err != nil {
				t.Fatal(err)
			}
			all, err = sources.PageSource(t.Context(), "supergroup", 77, all.NextOffsetID, 20, "", "all")
			if err != nil || len(all.Items) != 1 || all.Items[0].TopicID != 42 || len(all.Topics) != 0 || client.metadataReads != metadataReads+1 {
				t.Fatalf("metadata failure hid media: %#v, %v", all, err)
			}
			fake.SeedMediaSourcePeers(tgclient.SourcePeer{Kind: tgclient.PeerSupergroup, ID: 77, AccessHash: 99, Title: "Group"})
			client.metadataErr = nil
			if _, err := sources.PageSourceByTopic(t.Context(), "supergroup", 77, 42, 0, 20, "", "all"); !errors.Is(err, ErrNotForum) {
				t.Fatalf("disabled forum: %v", err)
			}
			plain, err := sources.PageSource(t.Context(), "supergroup", 77, 0, 20, "", "all")
			if err != nil || plain.Forum {
				t.Fatalf("disabled group: %#v, %v", plain, err)
			}
		})
	}
}
