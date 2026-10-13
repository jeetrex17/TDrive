package file

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"testing"

	"TDrive/backend/projection"
	"TDrive/backend/tgclient"
)

func TestOrphanSweepPreservesInFlightAndRestorableBodies(t *testing.T) {
	svc, db, fake, actor := newTestService(t)
	for i, uuid := range []string{"deleted", "in-flight", "trashed"} {
		id := int64(i + 1)
		project(t, db, personalChannelID, id, *actor, projection.Op{
			Type: projection.OpFilePart, UploadUUID: uuid, PartIndex: 0, FileSize: 4,
		})
		fake.SeedHistory(tgclient.HistoryMessage{ChannelID: personalChannelID, MsgID: id, HasMedia: true, MediaSize: 4})
		fake.SeedDocumentBody(id, []byte("body"))
	}
	for i, uuid := range []string{"deleted", "trashed"} {
		project(t, db, personalChannelID, int64(10+i), *actor, projection.Op{
			Type: projection.OpFileManifest, UploadUUID: uuid, Name: uuid + ".bin",
			Parent: projection.RootParent, FileSize: 4, PartCount: 1,
		})
	}
	project(t, db, personalChannelID, 20, *actor, projection.Op{Type: projection.OpTomb, Obj: "f:10"})
	if err := svc.Delete(t.Context(), personalChannelID, 11); err != nil {
		t.Fatal(err)
	}
	fake.SeedHistory(tgclient.HistoryMessage{ChannelID: personalChannelID, MsgID: 4, HasMedia: true})
	if err := projection.QueuePartCleanup(db, personalChannelID, []int64{4}); err != nil {
		t.Fatal(err)
	}
	if err := projection.QueuePartCleanup(db, sharedChannelID, []int64{99}); err != nil {
		t.Fatal(err)
	}
	if err := svc.SweepOrphanParts(t.Context(), personalChannelID); err != nil {
		t.Fatal(err)
	}
	if batches := fake.DeletedBatches(); len(batches) != 2 || !slices.Equal(batches[0], []int64{1}) || !slices.Equal(batches[1], []int64{4}) {
		t.Fatalf("deleted batches = %v, want only dead and explicitly queued bodies", batches)
	}
	for _, uuid := range []string{"in-flight", "trashed"} {
		parts, err := projection.PartsForUUID(db, personalChannelID, uuid)
		if err != nil || len(parts) != 1 {
			t.Fatalf("protected %s parts = %v, err %v", uuid, parts, err)
		}
		missing, err := fake.MissingMessages(t.Context(), tgclient.InputPeer{ChannelID: personalChannelID}, []int64{parts[0].MsgID})
		if err != nil || len(missing) != 0 {
			t.Fatalf("protected %s remote body missing: %v, err %v", uuid, missing, err)
		}
	}
	parts, err := projection.PartsForUUID(db, personalChannelID, "deleted")
	if err != nil || len(parts) != 0 {
		t.Fatalf("deleted pointers = %v, err %v", parts, err)
	}
	queued, err := projection.PendingPartCleanup(db, personalChannelID)
	if err != nil || len(queued) != 0 {
		t.Fatalf("completed queue = %v, err %v", queued, err)
	}
	other, err := projection.PendingPartCleanup(db, sharedChannelID)
	if err != nil || !slices.Equal(other, []int64{99}) {
		t.Fatalf("other channel queue = %v, err %v", other, err)
	}
	if err := svc.SweepOrphanParts(t.Context(), personalChannelID); err != nil || len(fake.DeletedBatches()) != 2 {
		t.Fatalf("repeated sweep performed additional cleanup: %v", err)
	}
}

type failingSweepClient struct {
	*tgclient.Fake
	failID int64
	err    error
}

type unavailableSweepPeer struct{ err error }

func (r unavailableSweepPeer) ResolvePeer(context.Context, int64) (tgclient.InputPeer, error) {
	return tgclient.InputPeer{}, r.err
}

func TestOrphanSweepKeepsQueueWhenDriveCannotBeResolved(t *testing.T) {
	svc, db, fake, _ := newTestService(t)
	if err := projection.QueuePartCleanup(db, personalChannelID, []int64{55}); err != nil {
		t.Fatal(err)
	}
	failure := errors.New("drive unavailable")
	svc.Peers = unavailableSweepPeer{err: failure}
	if err := svc.SweepOrphanParts(t.Context(), personalChannelID); !errors.Is(err, failure) {
		t.Fatalf("sweep error = %v, want drive resolution failure", err)
	}
	queued, err := projection.PendingPartCleanup(db, personalChannelID)
	if err != nil || !slices.Equal(queued, []int64{55}) || len(fake.DeletedBatches()) != 0 {
		t.Fatalf("resolution failure lost cleanup ownership: queue %v, err %v, deletes %v", queued, err, fake.DeletedBatches())
	}
}

func (f *failingSweepClient) DeleteMessages(ctx context.Context, peer tgclient.InputPeer, ids []int64) error {
	if slices.Contains(ids, f.failID) {
		return f.err
	}
	return f.Fake.DeleteMessages(ctx, peer, ids)
}

func TestOrphanSweepRetainsCleanupOwnershipUntilRemoteDeletion(t *testing.T) {
	for _, failID := range []int64{1, 2} {
		t.Run(fmt.Sprintf("delete body %d fails", failID), func(t *testing.T) {
			svc, db, fake, actor := newTestService(t)
			project(t, db, personalChannelID, 1, *actor, projection.Op{Type: projection.OpFilePart, UploadUUID: "dead", PartIndex: 0, FileSize: 4})
			project(t, db, personalChannelID, 3, *actor, projection.Op{Type: projection.OpFileManifest, UploadUUID: "dead", Name: "dead.bin", FileSize: 4, PartCount: 1})
			project(t, db, personalChannelID, 4, *actor, projection.Op{Type: projection.OpTomb, Obj: "f:3"})
			if err := projection.QueuePartCleanup(db, personalChannelID, []int64{2}); err != nil {
				t.Fatal(err)
			}
			fake.SeedHistory(tgclient.HistoryMessage{ChannelID: personalChannelID, MsgID: 1}, tgclient.HistoryMessage{ChannelID: personalChannelID, MsgID: 2})
			failure := errors.New("delete denied")
			svc.TG = &failingSweepClient{Fake: fake, failID: failID, err: failure}
			if err := svc.SweepOrphanParts(t.Context(), personalChannelID); !errors.Is(err, failure) {
				t.Fatalf("sweep error = %v, want deletion failure", err)
			}
			parts, err := projection.PartsForUUID(db, personalChannelID, "dead")
			wantParts := 0
			if failID == 1 {
				wantParts = 1
			}
			if err != nil || len(parts) != wantParts {
				t.Fatalf("retained pointers = %v, err %v; want %d", parts, err, wantParts)
			}
			queued, err := projection.PendingPartCleanup(db, personalChannelID)
			if err != nil || !slices.Equal(queued, []int64{2}) {
				t.Fatalf("retained queue = %v, err %v", queued, err)
			}
			missing, err := fake.MissingMessages(t.Context(), tgclient.InputPeer{ChannelID: personalChannelID}, []int64{failID})
			if err != nil || len(missing) != 0 {
				t.Fatalf("failed delete removed remote body: %v, err %v", missing, err)
			}
			svc.TG = fake
			if err := svc.SweepOrphanParts(t.Context(), personalChannelID); err != nil {
				t.Fatal(err)
			}
			parts, err = projection.PartsForUUID(db, personalChannelID, "dead")
			if err != nil || len(parts) != 0 {
				t.Fatalf("retry pointers = %v, err %v", parts, err)
			}
			queued, err = projection.PendingPartCleanup(db, personalChannelID)
			if err != nil || len(queued) != 0 {
				t.Fatalf("retry queue = %v, err %v", queued, err)
			}
			missing, err = fake.MissingMessages(t.Context(), tgclient.InputPeer{ChannelID: personalChannelID}, []int64{1, 2})
			if err != nil || len(missing) != 2 {
				t.Fatalf("remote bodies after retry missing = %v, err %v", missing, err)
			}
		})
	}
}

func TestStagedUploadSourceRetainedAcrossAccountsUntilEveryJobCompletes(t *testing.T) {
	svc, db, _, _ := newTestService(t)
	svc.CacheNamespace = "account-one"
	path := writeTempFile(t, "eight123")
	plan := uploadPartPlan{partSize: 4, partCount: 2}
	first, err := svc.createUploadJob(t.Context(), path, "first.bin", projection.RootParent, 8, personalChannelID, 7, plan, tgclient.InputPeer{ChannelID: personalChannelID})
	if err != nil {
		t.Fatal(err)
	}
	other := &Service{DB: db, TG: svc.TG, CacheNamespace: "account-two"}
	second, err := other.createUploadJob(t.Context(), path, "second.bin", projection.RootParent, 8, sharedChannelID, 8, plan, tgclient.InputPeer{ChannelID: sharedChannelID})
	if err != nil {
		t.Fatal(err)
	}
	if source, err := svc.UploadJobSource(t.Context(), personalChannelID, first.ID); err != nil || source != path {
		t.Fatalf("owned source = %q, err %v", source, err)
	}
	for _, channel := range []int64{0, sharedChannelID} {
		if source, err := svc.UploadJobSource(t.Context(), channel, first.ID); err == nil || source != "" {
			t.Fatalf("channel %d disclosed source %q, err %v", channel, source, err)
		}
	}
	if source, err := other.UploadJobSource(t.Context(), personalChannelID, first.ID); err == nil || source != "" {
		t.Fatalf("other account disclosed source %q, err %v", source, err)
	}
	if err := svc.setUploadJobStatus(t.Context(), first.ID, resumeComplete, ""); err != nil {
		t.Fatal(err)
	}
	for _, status := range []string{resumePaused, resumeNeedsFile, resumeUncertain, resumeManifestUncertain, resumeCanceling, resumeWaitingNetwork} {
		if err := other.setUploadJobStatus(t.Context(), second.ID, status, ""); err != nil {
			t.Fatal(err)
		}
		if retained, err := svc.StagedSourceReferenced(t.Context(), path); err != nil || !retained {
			t.Fatalf("other-account %s job lost source retention: %t, err %v", status, retained, err)
		}
	}
	if err := other.setUploadJobStatus(t.Context(), second.ID, resumeComplete, ""); err != nil {
		t.Fatal(err)
	}
	if retained, err := svc.StagedSourceReferenced(t.Context(), path); err != nil || retained {
		t.Fatalf("completed source retained = %t, err %v", retained, err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := svc.StagedSourceReferenced(ctx, path); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled source lookup must not authorize removal: %v", err)
	}
}
