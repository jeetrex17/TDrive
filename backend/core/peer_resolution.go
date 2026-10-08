package core

import (
	"context"
	"fmt"

	"TDrive/backend"
	"TDrive/backend/projection"
	"TDrive/backend/tgclient"
)

// RefreshPeer bypasses the cached access hash after Telegram rejects a peer.
// Persisting the refreshed hash lets subsequent reads and writes share it.
func (e *Engine) RefreshPeer(ctx context.Context, channelID int64) (tgclient.InputPeer, error) {
	if e == nil || e.tg == nil {
		return tgclient.InputPeer{}, fmt.Errorf("tg client not ready")
	}
	if channelID <= 0 {
		return tgclient.InputPeer{}, fmt.Errorf("invalid channel id %d", channelID)
	}
	if err := ctx.Err(); err != nil {
		return tgclient.InputPeer{}, err
	}
	peer, err := e.tg.ResolveDriveChannel(ctx, channelID)
	if err != nil {
		return tgclient.InputPeer{}, err
	}
	if peer.ChannelID != channelID || peer.AccessHash == 0 {
		return tgclient.InputPeer{}, fmt.Errorf("invalid resolved peer for channel %d", channelID)
	}
	if backend.DB != nil {
		if err := projection.UpdateAccessHash(backend.DB, channelID, peer.AccessHash); err != nil && e.warnf != nil {
			e.warnf("warn: could not refresh channel access hash %d: %v\n", channelID, err)
		}
	}
	return peer, nil
}
