package mountcontent

import (
	"container/list"
	"time"

	"TDrive/backend/media"
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
	capacity int
	ttl      time.Duration
	now      func() time.Time
	entries  map[documentRefKey]*list.Element
	recent   list.List
}

type documentRefEntry struct {
	key       documentRefKey
	ref       tgclient.DocumentRef
	expiresAt time.Time
}

func newDocumentRefCache(capacity int, ttl time.Duration, now func() time.Time) *documentRefCache {
	if capacity < 0 {
		capacity = 0
	}
	if now == nil {
		now = time.Now
	}
	return &documentRefCache{
		capacity: capacity,
		ttl:      ttl,
		now:      now,
		entries:  make(map[documentRefKey]*list.Element, capacity),
	}
}

func (cache *documentRefCache) get(key documentRefKey) (tgclient.DocumentRef, bool) {
	if cache == nil {
		return tgclient.DocumentRef{}, false
	}
	element := cache.entries[key]
	if element == nil {
		return tgclient.DocumentRef{}, false
	}
	entry := element.Value.(documentRefEntry)
	if !entry.expiresAt.IsZero() && !cache.now().Before(entry.expiresAt) {
		delete(cache.entries, key)
		cache.recent.Remove(element)
		return tgclient.DocumentRef{}, false
	}
	cache.recent.MoveToFront(element)
	return cloneDocumentRef(entry.ref), true
}

func (cache *documentRefCache) put(key documentRefKey, ref tgclient.DocumentRef) {
	if cache == nil || cache.capacity <= 0 || cache.ttl <= 0 {
		return
	}
	entry := documentRefEntry{
		key:       key,
		ref:       cloneDocumentRef(ref),
		expiresAt: cache.now().Add(cache.ttl),
	}
	if element := cache.entries[key]; element != nil {
		element.Value = entry
		cache.recent.MoveToFront(element)
		return
	}
	element := cache.recent.PushFront(entry)
	cache.entries[key] = element
	if cache.recent.Len() <= cache.capacity {
		return
	}
	lru := cache.recent.Back()
	lruEntry := lru.Value.(documentRefEntry)
	delete(cache.entries, lruEntry.key)
	cache.recent.Remove(lru)
}

func (cache *documentRefCache) clear() {
	if cache == nil {
		return
	}
	cache.entries = make(map[documentRefKey]*list.Element, cache.capacity)
	cache.recent.Init()
}

func (cache *documentRefCache) len() int {
	if cache == nil {
		return 0
	}
	return len(cache.entries)
}

func cloneDocumentRef(ref tgclient.DocumentRef) tgclient.DocumentRef {
	ref.FileReference = append([]byte(nil), ref.FileReference...)
	return ref
}
