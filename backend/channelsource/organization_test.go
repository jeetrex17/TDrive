package channelsource

import (
	"errors"
	"testing"

	"TDrive/backend/tgclient"
)

func TestSourceOrganizationPersistsAndSeparatesTypedPeers(t *testing.T) {
	ctx := t.Context()
	fake := tgclient.NewFake(testAccountID)
	fake.SeedMediaSourcePeers(
		tgclient.SourcePeer{Kind: tgclient.PeerUser, ID: 44, AccessHash: 99, Title: "Mina"},
		tgclient.SourcePeer{Kind: tgclient.PeerGroup, ID: 44, Title: "Friends"},
	)
	sources, media, db := sourceFixture(t, fake, fake)
	if _, err := db.Exec("PRAGMA foreign_keys = ON"); err != nil {
		t.Fatal(err)
	}
	require := requireOrganization(t)
	user := connectTyped(t, sources, tgclient.PeerUser, 44, "")
	group := connectTyped(t, sources, tgclient.PeerGroup, 44, "")
	org := require(sources.ListSourceGroups(ctx, testAccountID))
	if len(org.Groups) != 0 || len(org.Assignments) != 0 {
		t.Fatalf("existing sources should be ungrouped, got %+v", org)
	}
	org = require(sources.SaveSourceGroup(ctx, testAccountID, "", " Work "))
	if len(org.Groups) != 1 || org.Groups[0].Name != "Work" || org.Groups[0].ID == "" {
		t.Fatalf("new group = %+v", org.Groups)
	}
	groupID := org.Groups[0].ID
	org = require(sources.MoveSourceToGroup(ctx, user.PeerKind, user.PeerID, testAccountID, user.Generation, groupID))
	want := SourceGroupAssignment{PeerKind: user.PeerKind, PeerID: user.PeerID, Generation: user.Generation, GroupID: groupID}
	if len(org.Assignments) != 1 || org.Assignments[0] != want {
		t.Fatalf("move should assign only the user peer, got %+v", org.Assignments)
	}
	reopened, err := NewService(db, fake, media, fake.SelfID)
	if err != nil {
		t.Fatal(err)
	}
	org = require(reopened.SaveSourceGroup(ctx, testAccountID, groupID, "Projects"))
	if len(org.Groups) != 1 || org.Groups[0].Name != "Projects" || len(org.Assignments) != 1 || org.Assignments[0] != want {
		t.Fatalf("reopened and renamed organization = %+v", org)
	}
	if _, err := reopened.MoveSourceToGroup(ctx, group.PeerKind, group.PeerID, testAccountID, "stale", groupID); !errors.Is(err, ErrNotConnected) {
		t.Fatalf("stale connection error = %v", err)
	}
	fake.SetSelfID(testAccountID + 1)
	if _, err := reopened.DeleteSourceGroup(ctx, testAccountID, groupID); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("wrong account delete error = %v", err)
	}
	org = require(reopened.ListSourceGroups(ctx, testAccountID+1))
	if len(org.Groups) != 0 || len(org.Assignments) != 0 {
		t.Fatalf("second account saw %+v", org)
	}
	fake.SetSelfID(testAccountID)
	org = require(reopened.DeleteSourceGroup(ctx, testAccountID, groupID))
	if len(org.Groups) != 0 || len(org.Assignments) != 0 {
		t.Fatalf("delete should return the members to ungrouped, got %+v", org)
	}
	connected, err := reopened.ListAllConnected(ctx)
	if err != nil || len(connected) != 2 {
		t.Fatalf("organization changed Telegram sources = %+v, %v", connected, err)
	}
}

func TestSourceOrganizationRejectsBadNamesAndAbsentGroups(t *testing.T) {
	ctx := t.Context()
	fake := tgclient.NewFake(testAccountID)
	fake.SeedMediaSourcePeers(tgclient.SourcePeer{Kind: tgclient.PeerGroup, ID: 44, Title: "Friends"})
	sources, _, db := sourceFixture(t, fake, fake)
	if _, err := db.Exec("PRAGMA foreign_keys = ON"); err != nil {
		t.Fatal(err)
	}
	require := requireOrganization(t)
	source := connectTyped(t, sources, tgclient.PeerGroup, 44, "")
	for _, name := range []string{" ", "One\x00two"} {
		if _, err := sources.SaveSourceGroup(ctx, testAccountID, "", name); !errors.Is(err, ErrSourceGroupName) {
			t.Fatalf("invalid name %q error = %v", name, err)
		}
	}
	org := require(sources.SaveSourceGroup(ctx, testAccountID, "", "Friends"))
	if _, err := sources.SaveSourceGroup(ctx, testAccountID, "", "FRIENDS"); !errors.Is(err, ErrSourceGroupNameInUse) {
		t.Fatalf("duplicate name error = %v", err)
	}
	groupID := org.Groups[0].ID
	require(sources.DeleteSourceGroup(ctx, testAccountID, groupID))
	if _, err := sources.MoveSourceToGroup(ctx, source.PeerKind, source.PeerID, testAccountID, source.Generation, groupID); !errors.Is(err, ErrSourceGroupMissing) {
		t.Fatalf("move to deleted group error = %v", err)
	}
}

func TestSourceOrganizationDiscardsAssignmentWhenConnectionIsRemoved(t *testing.T) {
	ctx := t.Context()
	fake := tgclient.NewFake(testAccountID)
	fake.SeedMediaSourcePeers(tgclient.SourcePeer{Kind: tgclient.PeerGroup, ID: 44, Title: "Friends"})
	sources, _, db := sourceFixture(t, fake, fake)
	if _, err := db.Exec("PRAGMA foreign_keys = ON"); err != nil {
		t.Fatal(err)
	}
	require := requireOrganization(t)
	source := connectTyped(t, sources, tgclient.PeerGroup, 44, "")
	org := require(sources.SaveSourceGroup(ctx, testAccountID, "", "Friends"))
	require(sources.MoveSourceToGroup(ctx, source.PeerKind, source.PeerID, testAccountID, source.Generation, org.Groups[0].ID))

	_, err := sources.DisconnectSourceWithGate(ctx, source.PeerKind, source.PeerID, testAccountID, source.Generation,
		func(run func() error) error { return run() })
	if err != nil {
		t.Fatal(err)
	}
	connectTyped(t, sources, tgclient.PeerGroup, 44, "")
	org = require(sources.ListSourceGroups(ctx, testAccountID))
	if len(org.Assignments) != 0 || len(org.Groups) != 1 {
		t.Fatalf("reconnected source should be ungrouped, got %+v", org)
	}
}

func requireOrganization(t *testing.T) func(SourceOrganization, error) SourceOrganization {
	t.Helper()
	return func(org SourceOrganization, err error) SourceOrganization {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
		return org
	}
}
