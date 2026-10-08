package channel

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"testing"

	"TDrive/backend/projection"
	"TDrive/backend/tgclient"

	_ "modernc.org/sqlite"
)

const personalChannelID int64 = 12345

type syncRecorder struct {
	calls []int64
	err   error
}

func (s *syncRecorder) InitialSyncEmptyChannel(ctx context.Context, channelID int64) error {
	s.calls = append(s.calls, channelID)
	return s.err
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

type joinRecordingClient struct {
	*tgclient.Fake
	joins int
}

func (c *joinRecordingClient) JoinByInvite(ctx context.Context, hash string) (tgclient.InputPeer, error) {
	c.joins++
	return c.Fake.JoinByInvite(ctx, hash)
}

func TestJoinSharedDriveReportsSyncFailureAndRetainsMembershipForRetry(t *testing.T) {
	svc, fake, syncer, active := newService(t)
	client := &joinRecordingClient{Fake: fake}
	svc.TG = client
	info := tgclient.InviteInfo{Title: "Shared", ChannelID: 22222, AccessHash: 33333}
	fake.SeedInvite("sync-retry", info)
	failure := errors.New("history temporarily unavailable")
	syncer.err = failure
	result, err := svc.JoinSharedDrive(t.Context(), "https://t.me/+sync-retry")
	if !errors.Is(err, failure) || !strings.Contains(err.Error(), "joined drive") || !strings.Contains(err.Error(), "Retry this invite") {
		t.Fatalf("join error = %v; want actionable partial-join error wrapping sync failure", err)
	}
	if result.Status != JoinStatusJoined || result.Channel == nil || result.Channel.ChannelID != 22222 {
		t.Fatalf("result = %+v; want joined identity despite incomplete sync", result)
	}
	stored, lookupErr := projection.GetChannel(svc.DB, 22222)
	if lookupErr != nil || stored.AccessHash != 33333 || *active != 22222 {
		t.Fatalf("stored %+v, err %v, active %d; want retained joined drive", stored, lookupErr, *active)
	}
	if left := fake.LeftChannels(); len(left) != 0 {
		t.Fatalf("rollback left Telegram channels: %v", left)
	}
	// Telegram reports membership after the accepted join, even though the
	// independent history read failed. Retrying must only sync that membership.
	info.AlreadyJoined = true
	fake.SeedInvite("sync-retry", info)
	syncer.err = nil
	result, err = svc.JoinSharedDrive(t.Context(), "https://t.me/+sync-retry")
	if err != nil || result.Channel == nil || result.Channel.ChannelID != 22222 {
		t.Fatalf("retry result %+v, error %v", result, err)
	}
	if client.joins != 1 || len(syncer.calls) != 2 {
		t.Fatalf("joins %d, sync calls %v; want one join and one sync retry", client.joins, syncer.calls)
	}
}

func TestApprovedPendingJoinReportsSyncFailureAndCanRetry(t *testing.T) {
	svc, fake, syncer, _ := newService(t)
	fake.SeedInvite("pending-sync", tgclient.InviteInfo{RequestNeeded: true, Title: "Pending"})
	if _, err := svc.JoinSharedDrive(t.Context(), "https://t.me/+pending-sync"); err != nil {
		t.Fatal(err)
	}
	fake.SeedInvite("pending-sync", tgclient.InviteInfo{AlreadyJoined: true, Title: "Approved", ChannelID: 22222, AccessHash: 33333})
	failure := errors.New("history temporarily unavailable")
	syncer.err = failure
	result, err := svc.CheckPendingJoin(t.Context(), "pending-sync")
	if !errors.Is(err, failure) || result.Channel == nil || result.Status != JoinStatusJoined {
		t.Fatalf("check result %+v, error %v; want preserved joined identity and sync error", result, err)
	}
	if _, err := projection.GetChannel(svc.DB, 22222); err != nil {
		t.Fatalf("joined channel disappeared after sync error: %v", err)
	}
	if _, err := projection.GetPendingJoin(svc.DB, "pending-sync"); err != nil {
		t.Fatalf("retry handle disappeared after sync error: %v", err)
	}
	syncer.err = nil
	result, err = svc.CheckPendingJoin(t.Context(), "pending-sync")
	if err != nil || result.Channel == nil || result.Channel.ChannelID != 22222 {
		t.Fatalf("retry result %+v, error %v", result, err)
	}
	if _, err := projection.GetPendingJoin(svc.DB, "pending-sync"); err == nil {
		t.Fatal("pending handle still exists after successful sync")
	}
	if joins := fake.RequestedJoins(); len(joins) != 1 {
		t.Fatalf("join requests = %v; retry repeated membership request", joins)
	}
}

// This stub uses the same persisted-empty precondition as sync.Engine; the
// incremental path exposes its consequence by projecting one additional folder.
type populatedDriveSyncer struct {
	db               *sql.DB
	initialCalls     []int64
	incrementalCalls []int64
	incrementalErr   error
}

func (s *populatedDriveSyncer) InitialSyncEmptyChannel(_ context.Context, channelID int64) error {
	s.initialCalls = append(s.initialCalls, channelID)
	empty, err := projection.ChannelIsEmpty(s.db, channelID)
	if err != nil {
		return err
	}
	if !empty {
		return fmt.Errorf("initial scan: %w", projection.ErrChannelNotEmpty)
	}
	return nil
}

func (s *populatedDriveSyncer) Incremental(_ context.Context, channelID int64) error {
	s.incrementalCalls = append(s.incrementalCalls, channelID)
	if s.incrementalErr != nil {
		return s.incrementalErr
	}
	_, err := projection.ProjectFromOp(s.db, channelID, 12, projection.Op{Type: projection.OpMkdir, Obj: "d:new", Parent: projection.RootParent, Name: "New"}, 77, "incremental mkdir")
	return err
}

func TestJoinPopulatedSharedDriveUsesIncrementalAndPreservesStateOnFailure(t *testing.T) {
	for _, fail := range []bool{false, true} {
		name := "sync existing files"
		if fail {
			name = "retain files after incremental failure"
		}
		t.Run(name, func(t *testing.T) {
			svc, fake, _, _ := newService(t)
			peer := tgclient.InputPeer{ChannelID: 22222, AccessHash: 33333}
			if err := projection.InsertChannel(svc.DB, projection.Channel{ChannelID: peer.ChannelID, AccessHash: peer.AccessHash, Kind: projection.KindShared}); err != nil {
				t.Fatal(err)
			}
			if _, err := projection.ProjectFromOp(svc.DB, peer.ChannelID, 11, projection.Op{Type: projection.OpMkdir, Obj: "d:existing", Parent: projection.RootParent, Name: "Existing"}, 77, "existing mkdir"); err != nil {
				t.Fatal(err)
			}
			fake.SeedInvite("populated", tgclient.InviteInfo{AlreadyJoined: true, ChannelID: peer.ChannelID, AccessHash: peer.AccessHash})
			client := &joinRecordingClient{Fake: fake}
			svc.TG = client
			syncer := &populatedDriveSyncer{db: svc.DB}
			failure := errors.New("incremental history unavailable")
			if fail {
				syncer.incrementalErr = failure
			}
			svc.Sync = syncer
			result, err := svc.JoinSharedDrive(t.Context(), "https://t.me/+populated")
			if fail {
				if !errors.Is(err, failure) || !strings.Contains(err.Error(), "Retry this invite") {
					t.Fatalf("error %v, want actionable incremental failure", err)
				}
			} else if err != nil {
				t.Fatalf("populated join: %v", err)
			}
			if result.Status != JoinStatusJoined || result.Channel == nil || result.Channel.ChannelID != peer.ChannelID {
				t.Fatalf("result %+v, want saved joined identity", result)
			}
			if len(syncer.initialCalls) != 1 || len(syncer.incrementalCalls) != 1 || syncer.incrementalCalls[0] != peer.ChannelID {
				t.Fatalf("initial %v, incremental %v; want one incremental fallback", syncer.initialCalls, syncer.incrementalCalls)
			}
			if client.joins != 0 || len(fake.LeftChannels()) != 0 {
				t.Fatalf("join calls %d, leave calls %v; existing membership changed", client.joins, fake.LeftChannels())
			}
			if !projection.FolderExists(svc.DB, peer.ChannelID, "d:existing") {
				t.Fatal("existing projection removed")
			}
			if got := projection.FolderExists(svc.DB, peer.ChannelID, "d:new"); got == fail {
				t.Fatalf("incremental folder exists %t, failure injected %t", got, fail)
			}
			if _, err := projection.GetChannel(svc.DB, peer.ChannelID); err != nil {
				t.Fatalf("saved channel removed: %v", err)
			}
		})
	}
}
