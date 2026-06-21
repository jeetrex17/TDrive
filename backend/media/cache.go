package media

import (
	"container/list"
	"sync"
)

type blockCache struct {
	mu       sync.Mutex
	maxBytes int64
	used     int64
	items    map[string]*list.Element
	lru      *list.List
}

type cacheEntry struct {
	key  string
	data []byte
}

func newBlockCache(maxBytes int64) *blockCache {
	if maxBytes <= 0 {
		return nil
	}
	return &blockCache{
		maxBytes: maxBytes,
		items:    make(map[string]*list.Element),
		lru:      list.New(),
	}
}

func (c *blockCache) get(key string) ([]byte, bool) {
	if c == nil {
		return nil, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	elem, ok := c.items[key]
	if !ok {
		return nil, false
	}
	c.lru.MoveToFront(elem)
	return elem.Value.(*cacheEntry).data, true
}

func (c *blockCache) put(key string, data []byte) {
	if c == nil || len(data) == 0 {
		return
	}
	stored := append([]byte(nil), data...)

	c.mu.Lock()
	defer c.mu.Unlock()
	if elem, ok := c.items[key]; ok {
		ent := elem.Value.(*cacheEntry)
		c.used -= int64(len(ent.data))
		ent.data = stored
		c.used += int64(len(stored))
		c.lru.MoveToFront(elem)
		c.evictLocked()
		return
	}
	elem := c.lru.PushFront(&cacheEntry{key: key, data: stored})
	c.items[key] = elem
	c.used += int64(len(stored))
	c.evictLocked()
}

func (c *blockCache) evictLocked() {
	for c.used > c.maxBytes && c.lru.Len() > 1 {
		elem := c.lru.Back()
		if elem == nil {
			return
		}
		ent := elem.Value.(*cacheEntry)
		delete(c.items, ent.key)
		c.used -= int64(len(ent.data))
		c.lru.Remove(elem)
	}
}
