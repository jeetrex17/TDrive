package core

import (
	"context"
	"errors"
	"testing"

	"TDrive/backend"
	"TDrive/backend/projection"
	"TDrive/backend/tgclient"
	"github.com/gotd/td/tgerr"
)

type channelResolutionClient struct {
	*tgclient.Fake
	resolved    tgclient.InputPeer
	resolveErr  error
	resolutions int
	sends       []tgclient.InputPeer
	randomIDs   []int64
}

func (c *channelResolutionClient) ResolveDriveChannel(context.Context, int64) (tgclient.InputPeer, error) {
	c.resolutions++
	return c.resolved, c.resolveErr
}

func (c *channelResolutionClient) SendControlWithRandomID(_ context.Context, peer tgclient.InputPeer, _ string, _ bool, randomID int64) (int64, error) {
	c.sends = append(c.sends, peer)
	c.randomIDs = append(c.randomIDs, randomID)
	if peer.AccessHash != c.resolved.AccessHash {
		return 0, tgerr.New(400, "CHANNEL_INVALID")
	}
	return 42, nil
}

func TestChannelPeerUsesPersistedHashForRequestedDrive(t *testing.T) {
	db := openEngineWriteTestDB(t)
	previousDB := backend.DB
	backend.DB = db
	t.Cleanup(func() { backend.DB = previousDB })
	if err := projection.UpdateAccessHash(db, engineWriteTestChannelID, 71); err != nil {
		t.Fatal(err)
	}
	sharedID := engineWriteTestChannelID + 1
	if err := projection.InsertChannel(db, projection.Channel{ChannelID: sharedID, AccessHash: 91, Kind: projection.KindShared}); err != nil {
		t.Fatal(err)
	}
	client := &channelResolutionClient{Fake: tgclient.NewFake(7), resolveErr: errors.New("remote unavailable")}
	engine := &Engine{tg: client}
	engine.SetActiveChannelID(engineWriteTestChannelID)
	peer, err := engine.ChannelPeer(t.Context(), sharedID)
	if err != nil || peer.ChannelID != sharedID || peer.AccessHash != 91 {
		t.Fatalf("shared peer = %+v, err %v; want persisted shared hash 91", peer, err)
	}
	if client.resolutions != 0 {
		t.Fatalf("remote resolutions = %d, want 0", client.resolutions)
	}
}

func TestChannelPeerCachesFreshHash(t *testing.T) {
	db := openEngineWriteTestDB(t)
	previousDB := backend.DB
	backend.DB = db
	t.Cleanup(func() { backend.DB = previousDB })
	fresh := tgclient.InputPeer{ChannelID: engineWriteTestChannelID, AccessHash: 92}
	client := &channelResolutionClient{Fake: tgclient.NewFake(7), resolved: fresh}
	engine := &Engine{tg: client, warnf: func(string, ...any) {}}
	for range 2 {
		peer, err := engine.ChannelPeer(t.Context(), engineWriteTestChannelID)
		if err != nil || peer != fresh {
			t.Fatalf("peer = %+v, err %v, want %+v", peer, err, fresh)
		}
	}
	if client.resolutions != 1 {
		t.Fatalf("remote resolutions = %d, want one cache miss", client.resolutions)
	}
	channel, err := projection.GetChannel(db, engineWriteTestChannelID)
	if err != nil || channel.AccessHash != 92 {
		t.Fatalf("cached channel = %+v, err %v, want hash 92", channel, err)
	}
}

func TestStaleControlPeerRefreshBypassesCacheAndKeepsSendID(t *testing.T) {
	db := openEngineWriteTestDB(t)
	previousDB := backend.DB
	backend.DB = db
	t.Cleanup(func() { backend.DB = previousDB })
	if err := projection.UpdateAccessHash(db, engineWriteTestChannelID, 91); err != nil {
		t.Fatal(err)
	}
	fresh := tgclient.InputPeer{ChannelID: engineWriteTestChannelID, AccessHash: 92}
	client := &channelResolutionClient{Fake: tgclient.NewFake(7), resolved: fresh}
	engine := &Engine{tg: client, warnf: func(string, ...any) {}}
	id, err := engine.sendControlContext(t.Context(), engineWriteTestChannelID, "control")
	if err != nil || id != 42 {
		t.Fatalf("send = %d, %v; want successful refresh", id, err)
	}
	if client.resolutions != 1 || len(client.sends) != 2 || client.sends[0].AccessHash != 91 || client.sends[1] != fresh {
		t.Fatalf("resolutions %d, peers %+v; want cached then fresh", client.resolutions, client.sends)
	}
	if client.randomIDs[0] <= 0 || client.randomIDs[0] != client.randomIDs[1] {
		t.Fatalf("random IDs = %v, want same positive ID", client.randomIDs)
	}
	channel, err := projection.GetChannel(db, engineWriteTestChannelID)
	if err != nil || channel.AccessHash != 92 {
		t.Fatalf("cached channel = %+v, err %v, want hash 92", channel, err)
	}
}

func TestRefreshPeerRejectsInvalidRemoteIdentityWithoutPoisoningCache(t *testing.T) {
	db := openEngineWriteTestDB(t)
	previousDB := backend.DB
	backend.DB = db
	t.Cleanup(func() { backend.DB = previousDB })
	if err := projection.UpdateAccessHash(db, engineWriteTestChannelID, 91); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name string
		peer tgclient.InputPeer
	}{
		{"wrong channel", tgclient.InputPeer{ChannelID: engineWriteTestChannelID + 1, AccessHash: 92}},
		{"missing hash", tgclient.InputPeer{ChannelID: engineWriteTestChannelID}},
	} {
		t.Run(test.name, func(t *testing.T) {
			client := &channelResolutionClient{Fake: tgclient.NewFake(7), resolved: test.peer}
			engine := &Engine{tg: client}
			if _, err := engine.RefreshPeer(t.Context(), engineWriteTestChannelID); err == nil {
				t.Fatal("invalid peer accepted")
			}
			channel, err := projection.GetChannel(db, engineWriteTestChannelID)
			if err != nil || channel.AccessHash != 91 {
				t.Fatalf("cached channel %+v, err %v; want original hash 91", channel, err)
			}
		})
	}
}

func TestChannelPeerHonorsCanceledContextBeforeCachedLookup(t *testing.T) {
	client := &channelResolutionClient{Fake: tgclient.NewFake(7)}
	engine := &Engine{tg: client}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := engine.ChannelPeer(ctx, engineWriteTestChannelID); !errors.Is(err, context.Canceled) {
		t.Fatalf("error %v, want canceled", err)
	}
	if client.resolutions != 0 {
		t.Fatalf("canceled operation resolved %d remote peers", client.resolutions)
	}
}
