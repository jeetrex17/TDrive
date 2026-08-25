package mountcontent

import (
	"context"

	"TDrive/backend/mountcache"
	"TDrive/backend/tgclient"
)

// Mounts normally pin one channel. The small bound protects callers that use
// Opener directly with multiple drives without retaining peers indefinitely.
const maxCachedPeers = 8

// resolvePeer coalesces concurrent lookups while keeping each caller's
// cancellation independent. The resolution itself belongs to the opener and
// is canceled when its final waiter leaves or the opener closes.
func (o *Opener) resolvePeer(ctx context.Context, channelID int64) (tgclient.InputPeer, error) {
	if ctx == nil {
		return tgclient.InputPeer{}, ErrNilContext
	}
	if err := ctx.Err(); err != nil {
		return tgclient.InputPeer{}, err
	}
	if err := o.ensureOpen(); err != nil {
		return tgclient.InputPeer{}, err
	}

	peer, err := o.peerResolutions.Load(
		ctx,
		o.lifetime,
		channelID,
		func() (tgclient.InputPeer, bool) {
			return o.peerCache.get(channelID)
		},
		func(loadContext context.Context) (tgclient.InputPeer, error) {
			return o.peers.ResolvePeer(loadContext, channelID)
		},
		func(resolved tgclient.InputPeer) {
			o.peerCache.put(channelID, resolved)
		},
	)
	if openErr := o.ensureOpen(); openErr != nil {
		return tgclient.InputPeer{}, openErr
	}
	return peer, err
}

// resolvedPeerCache returns value-only Telegram peers so callers cannot mutate
// cache state after a lookup.
type resolvedPeerCache struct {
	entries *mountcache.LRU[int64, tgclient.InputPeer]
}

func newResolvedPeerCache(capacity int) *resolvedPeerCache {
	return &resolvedPeerCache{
		entries: mountcache.NewLRU[int64, tgclient.InputPeer](
			mountcache.LRUConfig[tgclient.InputPeer]{Capacity: capacity},
		),
	}
}

func (cache *resolvedPeerCache) get(channelID int64) (tgclient.InputPeer, bool) {
	if cache == nil {
		return tgclient.InputPeer{}, false
	}
	return cache.entries.Get(channelID)
}

func (cache *resolvedPeerCache) put(channelID int64, peer tgclient.InputPeer) {
	if cache == nil {
		return
	}
	cache.entries.Put(channelID, peer)
}

func (cache *resolvedPeerCache) clear() {
	if cache == nil {
		return
	}
	cache.entries.Clear()
}

func (cache *resolvedPeerCache) len() int {
	if cache == nil {
		return 0
	}
	return cache.entries.Len()
}
