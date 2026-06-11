package thumbnail

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCachePutGetRoundTrip(t *testing.T) {
	c := NewCache(t.TempDir(), 1<<20)
	want := []byte("thumbnail-bytes")
	if err := c.Put("100-200", want); err != nil {
		t.Fatalf("put: %v", err)
	}
	got, ok := c.Get("100-200")
	if !ok {
		t.Fatalf("get miss after put")
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestCacheMissReturnsFalse(t *testing.T) {
	c := NewCache(t.TempDir(), 1<<20)
	if _, ok := c.Get("nope"); ok {
		t.Fatalf("expected miss")
	}
}

func TestNilCacheIsDisabled(t *testing.T) {
	var c *Cache
	if err := c.Put("k", []byte("v")); err != nil {
		t.Fatalf("nil put: %v", err)
	}
	if _, ok := c.Get("k"); ok {
		t.Fatalf("nil cache should always miss")
	}
}

func TestCacheEvictsToStayWithinBudget(t *testing.T) {
	dir := t.TempDir()
	c := NewCache(dir, 250)
	val := bytes.Repeat([]byte{1}, 100)

	if err := c.Put("a", val); err != nil {
		t.Fatalf("put a: %v", err)
	}
	if err := c.Put("b", val); err != nil {
		t.Fatalf("put b: %v", err)
	}
	if err := c.Put("c", val); err != nil { // 300 bytes total > 250 budget
		t.Fatalf("put c: %v", err)
	}

	c.mu.Lock()
	used, count := c.used, len(c.entries)
	c.mu.Unlock()
	if used > 250 {
		t.Fatalf("used = %d, want <= 250", used)
	}
	if count != 2 {
		t.Fatalf("entries = %d, want 2", count)
	}

	// The on-disk file count must match the in-memory index.
	files, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("readdir: %v", err)
	}
	bins := 0
	for _, f := range files {
		if filepath.Ext(f.Name()) == cacheFileSuffix {
			bins++
		}
	}
	if bins != 2 {
		t.Fatalf("on-disk thumbnails = %d, want 2", bins)
	}
}

func TestCacheEvictsLeastRecentlyUsed(t *testing.T) {
	c := NewCache(t.TempDir(), 250)
	val := bytes.Repeat([]byte{2}, 100)

	mustPut := func(k string) {
		if err := c.Put(k, val); err != nil {
			t.Fatalf("put %s: %v", k, err)
		}
		time.Sleep(2 * time.Millisecond) // separate recency timestamps
	}

	mustPut("a")
	mustPut("b")
	if _, ok := c.Get("a"); !ok { // touch a so b becomes the oldest
		t.Fatalf("get a miss")
	}
	time.Sleep(2 * time.Millisecond)
	mustPut("c") // 300 > 250 -> evict the LRU, which is b

	if _, ok := c.Get("b"); ok {
		t.Fatalf("b should have been evicted as least-recently-used")
	}
	if _, ok := c.Get("a"); !ok {
		t.Fatalf("a should survive (recently used)")
	}
	if _, ok := c.Get("c"); !ok {
		t.Fatalf("c should survive (just written)")
	}
}

func TestCacheReloadsIndexFromDisk(t *testing.T) {
	dir := t.TempDir()
	first := NewCache(dir, 1<<20)
	if err := first.Put("persist", []byte("survives-restart")); err != nil {
		t.Fatalf("put: %v", err)
	}

	// A fresh cache over the same dir must rebuild its index by scanning.
	second := NewCache(dir, 1<<20)
	got, ok := second.Get("persist")
	if !ok {
		t.Fatalf("reloaded cache missed a persisted entry")
	}
	if string(got) != "survives-restart" {
		t.Fatalf("got %q, want survives-restart", got)
	}
}

func TestCacheCleansStrayTempFiles(t *testing.T) {
	dir := t.TempDir()
	stray := filepath.Join(dir, cacheTempPrefix+"leftover")
	if err := os.WriteFile(stray, []byte("interrupted"), 0o600); err != nil {
		t.Fatalf("write stray: %v", err)
	}

	c := NewCache(dir, 1<<20)
	// Force an index build (and thus temp cleanup).
	c.mu.Lock()
	c.ensureInitLocked()
	c.mu.Unlock()

	if _, err := os.Stat(stray); !os.IsNotExist(err) {
		t.Fatalf("stray temp file not cleaned: %v", err)
	}
}
