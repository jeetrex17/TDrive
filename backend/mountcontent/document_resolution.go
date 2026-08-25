package mountcontent

import (
	"context"

	"TDrive/backend/media"
	"TDrive/backend/tgclient"
)

type documentResolution struct {
	ctx       context.Context
	cancel    context.CancelFunc
	done      chan struct{}
	waiters   int
	ref       tgclient.DocumentRef
	err       error
	completed bool
	abandoned bool
}

// resolveDocument coalesces Telegram message lookups by immutable projected
// segment identity. Caller cancellation only abandons that caller; the shared
// operation continues until its final waiter leaves or the opener closes.
func (o *Opener) resolveDocument(
	ctx context.Context,
	peer tgclient.InputPeer,
	projected media.Segment,
) (tgclient.DocumentRef, error) {
	if ctx == nil {
		return tgclient.DocumentRef{}, ErrNilContext
	}
	if err := ctx.Err(); err != nil {
		return tgclient.DocumentRef{}, err
	}
	key := newDocumentRefKey(peer, projected)

	o.mu.Lock()
	if o.closed {
		o.mu.Unlock()
		return tgclient.DocumentRef{}, ErrClosed
	}
	if ref, ok := o.documentCache.get(key); ok {
		o.mu.Unlock()
		return ref, nil
	}
	if resolution := o.documentResolutions[key]; resolution != nil {
		resolution.waiters++
		o.mu.Unlock()
		return o.waitForDocument(ctx, key, resolution)
	}

	resolveCtx, cancel := context.WithCancel(o.lifetime)
	resolution := &documentResolution{
		ctx:     resolveCtx,
		cancel:  cancel,
		done:    make(chan struct{}),
		waiters: 1,
	}
	o.documentResolutions[key] = resolution
	o.mu.Unlock()

	go o.runDocumentResolution(key, projected, resolution)
	return o.waitForDocument(ctx, key, resolution)
}

func (o *Opener) runDocumentResolution(
	key documentRefKey,
	projected media.Segment,
	resolution *documentResolution,
) {
	defer resolution.cancel()
	ref, err := o.resolveDocumentUncached(resolution.ctx, key.peer, projected)
	ref = cloneDocumentRef(ref)

	o.mu.Lock()
	resolution.ref = ref
	resolution.err = err
	resolution.completed = true
	if current := o.documentResolutions[key]; current == resolution {
		delete(o.documentResolutions, key)
		if err == nil && resolution.ctx.Err() == nil && !resolution.abandoned && !o.closed {
			o.documentCache.put(key, ref)
		}
	}
	close(resolution.done)
	o.mu.Unlock()
}

func (o *Opener) resolveDocumentUncached(
	ctx context.Context,
	peer tgclient.InputPeer,
	projected media.Segment,
) (tgclient.DocumentRef, error) {
	select {
	case o.resolveSlots <- struct{}{}:
		defer func() { <-o.resolveSlots }()
	case <-ctx.Done():
		return tgclient.DocumentRef{}, ctx.Err()
	}
	if err := o.ensureOpen(); err != nil {
		return tgclient.DocumentRef{}, err
	}
	if err := ctx.Err(); err != nil {
		return tgclient.DocumentRef{}, err
	}

	ref, err := o.ranges.ResolveDocument(ctx, peer, projected.MsgID)
	if err != nil {
		return tgclient.DocumentRef{}, err
	}
	if err := validateDocumentIdentity(peer, projected, ref); err != nil {
		return tgclient.DocumentRef{}, err
	}
	return ref, nil
}

func (o *Opener) waitForDocument(
	ctx context.Context,
	key documentRefKey,
	resolution *documentResolution,
) (tgclient.DocumentRef, error) {
	select {
	case <-resolution.done:
		if err := o.ensureOpen(); err != nil {
			return tgclient.DocumentRef{}, err
		}
		if resolution.err != nil {
			return tgclient.DocumentRef{}, resolution.err
		}
		return cloneDocumentRef(resolution.ref), nil
	case <-ctx.Done():
		o.releaseDocumentWaiter(key, resolution)
		if err := o.ensureOpen(); err != nil {
			return tgclient.DocumentRef{}, err
		}
		return tgclient.DocumentRef{}, ctx.Err()
	case <-o.lifetime.Done():
		o.releaseDocumentWaiter(key, resolution)
		return tgclient.DocumentRef{}, ErrClosed
	}
}

func (o *Opener) releaseDocumentWaiter(
	key documentRefKey,
	resolution *documentResolution,
) {
	shouldCancel := false
	o.mu.Lock()
	if current := o.documentResolutions[key]; current == resolution && !resolution.completed {
		resolution.waiters--
		if resolution.waiters == 0 {
			resolution.abandoned = true
			delete(o.documentResolutions, key)
			shouldCancel = true
		}
	}
	o.mu.Unlock()
	if shouldCancel {
		resolution.cancel()
	}
}
