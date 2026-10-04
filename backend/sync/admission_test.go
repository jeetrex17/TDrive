package sync

import (
	"context"
	"errors"
	stdsync "sync"
	"sync/atomic"
	"testing"
	"time"

	"TDrive/backend/projection"
	"TDrive/backend/tgclient"
)

// Keep a real history scan in flight: cancellation must release a queued
// caller without interrupting the scan or opening a second same-channel scan.
type blockedHistoryClient struct {
	tgclient.Client
	entered chan struct{}
	release chan struct{}
	once    stdsync.Once
	calls   atomic.Int64
}

func (c *blockedHistoryClient) GetHistory(ctx context.Context, peer tgclient.InputPeer, minID, offsetID int64, limit int) ([]tgclient.HistoryMessage, error) {
	if peer.ChannelID == testChan {
		c.calls.Add(1)
		c.once.Do(func() { close(c.entered) })
		select {
		case <-c.release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return c.Client.GetHistory(ctx, peer, minID, offsetID, limit)
}

func TestChannelAdmissionHonorsCancellation(t *testing.T) {
	for _, name := range []string{"incremental", "hard delete", "authority", "initial", "deletions"} {
		t.Run(name, func(t *testing.T) {
			db, telegram, engine := newSyncEnv(t)
			db.SetMaxOpenConns(1)
			client := &blockedHistoryClient{Client: telegram, entered: make(chan struct{}), release: make(chan struct{})}
			engine.tg = client
			engine.EmitTomb = func(int64, int64) error { return nil }
			release := stdsync.OnceFunc(func() { close(client.release) })
			holder := make(chan struct{})
			var holderErr error
			go func() {
				holderErr = engine.Incremental(t.Context(), testChan)
				close(holder)
			}()
			t.Cleanup(func() {
				release()
				select {
				case <-holder:
				case <-time.After(5 * time.Second):
					t.Error("holding scan did not finish")
				}
			})
			select {
			case <-client.entered:
			case <-time.After(5 * time.Second):
				t.Fatal("holding scan did not reach Telegram")
			}

			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			result := make(chan error, 1)
			started := make(chan struct{})
			go func() {
				close(started)
				var err error
				switch name {
				case "incremental":
					err = engine.Incremental(ctx, testChan)
				case "hard delete":
					err = engine.PrepareHardDeleteProjection(ctx, testChan)
				case "authority":
					err = engine.EnsureAuthoritative(ctx, testChan)
				case "initial":
					err = engine.InitialSyncEmptyChannel(ctx, testChan)
				case "deletions":
					_, err = engine.ReconcileDeletions(ctx, testChan)
				}
				result <- err
			}()
			<-started
			cancel()
			select {
			case err := <-result:
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("waiting operation: %v, want context cancellation", err)
				}
			case <-time.After(time.Second):
				// Release the holder and join the waiter even on the regression path.
				release()
				select {
				case <-result:
				case <-time.After(5 * time.Second):
					t.Fatal("waiting operation did not finish after releasing the scan")
				}
				t.Fatal("canceled operation waited for the holding scan")
			}
			if calls := client.calls.Load(); calls != 1 {
				t.Fatalf("same-channel history reads = %d, want only the holding scan", calls)
			}

			// A different channel and unrelated SQL must still be usable while
			// the first channel remains blocked in network I/O.
			const otherChannel = testChan + 1
			if err := projection.MigratePersonalChannel(db, otherChannel); err != nil {
				t.Fatal(err)
			}
			otherCtx, stop := context.WithTimeout(t.Context(), 5*time.Second)
			defer stop()
			if err := engine.Incremental(otherCtx, otherChannel); err != nil {
				t.Fatalf("independent channel: %v", err)
			}
			release()
			select {
			case <-holder:
				if holderErr != nil {
					t.Fatalf("canceling the waiter affected the holder: %v", holderErr)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("holding scan did not finish")
			}
			if err := engine.Incremental(otherCtx, testChan); err != nil {
				t.Fatalf("channel reuse after cancellation: %v", err)
			}
		})
	}
}

func TestFloodWaitCancellationReleasesChannel(t *testing.T) {
	db, telegram, engine := newSyncEnv(t)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	engine.OnProgress = func(progress Progress) {
		if progress.Phase == ProgressWaiting {
			cancel()
		}
	}
	telegram.InjectReadFloodWaits(1)
	sendOp(t, telegram, projection.Op{Type: projection.OpMkdir, Obj: "d:after-cancel", Name: "After cancel"})
	if err := engine.Incremental(ctx, testChan); !errors.Is(err, context.Canceled) {
		t.Fatalf("flood-wait cancellation: %v", err)
	}
	channel, err := projection.GetChannel(db, testChan)
	if err != nil {
		t.Fatal(err)
	}
	if channel.LastSyncedMsg != 0 {
		t.Fatalf("canceled scan advanced watermark to %d", channel.LastSyncedMsg)
	}
	if err := engine.Incremental(t.Context(), testChan); err != nil {
		t.Fatalf("scan after cancellation: %v", err)
	}
	if !projection.FolderExists(db, testChan, "d:after-cancel") {
		t.Fatal("retry did not project the pending operation")
	}
}
