package main

import (
	"context"
	"database/sql"
	"sync/atomic"
	"testing"
	"time"

	"TDrive/backend/projection"
	fileservice "TDrive/backend/services/file"
	"TDrive/backend/tgclient"

	_ "modernc.org/sqlite"
)

const trashSweepTestChannelID int64 = 515151

type trashSweepPeerResolver struct {
	peer tgclient.InputPeer
}

func (r trashSweepPeerResolver) ResolvePeer(context.Context, int64) (tgclient.InputPeer, error) {
	return r.peer, nil
}

// newTrashSweepFixture builds the real file service against an in-memory
// projection and a fake Telegram, with one file already in the trash. The
// scheduled sweep is only worth testing against the real purge path, because
// what is under test is whether that path can be made to destroy bytes early.
func newTrashSweepFixture(t *testing.T) (*fileservice.Service, *tgclient.Fake, int64) {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	if err := projection.MigratePersonalChannel(db, trashSweepTestChannelID); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	peer := tgclient.InputPeer{ChannelID: trashSweepTestChannelID, AccessHash: 42}
	fake := tgclient.NewFake(7)
	fake.SeedChannel(peer, "Personal")

	msgID := int64(2000)
	svc := &fileservice.Service{
		DB:    db,
		TG:    fake,
		Peers: trashSweepPeerResolver{peer: peer},
		EmitOp: func(channelID int64, op projection.Op) (int64, error) {
			msgID++
			_, err := projection.ProjectFromOp(db, channelID, msgID, op, 7, projection.Format(op))
			return msgID, err
		},
		ActorID: func(context.Context) (int64, error) { return 7, nil },
		Now:     func() time.Time { return time.Unix(5000, 0) },
	}

	seed := projection.Op{
		Type: projection.OpFileCommit, ProtocolVersion: 1, OpID: "put-doomed",
		Name: "doomed.txt", ContentMsgID: 9500, FileSize: 8,
	}
	if _, err := projection.ProjectFromOp(db, trashSweepTestChannelID, 1500, seed, 7, projection.Format(seed)); err != nil {
		t.Fatalf("seed file: %v", err)
	}
	if err := svc.Delete(context.Background(), trashSweepTestChannelID, 1500); err != nil {
		t.Fatalf("delete: %v", err)
	}
	listings, err := svc.ListTrash(trashSweepTestChannelID)
	if err != nil || len(listings) != 1 {
		t.Fatalf("trash listing = %+v, err %v", listings, err)
	}
	return svc, fake, listings[0].PurgeAfter
}

func trashEntryCount(t *testing.T, svc *fileservice.Service) int {
	t.Helper()
	listings, err := svc.ListTrash(trashSweepTestChannelID)
	if err != nil {
		t.Fatalf("ListTrash: %v", err)
	}
	return len(listings)
}

// TestScheduledSweepDestroysNothingBeforePurgeAfter drives the very loop the
// app runs. The clock is the only thing faked, so a regression that let the
// scheduled path skip the retention check would fail here even though the
// manual purge tests still passed.
func TestScheduledSweepDestroysNothingBeforePurgeAfter(t *testing.T) {
	svc, fake, purgeAfter := newTrashSweepFixture(t)

	var now atomic.Int64
	now.Store(purgeAfter - 1)
	passes := make(chan struct{})
	stop := make(chan struct{})
	startup := make(chan time.Time)
	tick := make(chan time.Time)
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		runTrashSweeps(stop, startup, tick, func() {
			if err := svc.PurgeExpiredTrash(context.Background(), trashSweepTestChannelID, now.Load()); err != nil {
				t.Errorf("sweep pass: %v", err)
			}
			passes <- struct{}{}
		})
	}()

	// The startup pass exists so a session shorter than the interval still
	// sweeps -- and it must respect the window just as the ticked pass does.
	startup <- time.Now()
	<-passes
	if batches := fake.DeletedBatches(); len(batches) != 0 {
		t.Fatalf("startup pass destroyed bodies one second early: %+v", batches)
	}
	if count := trashEntryCount(t, svc); count != 1 {
		t.Fatalf("trash entries = %d, want the unexpired one still restorable", count)
	}

	// Still early on the interval pass.
	tick <- time.Now()
	<-passes
	if batches := fake.DeletedBatches(); len(batches) != 0 {
		t.Fatalf("interval pass destroyed bodies one second early: %+v", batches)
	}

	now.Store(purgeAfter)
	tick <- time.Now()
	<-passes
	if count := trashEntryCount(t, svc); count != 0 {
		t.Fatalf("expired entry survived its own sweep: %d left", count)
	}
	deleted := false
	for _, batch := range fake.DeletedBatches() {
		for _, id := range batch {
			if id == 9500 {
				deleted = true
			}
			if id == 1500 {
				t.Fatalf("sweep deleted the control message, not just the body")
			}
		}
	}
	if !deleted {
		t.Fatalf("sweep did not delete the expired body")
	}

	close(stop)
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("sweep loop did not stop when its stop channel closed")
	}
}

// TestSweepLoopRunsEveryTickAndStopsOnlyOnStop covers the wedge case: whatever
// one pass does, the loop keeps taking the next tick and ends only when asked.
func TestSweepLoopRunsEveryTickAndStopsOnlyOnStop(t *testing.T) {
	stop := make(chan struct{})
	tick := make(chan time.Time)
	var passes atomic.Int32
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		runTrashSweeps(stop, nil, tick, func() { passes.Add(1) })
	}()

	for range 3 {
		tick <- time.Now()
	}
	close(stop)
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("sweep loop did not stop")
	}
	if got := passes.Load(); got != 3 {
		t.Fatalf("passes = %d, want 3", got)
	}
}
