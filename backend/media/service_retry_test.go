package media

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"TDrive/backend/projection"
	"TDrive/backend/tgclient"
)

type resolvingRangeFake struct {
	tgclient.RangeClient
	calls    int
	failures int
	err      error
}

func (f *resolvingRangeFake) ResolveDocument(ctx context.Context, peer tgclient.InputPeer, msgID int64) (tgclient.DocumentRef, error) {
	f.calls++
	if f.calls <= f.failures {
		return tgclient.DocumentRef{}, f.err
	}
	return f.RangeClient.ResolveDocument(ctx, peer, msgID)
}

func TestMediaOpenResolveFloodWait(t *testing.T) {
	permanent := errors.New("document unavailable")
	for _, tc := range []struct {
		name       string
		failures   int
		failure    error
		cancel     bool
		wantCalls  int
		wantSleeps int
		wantErr    error
	}{
		{name: "25 seconds then success", failures: 1, failure: fmt.Errorf("rpc: %w", tgclient.NewFloodWaitError(25*time.Second)), wantCalls: 2, wantSleeps: 1},
		{name: "retry count bounded", failures: 10, failure: tgclient.NewFloodWaitError(time.Second), wantCalls: 3, wantSleeps: 2, wantErr: tgclient.ErrFloodWait},
		{name: "long wait never shortened", failures: 1, failure: tgclient.NewFloodWaitError(time.Hour), wantCalls: 1, wantErr: tgclient.ErrFloodWait},
		{name: "permanent error", failures: 1, failure: permanent, wantCalls: 1, wantErr: permanent},
		{name: "cancel during wait", failures: 1, failure: tgclient.NewFloodWaitError(25 * time.Second), cancel: true, wantCalls: 1, wantSleeps: 1, wantErr: context.Canceled},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := newResolverTestDB(t)
			mustApplyOp(t, db, 1895, projection.Op{Type: projection.OpFileUpload, Parent: projection.RootParent, Name: "movie.mkv", FileSize: 64})
			base := newMediaRangeFake(map[int64][]byte{1895: testBytes(64)})
			ranges := &resolvingRangeFake{RangeClient: base, failures: tc.failures, err: tc.failure}
			svc := NewService(Config{DB: db, Peers: staticPeerResolver{peer: base.peer}, Ranges: ranges})
			defer svc.Close()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			sleeps := 0
			svc.resolveRetry.Sleep = func(sleepCtx context.Context, wait time.Duration) error {
				sleeps++
				want, _ := tgclient.FloodWaitDuration(tc.failure)
				if wait != want {
					t.Fatalf("wait = %v, want full server duration %v", wait, want)
				}
				if tc.cancel {
					cancel()
				}
				return sleepCtx.Err()
			}
			opened, err := svc.Open(ctx, testChannelID, 1895)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("Open error = %v, want %v", err, tc.wantErr)
			}
			if ranges.calls != tc.wantCalls || sleeps != tc.wantSleeps {
				t.Fatalf("calls/sleeps = %d/%d, want %d/%d", ranges.calls, sleeps, tc.wantCalls, tc.wantSleeps)
			}
			if tc.wantErr == nil && opened.Token == "" {
				t.Fatal("successful retry did not publish session")
			}
			if tc.wantErr != nil && len(svc.server.sessions) != 0 {
				t.Fatal("failed open published a session")
			}
		})
	}
}

type resolvingPeerFake struct {
	peer  tgclient.InputPeer
	calls int
}

func (f *resolvingPeerFake) ResolvePeer(ctx context.Context, channelID int64) (tgclient.InputPeer, error) {
	f.calls++
	if f.calls == 1 {
		return tgclient.InputPeer{}, tgclient.NewFloodWaitError(25 * time.Second)
	}
	return f.peer, nil
}

func TestMediaOpenRetriesPeerFloodWait(t *testing.T) {
	db := newResolverTestDB(t)
	mustApplyOp(t, db, 1895, projection.Op{Type: projection.OpFileUpload, Parent: projection.RootParent, Name: "movie.mkv", FileSize: 64})
	ranges := newMediaRangeFake(map[int64][]byte{1895: testBytes(64)})
	peers := &resolvingPeerFake{peer: ranges.peer}
	svc := NewService(Config{DB: db, Peers: peers, Ranges: ranges})
	defer svc.Close()
	sleeps := 0
	svc.resolveRetry.Sleep = func(ctx context.Context, wait time.Duration) error {
		sleeps++
		if wait != 25*time.Second {
			t.Fatalf("wait = %v, want 25s", wait)
		}
		return ctx.Err()
	}
	opened, err := svc.OpenStream(context.Background(), testChannelID, 1895)
	if err != nil {
		t.Fatal(err)
	}
	if opened.Token == "" || peers.calls != 2 || sleeps != 1 {
		t.Fatalf("open = %+v, calls = %d, sleeps = %d", opened, peers.calls, sleeps)
	}
}
