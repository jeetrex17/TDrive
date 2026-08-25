package mountcontent

import (
	"context"

	"TDrive/backend/media"
	"TDrive/backend/tgclient"
)

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
	if err := o.ensureOpen(); err != nil {
		return tgclient.DocumentRef{}, err
	}
	key := newDocumentRefKey(peer, projected)

	ref, err := o.documentResolutions.Load(
		ctx,
		o.lifetime,
		key,
		func() (tgclient.DocumentRef, bool) {
			return o.documentCache.get(key)
		},
		func(loadContext context.Context) (tgclient.DocumentRef, error) {
			return o.resolveDocumentUncached(loadContext, peer, projected)
		},
		func(resolved tgclient.DocumentRef) {
			o.documentCache.put(key, resolved)
		},
	)
	if openErr := o.ensureOpen(); openErr != nil {
		return tgclient.DocumentRef{}, openErr
	}
	return cloneDocumentRef(ref), err
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
