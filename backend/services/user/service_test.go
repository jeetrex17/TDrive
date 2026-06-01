package user

import (
	"context"
	"database/sql"
	"testing"

	"TDrive/backend/projection"
	"TDrive/backend/tgclient"

	_ "modernc.org/sqlite"
)

const personalChannelID int64 = 424242

type testPeerResolver struct {
	peer tgclient.InputPeer
}

func (r testPeerResolver) ResolvePeer(ctx context.Context, channelID int64) (tgclient.InputPeer, error) {
	return r.peer, nil
}

func newTestService(t *testing.T) (*Service, *sql.DB, *tgclient.Fake, *int64) {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := projection.MigratePersonalChannel(db, personalChannelID); err != nil {
		t.Fatalf("migrate personal: %v", err)
	}

	fakeTG := tgclient.NewFake(7)
	peer := tgclient.InputPeer{ChannelID: personalChannelID, AccessHash: 99}
	fakeTG.SeedChannel(peer, "Personal")
	actor := int64(7)
	svc := &Service{
		DB:    db,
		TG:    fakeTG,
		Peers: testPeerResolver{peer: peer},
		ActorID: func(ctx context.Context) (int64, error) {
			return actor, nil
		},
		Active: func() int64 {
			return personalChannelID
		},
	}
	return svc, db, fakeTG, &actor
}

func projectFile(t *testing.T, db *sql.DB, msgID int64, actorID int64, name string) {
	t.Helper()
	op := projection.Op{
		Type:           projection.OpFileUpload,
		Parent:         "",
		Name:           name,
		FileSize:       10,
		FileUploadTime: msgID,
	}
	header := projection.Format(op)
	if _, err := projection.ProjectFromOp(db, personalChannelID, msgID, op, actorID, header); err != nil {
		t.Fatalf("project file: %v", err)
	}
}

func TestResolveUsernamesMapsIDsToNames(t *testing.T) {
	svc, db, fakeTG, _ := newTestService(t)
	projectFile(t, db, 100, 20, "a.txt")
	projectFile(t, db, 101, 21, "b.txt")
	fakeTG.SeedUser(tgclient.UserProfile{ID: 20, FirstName: "Jeet", LastName: "Raj"})
	fakeTG.SeedUser(tgclient.UserProfile{ID: 21, Username: "friend"})

	got, err := svc.ResolveUsernames(context.Background(), []int64{20, 21, 20, 0})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if got["20"] != "Jeet R." {
		t.Fatalf("user 20 = %q, want Jeet R.", got["20"])
	}
	if got["21"] != "friend" {
		t.Fatalf("user 21 = %q, want friend", got["21"])
	}
}

func TestResolveUsernamesSelfTaggedAsYou(t *testing.T) {
	svc, _, fakeTG, _ := newTestService(t)
	fakeTG.SeedUser(tgclient.UserProfile{ID: 7, FirstName: "Actual"})

	got, err := svc.ResolveUsernames(context.Background(), []int64{7})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if got["7"] != "You" {
		t.Fatalf("self = %q, want You", got["7"])
	}
	if calls := fakeTG.ResolveUsersFromMessagesCalls(); calls != 0 {
		t.Fatalf("telegram resolve calls = %d, want 0", calls)
	}
}

func TestMeCachesAfterFirstCall(t *testing.T) {
	svc, _, fakeTG, _ := newTestService(t)
	fakeTG.SeedUser(tgclient.UserProfile{ID: 7, FirstName: "Jeet", LastName: "Raj", Username: "jeet", PhotoBytes: []byte("photo")})

	first, err := svc.Me(context.Background())
	if err != nil {
		t.Fatalf("me first: %v", err)
	}
	second, err := svc.Me(context.Background())
	if err != nil {
		t.Fatalf("me second: %v", err)
	}
	if first != second {
		t.Fatalf("cached me mismatch: first=%+v second=%+v", first, second)
	}
	if calls := fakeTG.SelfProfileCalls(); calls != 1 {
		t.Fatalf("self profile calls = %d, want 1", calls)
	}
}

func TestClearCacheDropsSelf(t *testing.T) {
	svc, _, fakeTG, _ := newTestService(t)
	fakeTG.SeedUser(tgclient.UserProfile{ID: 7, FirstName: "One"})
	if _, err := svc.Me(context.Background()); err != nil {
		t.Fatalf("me first: %v", err)
	}

	svc.ClearCache()
	fakeTG.SeedUser(tgclient.UserProfile{ID: 7, FirstName: "Two"})
	got, err := svc.Me(context.Background())
	if err != nil {
		t.Fatalf("me second: %v", err)
	}
	if got.DisplayName != "Two" {
		t.Fatalf("display name = %q, want Two", got.DisplayName)
	}
	if calls := fakeTG.SelfProfileCalls(); calls != 2 {
		t.Fatalf("self profile calls = %d, want 2", calls)
	}
}
