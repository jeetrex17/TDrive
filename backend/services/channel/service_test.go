package channel

import (
	"context"
	"database/sql"
	"testing"

	"TDrive/backend/projection"
	"TDrive/backend/tgclient"

	_ "modernc.org/sqlite"
)

const personalChannelID int64 = 12345

type syncRecorder struct {
	calls []int64
}

func (s *syncRecorder) InitialSyncEmptyChannel(ctx context.Context, channelID int64) error {
	s.calls = append(s.calls, channelID)
	return nil
}

func newServiceDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := projection.MigratePersonalChannel(db, personalChannelID); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}

func newService(t *testing.T) (*Service, *tgclient.Fake, *syncRecorder, *int64) {
	t.Helper()
	db := newServiceDB(t)
	tg := tgclient.NewFake(77)
	syncer := &syncRecorder{}
	active := personalChannelID
	svc := &Service{
		DB:   db,
		TG:   tg,
		Sync: syncer,
		GetActive: func() int64 {
			return active
		},
		SetActive: func(id int64) {
			active = id
		},
	}
	return svc, tg, syncer, &active
}

func TestCreateSharedDriveUsesTelegramClientAndStoresChannel(t *testing.T) {
	svc, _, _, active := newService(t)

	got, err := svc.CreateSharedDrive(context.Background(), "Goa", true)
	if err != nil {
		t.Fatalf("create shared drive: %v", err)
	}
	if got.ChannelID == 0 || got.AccessHash == 0 {
		t.Fatalf("missing channel identity: %+v", got)
	}
	if got.Title != "Goa" || got.Kind != projection.KindShared || got.InviteLink == "" {
		t.Fatalf("bad channel row: %+v", got)
	}
	if *active != got.ChannelID {
		t.Fatalf("active = %d, want %d", *active, got.ChannelID)
	}

	stored, err := projection.GetChannel(svc.DB, got.ChannelID)
	if err != nil {
		t.Fatalf("get stored channel: %v", err)
	}
	if stored.AccessHash != got.AccessHash || stored.InviteLink != got.InviteLink {
		t.Fatalf("stored mismatch: %+v vs %+v", stored, got)
	}
}

func TestJoinSharedDrivePendingPersistsRequest(t *testing.T) {
	svc, tg, _, _ := newService(t)
	tg.SeedInvite("need-approval", tgclient.InviteInfo{
		RequestNeeded: true,
		Title:         "Private",
	})

	got, err := svc.JoinSharedDrive(context.Background(), "https://t.me/+need-approval")
	if err != nil {
		t.Fatalf("join shared drive: %v", err)
	}
	if got.Status != JoinStatusPending || got.Pending == nil {
		t.Fatalf("got %+v, want pending", got)
	}
	if got.Pending.InviteHash != "need-approval" || got.Pending.Title != "Private" {
		t.Fatalf("bad pending row: %+v", got.Pending)
	}
	if reqs := tg.RequestedJoins(); len(reqs) != 1 || reqs[0] != "need-approval" {
		t.Fatalf("requested joins = %+v", reqs)
	}

	stored, err := projection.GetPendingJoin(svc.DB, "need-approval")
	if err != nil {
		t.Fatalf("pending not stored: %v", err)
	}
	if stored.Status != projection.PendingJoinStatusPending {
		t.Fatalf("status = %q", stored.Status)
	}
}

func TestCheckPendingJoinRegistersApprovedDrive(t *testing.T) {
	svc, tg, syncer, active := newService(t)
	tg.SeedInvite("pending-hash", tgclient.InviteInfo{
		RequestNeeded: true,
		Title:         "Waiting",
	})
	if _, err := svc.JoinSharedDrive(context.Background(), "https://t.me/+pending-hash"); err != nil {
		t.Fatalf("seed pending: %v", err)
	}

	tg.SeedInvite("pending-hash", tgclient.InviteInfo{
		AlreadyJoined: true,
		Title:         "Approved",
		ChannelID:     22222,
		AccessHash:    33333,
	})
	got, err := svc.CheckPendingJoin(context.Background(), "pending-hash")
	if err != nil {
		t.Fatalf("check pending: %v", err)
	}
	if got.Status != JoinStatusJoined || got.Channel == nil {
		t.Fatalf("got %+v, want joined", got)
	}
	if got.Channel.ChannelID != 22222 || got.Channel.Title != "Approved" {
		t.Fatalf("bad channel: %+v", got.Channel)
	}
	if *active != 22222 {
		t.Fatalf("active = %d, want 22222", *active)
	}
	if len(syncer.calls) != 1 || syncer.calls[0] != 22222 {
		t.Fatalf("sync calls = %+v", syncer.calls)
	}
	if _, err := projection.GetPendingJoin(svc.DB, "pending-hash"); err == nil {
		t.Fatalf("pending row still exists or wrong error: %v", err)
	}
}

func TestHideJoinRequestApprovesViaTelegramClient(t *testing.T) {
	svc, tg, _, _ := newService(t)
	peer := tgclient.InputPeer{ChannelID: 44444, AccessHash: 55555}
	tg.SeedChannel(peer, "Shared")
	if err := projection.InsertChannel(svc.DB, projection.Channel{
		ChannelID:            peer.ChannelID,
		AccessHash:           peer.AccessHash,
		Title:                "Shared",
		Kind:                 projection.KindShared,
		PersonalBackfillDone: true,
	}); err != nil {
		t.Fatalf("seed channel: %v", err)
	}
	tg.SeedJoinRequests(peer.ChannelID, tgclient.JoinRequest{
		UserID:      99,
		AccessHash:  123,
		DisplayName: "Friend",
	})

	if err := svc.HideJoinRequest(context.Background(), peer.ChannelID, 99, true); err != nil {
		t.Fatalf("hide join request: %v", err)
	}
	hidden := tg.HiddenJoinRequests()
	if len(hidden) != 1 || hidden[0].UserID != 99 || !hidden[0].Approved {
		t.Fatalf("hidden requests = %+v", hidden)
	}
}

func TestLeaveSharedDriveDeletesLocalRowsAndSwitchesToPersonal(t *testing.T) {
	svc, tg, _, active := newService(t)
	peer := tgclient.InputPeer{ChannelID: 77777, AccessHash: 88888}
	tg.SeedChannel(peer, "Leaving")
	if err := projection.InsertChannel(svc.DB, projection.Channel{
		ChannelID:            peer.ChannelID,
		AccessHash:           peer.AccessHash,
		Title:                "Leaving",
		Kind:                 projection.KindShared,
		PersonalBackfillDone: true,
	}); err != nil {
		t.Fatalf("seed channel: %v", err)
	}
	*active = peer.ChannelID

	if err := svc.LeaveSharedDrive(context.Background(), peer.ChannelID); err != nil {
		t.Fatalf("leave shared drive: %v", err)
	}
	if _, err := projection.GetChannel(svc.DB, peer.ChannelID); err == nil {
		t.Fatalf("shared channel still exists or wrong error: %v", err)
	}
	if *active != personalChannelID {
		t.Fatalf("active = %d, want personal %d", *active, personalChannelID)
	}
	left := tg.LeftChannels()
	if len(left) != 1 || left[0] != peer {
		t.Fatalf("left channels = %+v", left)
	}
}
