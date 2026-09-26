package media

import "testing"

func TestBlockCacheRejectsOversizedEntry(t *testing.T) {
	cache := newBlockCache(4)
	cache.put("large", []byte("12345"))
	if _, ok := cache.get("large"); ok {
		t.Fatal("oversized block was cached")
	}
	if cache.used != 0 || cache.lru.Len() != 0 {
		t.Fatalf("used=%d entries=%d, want empty cache", cache.used, cache.lru.Len())
	}
}

func TestBlockCacheEvictsLastEntryWhenReplacementMakesItOversized(t *testing.T) {
	cache := newBlockCache(4)
	cache.put("entry", []byte("1234"))
	cache.put("entry", []byte("12345"))
	if _, ok := cache.get("entry"); !ok {
		t.Fatal("an oversized replacement should leave the existing block intact")
	}
	if cache.used != 4 {
		t.Fatalf("used=%d, want 4", cache.used)
	}
}
