package mountcontent

import (
	"container/list"
	"context"

	"TDrive/backend/tgclient"
)

// Mounts normally pin one channel. The small bound protects callers that use
// Opener directly with multiple drives without retaining peers indefinitely.
const maxCachedPeers = 8

type peerResolution struct {
	ctx       context.Context
	cancel    context.CancelFunc
	done      chan struct{}
	waiters   int
	peer      tgclient.InputPeer
	err       error
	completed bool
	abandoned bool
}

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

	o.mu.Lock()
	if o.closed {
		o.mu.Unlock()
		return tgclient.InputPeer{}, ErrClosed
	}
	if peer, ok := o.peerCache.get(channelID); ok {
		o.mu.Unlock()
		return peer, nil
	}
	if resolution := o.peerResolutions[channelID]; resolution != nil {
		resolution.waiters++
		o.mu.Unlock()
		return o.waitForPeer(ctx, channelID, resolution)
	}

	resolveCtx, cancel := context.WithCancel(o.lifetime)
	resolution := &peerResolution{
		ctx:     resolveCtx,
		cancel:  cancel,
		done:    make(chan struct{}),
		waiters: 1,
	}
	o.peerResolutions[channelID] = resolution
	o.mu.Unlock()

	go o.runPeerResolution(channelID, resolution)
	return o.waitForPeer(ctx, channelID, resolution)
}

func (o *Opener) runPeerResolution(channelID int64, resolution *peerResolution) {
	defer resolution.cancel()
	peer, err := o.peers.ResolvePeer(resolution.ctx, channelID)

	o.mu.Lock()
	resolution.peer = peer
	resolution.err = err
	resolution.completed = true
	current := o.peerResolutions[channelID]
	if current == resolution {
		delete(o.peerResolutions, channelID)
		if err == nil && resolution.ctx.Err() == nil && !resolution.abandoned && !o.closed {
			o.peerCache.put(channelID, peer)
		}
	}
	close(resolution.done)
	o.mu.Unlock()
}

func (o *Opener) waitForPeer(
	ctx context.Context,
	channelID int64,
	resolution *peerResolution,
) (tgclient.InputPeer, error) {
	select {
	case <-resolution.done:
		if err := o.ensureOpen(); err != nil {
			return tgclient.InputPeer{}, err
		}
		if resolution.err != nil {
			return tgclient.InputPeer{}, resolution.err
		}
		return resolution.peer, nil
	case <-ctx.Done():
		o.releasePeerWaiter(channelID, resolution)
		if err := o.ensureOpen(); err != nil {
			return tgclient.InputPeer{}, err
		}
		return tgclient.InputPeer{}, ctx.Err()
	case <-o.lifetime.Done():
		o.releasePeerWaiter(channelID, resolution)
		return tgclient.InputPeer{}, ErrClosed
	}
}

func (o *Opener) releasePeerWaiter(channelID int64, resolution *peerResolution) {
	shouldCancel := false
	o.mu.Lock()
	if current := o.peerResolutions[channelID]; current == resolution && !resolution.completed {
		resolution.waiters--
		if resolution.waiters == 0 {
			resolution.abandoned = true
			delete(o.peerResolutions, channelID)
			shouldCancel = true
		}
	}
	o.mu.Unlock()
	if shouldCancel {
		resolution.cancel()
	}
}

// resolvedPeerCache is an LRU of value-only Telegram peers. Returning values,
// rather than pointers into cache entries, keeps callers from mutating cache
// state after a lookup.
type resolvedPeerCache struct {
	capacity int
	entries  map[int64]*list.Element
	recent   list.List
}

type resolvedPeerEntry struct {
	channelID int64
	peer      tgclient.InputPeer
}

func newResolvedPeerCache(capacity int) *resolvedPeerCache {
	return &resolvedPeerCache{
		capacity: capacity,
		entries:  make(map[int64]*list.Element, capacity),
	}
}

func (c *resolvedPeerCache) get(channelID int64) (tgclient.InputPeer, bool) {
	if c == nil {
		return tgclient.InputPeer{}, false
	}
	element := c.entries[channelID]
	if element == nil {
		return tgclient.InputPeer{}, false
	}
	c.recent.MoveToFront(element)
	entry := element.Value.(resolvedPeerEntry)
	return entry.peer, true
}

func (c *resolvedPeerCache) put(channelID int64, peer tgclient.InputPeer) {
	if c == nil || c.capacity <= 0 {
		return
	}
	if element := c.entries[channelID]; element != nil {
		c.recent.MoveToFront(element)
		return
	}
	element := c.recent.PushFront(resolvedPeerEntry{channelID: channelID, peer: peer})
	c.entries[channelID] = element
	if c.recent.Len() <= c.capacity {
		return
	}
	lru := c.recent.Back()
	entry := lru.Value.(resolvedPeerEntry)
	delete(c.entries, entry.channelID)
	c.recent.Remove(lru)
}

func (c *resolvedPeerCache) clear() {
	if c == nil {
		return
	}
	c.entries = make(map[int64]*list.Element, c.capacity)
	c.recent.Init()
}

func (c *resolvedPeerCache) len() int {
	if c == nil {
		return 0
	}
	return len(c.entries)
}
