package mountcontent

import (
	"time"

	"TDrive/backend/media"
	"TDrive/backend/mountcache"
	"TDrive/backend/tgclient"
)

const (
	// A mount normally revisits a small working set. Keeping 384 references
	// for three minutes avoids repeat getMessages calls without retaining an
	// unbounded Telegram catalog in a solo-user desktop process. Range reads
	// refresh expired file references, so this cache is only an optimization.
	maxCachedDocumentRefs = 384
	documentRefCacheTTL   = 3 * time.Minute
)

type documentRefKey struct {
	peer          tgclient.InputPeer
	msgID         int64
	projectedSize int64
}

func newDocumentRefKey(peer tgclient.InputPeer, projected media.Segment) documentRefKey {
	return documentRefKey{
		peer:          peer,
		msgID:         projected.MsgID,
		projectedSize: projected.Size,
	}
}

type documentRefCache struct {
	entries *mountcache.LRU[documentRefKey, tgclient.DocumentRef]
}

func newDocumentRefCache(capacity int, ttl time.Duration, now func() time.Time) *documentRefCache {
	if ttl <= 0 {
		capacity = 0
	}
	return &documentRefCache{
		entries: mountcache.NewLRU[documentRefKey, tgclient.DocumentRef](
			mountcache.LRUConfig[tgclient.DocumentRef]{
				Capacity: capacity,
				TTL:      ttl,
				Now:      now,
				Clone:    cloneDocumentRef,
			},
		),
	}
}

func (cache *documentRefCache) get(key documentRefKey) (tgclient.DocumentRef, bool) {
	if cache == nil {
		return tgclient.DocumentRef{}, false
	}
	return cache.entries.Get(key)
}

func (cache *documentRefCache) put(key documentRefKey, ref tgclient.DocumentRef) {
	if cache == nil {
		return
	}
	cache.entries.Put(key, ref)
}

func (cache *documentRefCache) clear() {
	if cache == nil {
		return
	}
	cache.entries.Clear()
}

func (cache *documentRefCache) len() int {
	if cache == nil {
		return 0
	}
	return cache.entries.Len()
}

func cloneDocumentRef(ref tgclient.DocumentRef) tgclient.DocumentRef {
	ref.FileReference = append([]byte(nil), ref.FileReference...)
	return ref
}
