// Package mountcache contains the bounded caches and coalesced loaders shared
// by the desktop mount implementations.
package mountcache

import (
	"container/list"
	"sync"
	"time"
)

// LRUConfig controls cache admission, expiration, and value ownership.
type LRUConfig[V any] struct {
	Capacity  int
	MaxWeight int
	TTL       time.Duration
	Now       func() time.Time
	Weight    func(V) int
	Clone     func(V) V
}

// LRU is a concurrency-safe, least-recently-used cache. Values are cloned on
// insertion and retrieval when LRUConfig.Clone is set.
type LRU[K comparable, V any] struct {
	mu          sync.Mutex
	entries     map[K]*list.Element
	recent      list.List
	capacity    int
	maxWeight   int
	totalWeight int
	ttl         time.Duration
	now         func() time.Time
	weight      func(V) int
	clone       func(V) V
}

type lruEntry[K comparable, V any] struct {
	key       K
	value     V
	weight    int
	expiresAt time.Time
}

// NewLRU constructs an empty cache. A non-positive capacity disables
// admission. A zero maximum weight or TTL means that bound is disabled.
func NewLRU[K comparable, V any](config LRUConfig[V]) *LRU[K, V] {
	if config.Capacity < 0 {
		config.Capacity = 0
	}
	if config.MaxWeight < 0 {
		config.MaxWeight = 0
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	if config.Weight == nil {
		config.Weight = func(V) int { return 1 }
	}
	if config.Clone == nil {
		config.Clone = func(value V) V { return value }
	}
	return &LRU[K, V]{
		entries:   make(map[K]*list.Element, config.Capacity),
		capacity:  config.Capacity,
		maxWeight: config.MaxWeight,
		ttl:       config.TTL,
		now:       config.Now,
		weight:    config.Weight,
		clone:     config.Clone,
	}
}

// Get returns a caller-owned value and refreshes its recency.
func (cache *LRU[K, V]) Get(key K) (V, bool) {
	var zero V
	if cache == nil {
		return zero, false
	}
	cache.mu.Lock()
	defer cache.mu.Unlock()

	element := cache.entries[key]
	if element == nil {
		return zero, false
	}
	entry := element.Value.(lruEntry[K, V])
	if !entry.expiresAt.IsZero() && !cache.now().Before(entry.expiresAt) {
		cache.removeLocked(element)
		return zero, false
	}
	cache.recent.MoveToFront(element)
	return cache.clone(entry.value), true
}

// Put inserts value when it fits all configured bounds. Replacing a key with
// an oversized value removes its previous cached value.
func (cache *LRU[K, V]) Put(key K, value V) bool {
	if cache == nil || cache.capacity <= 0 {
		return false
	}
	stored := cache.clone(value)
	weight := cache.weight(stored)
	if weight < 0 {
		weight = 0
	}

	cache.mu.Lock()
	defer cache.mu.Unlock()
	if element := cache.entries[key]; element != nil {
		cache.removeLocked(element)
	}
	if cache.maxWeight > 0 && weight > cache.maxWeight {
		return false
	}

	var expiresAt time.Time
	if cache.ttl > 0 {
		expiresAt = cache.now().Add(cache.ttl)
	}
	element := cache.recent.PushFront(lruEntry[K, V]{
		key:       key,
		value:     stored,
		weight:    weight,
		expiresAt: expiresAt,
	})
	cache.entries[key] = element
	cache.totalWeight += weight
	for cache.recent.Len() > cache.capacity ||
		(cache.maxWeight > 0 && cache.totalWeight > cache.maxWeight) {
		cache.removeLocked(cache.recent.Back())
	}
	return cache.entries[key] == element
}

// Delete evicts key and returns its stored value even when it has expired.
// This lets invalidators inspect an old immutable value while removing it.
func (cache *LRU[K, V]) Delete(key K) (V, bool) {
	var zero V
	if cache == nil {
		return zero, false
	}
	cache.mu.Lock()
	defer cache.mu.Unlock()
	element := cache.entries[key]
	if element == nil {
		return zero, false
	}
	entry := element.Value.(lruEntry[K, V])
	cache.removeLocked(element)
	return cache.clone(entry.value), true
}

// Clear removes all cached values.
func (cache *LRU[K, V]) Clear() {
	if cache == nil {
		return
	}
	cache.mu.Lock()
	cache.entries = make(map[K]*list.Element, cache.capacity)
	cache.recent.Init()
	cache.totalWeight = 0
	cache.mu.Unlock()
}

// Len returns the number of retained values.
func (cache *LRU[K, V]) Len() int {
	if cache == nil {
		return 0
	}
	cache.mu.Lock()
	defer cache.mu.Unlock()
	return cache.recent.Len()
}

// Weight returns the aggregate retained weight.
func (cache *LRU[K, V]) Weight() int {
	if cache == nil {
		return 0
	}
	cache.mu.Lock()
	defer cache.mu.Unlock()
	return cache.totalWeight
}

func (cache *LRU[K, V]) removeLocked(element *list.Element) {
	if element == nil {
		return
	}
	entry := element.Value.(lruEntry[K, V])
	delete(cache.entries, entry.key)
	cache.totalWeight -= entry.weight
	cache.recent.Remove(element)
}
