package mountcache

import (
	"testing"
	"time"
)

func TestLRUAppliesTTLWeightLRUAndCloning(t *testing.T) {
	now := time.Date(2026, time.August, 25, 12, 0, 0, 0, time.UTC)
	cache := NewLRU[string, []byte](LRUConfig[[]byte]{
		Capacity:  2,
		MaxWeight: 4,
		TTL:       time.Minute,
		Now:       func() time.Time { return now },
		Weight:    func(value []byte) int { return len(value) },
		Clone:     func(value []byte) []byte { return append([]byte(nil), value...) },
	})

	first := []byte{1, 1}
	if !cache.Put("first", first) {
		t.Fatal("Put(first) rejected a cacheable value")
	}
	first[0] = 9
	got, ok := cache.Get("first")
	if !ok || got[0] != 1 {
		t.Fatalf("Get(first) = %v, %v, want a defensive copy", got, ok)
	}
	got[0] = 8
	if again, found := cache.Get("first"); !found || again[0] != 1 {
		t.Fatalf("second Get(first) = %v, %v, want isolated cached value", again, found)
	}

	cache.Put("second", []byte{2, 2})
	cache.Get("first")
	cache.Put("third", []byte{3, 3})
	if _, found := cache.Get("second"); found {
		t.Fatal("least-recently-used entry remained cached")
	}
	if got := cache.Len(); got != 2 {
		t.Fatalf("Len() = %d, want 2", got)
	}
	if got := cache.Weight(); got != 4 {
		t.Fatalf("Weight() = %d, want 4", got)
	}

	now = now.Add(time.Minute)
	if _, found := cache.Get("first"); found {
		t.Fatal("entry remained cached at its expiration time")
	}
	if got := cache.Len(); got != 1 {
		t.Fatalf("Len() after one expired lookup = %d, want 1", got)
	}
}

func TestLRURejectsOversizedValuesAndDeleteReturnsStoredValue(t *testing.T) {
	cache := NewLRU[string, string](LRUConfig[string]{
		Capacity:  2,
		MaxWeight: 3,
		Weight:    func(value string) int { return len(value) },
	})

	cache.Put("key", "old")
	if cache.Put("key", "oversized") {
		t.Fatal("Put() admitted a value over the weight budget")
	}
	if _, found := cache.Get("key"); found {
		t.Fatal("oversized replacement left the previous value cached")
	}

	cache.Put("kept", "ok")
	deleted, found := cache.Delete("kept")
	if !found || deleted != "ok" {
		t.Fatalf("Delete(kept) = %q, %v, want %q, true", deleted, found, "ok")
	}
	if got := cache.Len(); got != 0 {
		t.Fatalf("Len() after Delete = %d, want 0", got)
	}
}

func TestLRUClearAndDisabledCacheAreSafe(t *testing.T) {
	cache := NewLRU[string, int](LRUConfig[int]{
		Capacity:  -1,
		MaxWeight: -1,
	})
	if cache.Put("disabled", 1) {
		t.Fatal("Put() admitted a value into a disabled cache")
	}
	cache.Clear()
	if got := cache.Len(); got != 0 {
		t.Fatalf("Len() = %d, want 0", got)
	}

	enabled := NewLRU[string, int](LRUConfig[int]{Capacity: 1})
	enabled.Put("key", 1)
	enabled.Clear()
	if _, found := enabled.Get("key"); found {
		t.Fatal("Clear() retained a cached value")
	}
	if got := enabled.Weight(); got != 0 {
		t.Fatalf("Weight() after Clear = %d, want 0", got)
	}

	var nilCache *LRU[string, int]
	nilCache.Clear()
	if _, found := nilCache.Get("key"); found {
		t.Fatal("nil cache returned a value")
	}
	if nilCache.Put("key", 1) {
		t.Fatal("nil cache admitted a value")
	}
	if _, found := nilCache.Delete("key"); found {
		t.Fatal("nil cache deleted a value")
	}
	if nilCache.Len() != 0 || nilCache.Weight() != 0 {
		t.Fatal("nil cache reported retained state")
	}
}
