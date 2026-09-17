package projection

import (
	"container/list"
	"context"
	"sync"
)

const galleryTimelineCacheEntries = 8

var mediaTimelineCache = newGalleryTimelineCache(galleryTimelineCacheEntries)

type galleryTimelineCache struct {
	mu       sync.Mutex
	capacity int
	entries  map[string]*list.Element
	lru      *list.List
	loads    map[string]*galleryTimelineLoad
}

type galleryTimelineCacheEntry struct {
	generation string
	timeline   GalleryTimeline
}

type galleryTimelineLoad struct {
	ready    chan struct{}
	timeline GalleryTimeline
	err      error
}

func newGalleryTimelineCache(capacity int) *galleryTimelineCache {
	return &galleryTimelineCache{
		capacity: capacity,
		entries:  make(map[string]*list.Element),
		lru:      list.New(),
		loads:    make(map[string]*galleryTimelineLoad),
	}
}

func (c *galleryTimelineCache) get(generation string) (GalleryTimeline, bool) {
	if c == nil || generation == "" {
		return GalleryTimeline{}, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	elem, ok := c.entries[generation]
	if !ok {
		return GalleryTimeline{}, false
	}
	c.lru.MoveToFront(elem)
	return cloneGalleryTimeline(elem.Value.(galleryTimelineCacheEntry).timeline), true
}

func (c *galleryTimelineCache) put(timeline GalleryTimeline) {
	if c == nil || c.capacity <= 0 || timeline.Generation == "" {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.putLocked(timeline)
}

// load coalesces simultaneous cold requests for the same generation. Waiters
// retain their own read transaction and can cancel independently.
func (c *galleryTimelineCache) load(ctx context.Context, generation string, build func() (GalleryTimeline, error)) (GalleryTimeline, error) {
	if timeline, ok := c.get(generation); ok {
		return timeline, nil
	}
	c.mu.Lock()
	if elem, ok := c.entries[generation]; ok {
		c.lru.MoveToFront(elem)
		timeline := cloneGalleryTimeline(elem.Value.(galleryTimelineCacheEntry).timeline)
		c.mu.Unlock()
		return timeline, nil
	}
	if pending, ok := c.loads[generation]; ok {
		c.mu.Unlock()
		select {
		case <-ctx.Done():
			return GalleryTimeline{}, ctx.Err()
		case <-pending.ready:
			return cloneGalleryTimeline(pending.timeline), pending.err
		}
	}
	pending := &galleryTimelineLoad{ready: make(chan struct{})}
	c.loads[generation] = pending
	c.mu.Unlock()

	timeline, err := build()
	c.mu.Lock()
	if err == nil {
		c.putLocked(timeline)
	}
	pending.timeline = cloneGalleryTimeline(timeline)
	pending.err = err
	delete(c.loads, generation)
	close(pending.ready)
	c.mu.Unlock()
	return timeline, err
}

func (c *galleryTimelineCache) putLocked(timeline GalleryTimeline) {
	if c.capacity <= 0 || timeline.Generation == "" {
		return
	}
	stored := cloneGalleryTimeline(timeline)
	if elem, ok := c.entries[timeline.Generation]; ok {
		elem.Value = galleryTimelineCacheEntry{generation: timeline.Generation, timeline: stored}
		c.lru.MoveToFront(elem)
		return
	}
	elem := c.lru.PushFront(galleryTimelineCacheEntry{generation: timeline.Generation, timeline: stored})
	c.entries[timeline.Generation] = elem
	for len(c.entries) > c.capacity {
		oldest := c.lru.Back()
		if oldest == nil {
			break
		}
		entry := oldest.Value.(galleryTimelineCacheEntry)
		delete(c.entries, entry.generation)
		c.lru.Remove(oldest)
	}
}

func (c *galleryTimelineCache) len() int {
	if c == nil {
		return 0
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.entries)
}

func cloneGalleryTimeline(timeline GalleryTimeline) GalleryTimeline {
	clone := timeline
	clone.Buckets = make([]GalleryBucket, len(timeline.Buckets))
	copy(clone.Buckets, timeline.Buckets)
	clone.Anchors = make([]GalleryAnchor, len(timeline.Anchors))
	copy(clone.Anchors, timeline.Anchors)
	return clone
}
