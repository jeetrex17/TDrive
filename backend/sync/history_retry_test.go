package sync

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"testing/synctest"
	"time"

	"TDrive/backend/projection"
	"TDrive/backend/tgclient"
)

type failingHistoryClient struct {
	*tgclient.Fake
	calls int
	fail  func(context.Context, int) error
}

func (client *failingHistoryClient) GetHistory(ctx context.Context, peer tgclient.InputPeer, minID, offsetID int64, limit int) ([]tgclient.HistoryMessage, error) {
	client.calls++
	if err := client.fail(ctx, client.calls); err != nil {
		return nil, err
	}
	return client.Fake.GetHistory(ctx, peer, minID, offsetID, limit)
}

func TestHistoryReadTransportRetry(t *testing.T) {
	dropped := fmt.Errorf("rpcDoRequest: retryUntilAck: engine forcibly closed: %w", context.Canceled)
	rejected := errors.New("CHANNEL_PRIVATE")
	for _, test := range []struct {
		name      string
		failures  int
		err       error
		wantCalls int
		wantErr   error
	}{
		{name: "reconnect", failures: 1, err: dropped, wantCalls: 2},
		{name: "bounded outage", failures: 100, err: dropped, wantCalls: 5, wantErr: dropped},
		{name: "API rejection", failures: 1, err: rejected, wantCalls: 1, wantErr: rejected},
		{name: "bare cancellation", failures: 1, err: context.Canceled, wantCalls: 1, wantErr: context.Canceled},
	} {
		t.Run(test.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				client := &failingHistoryClient{Fake: tgclient.NewFake(7), fail: func(_ context.Context, call int) error {
					if call <= test.failures {
						return test.err
					}
					return nil
				}}
				engine := NewEngine(nil, client, fakePeers{})
				_, err := engine.getHistory(t.Context(), testChan, tgclient.InputPeer{ChannelID: testChan}, 0, 0, 100)
				if !errors.Is(err, test.wantErr) || client.calls != test.wantCalls {
					t.Fatalf("history error=%v calls=%d, want error=%v calls=%d", err, client.calls, test.wantErr, test.wantCalls)
				}
			})
		})
	}
}

func TestHistoryReadCancellationDuringTransportBackoff(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		client := &failingHistoryClient{Fake: tgclient.NewFake(7), fail: func(context.Context, int) error {
			return fmt.Errorf("engine forcibly closed: %w", context.Canceled)
		}}
		engine := NewEngine(nil, client, fakePeers{})
		go func() {
			time.Sleep(time.Second)
			cancel()
		}()
		_, err := engine.getHistory(ctx, testChan, tgclient.InputPeer{ChannelID: testChan}, 0, 0, 100)
		if !errors.Is(err, context.Canceled) || client.calls != 1 {
			t.Fatalf("history error=%v calls=%d, want cancellation after one call", err, client.calls)
		}
	})
}

func TestHistoryReadMixedFailuresKeepIndependentBudgets(t *testing.T) {
	dropped := fmt.Errorf("engine forcibly closed: %w", context.Canceled)
	flood := tgclient.NewFloodWaitError(0)
	for _, test := range []struct {
		name      string
		failures  []error
		wantErr   error
		wantCalls int
	}{
		{name: "flood budget", failures: []error{flood, dropped, flood, dropped, flood, dropped, flood, dropped, flood, flood}, wantErr: tgclient.ErrFloodWait, wantCalls: 10},
		{name: "transport budget", failures: []error{dropped, flood, dropped, flood, dropped, flood, dropped, flood, dropped}, wantErr: dropped, wantCalls: 9},
	} {
		t.Run(test.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				client := &failingHistoryClient{Fake: tgclient.NewFake(7), fail: func(_ context.Context, call int) error {
					if call <= len(test.failures) {
						return test.failures[call-1]
					}
					return nil
				}}
				engine := NewEngine(nil, client, fakePeers{})
				_, err := engine.getHistory(t.Context(), testChan, tgclient.InputPeer{ChannelID: testChan}, 0, 0, 100)
				if !errors.Is(err, test.wantErr) || client.calls != test.wantCalls {
					t.Fatalf("history error=%v calls=%d, want error=%v calls=%d", err, client.calls, test.wantErr, test.wantCalls)
				}
			})
		})
	}
}

func TestAuthoritativeHistoryRecoversDroppedConnection(t *testing.T) {
	db, telegram, _ := newSyncEnv(t)
	messageID := sendOp(t, telegram, projection.Op{Type: projection.OpMkdir, Obj: "d:recovered", Name: "Recovered"})
	client := &failingHistoryClient{Fake: telegram, fail: func(_ context.Context, call int) error {
		// Fail the apply pass after the counting pass read the populated and empty pages.
		if call == 3 {
			return fmt.Errorf("rpcDoRequest: retryUntilAck: engine forcibly closed: %w", context.Canceled)
		}
		return nil
	}}
	engine := NewEngine(db, client, fakePeers{})
	if err := engine.EnsureAuthoritative(t.Context(), testChan); err != nil {
		t.Fatalf("recover history: %v", err)
	}
	if !projection.FolderExists(db, testChan, "d:recovered") {
		t.Fatal("recovered folder missing")
	}
	channel, err := projection.GetChannel(db, testChan)
	if err != nil {
		t.Fatal(err)
	}
	if !channel.InitialSyncDone || channel.LastSyncedMsg != messageID {
		t.Fatalf("recovered authority=%v watermark=%d, want true/%d", channel.InitialSyncDone, channel.LastSyncedMsg, messageID)
	}
}
