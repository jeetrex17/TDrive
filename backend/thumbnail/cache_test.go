package thumbnail

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"sync"
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

func TestCacheConcurrentSameKeyWritesKeepAccurateAccounting(t *testing.T) {
	cache := NewCache(t.TempDir(), 1<<20)
	const writers = 64
	start := make(chan struct{})
	var wait sync.WaitGroup
	wait.Add(writers)
	for i := 1; i <= writers; i++ {
		data := bytes.Repeat([]byte{byte(i)}, i*17)
		go func() {
			defer wait.Done()
			<-start
			if err := cache.Put("shared", data); err != nil {
				t.Errorf("Put: %v", err)
			}
		}()
	}
	close(start)
	wait.Wait()

	stored, ok := cache.Get("shared")
	if !ok {
		t.Fatal("shared cache entry missing")
	}
	cache.mu.Lock()
	defer cache.mu.Unlock()
	elem := cache.entries[cacheRelativeName("shared")]
	if elem == nil {
		t.Fatal("shared cache index entry missing")
	}
	indexed := elem.Value.(entry).size
	if indexed != int64(len(stored)) || cache.used != indexed {
		t.Fatalf("disk=%d indexed=%d used=%d", len(stored), indexed, cache.used)
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
	bins := 0
	err := filepath.WalkDir(dir, func(_ string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() && filepath.Ext(entry.Name()) == cacheFileSuffix {
			bins++
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk cache: %v", err)
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

func TestCacheGetLimitedRejectsOversizedEntry(t *testing.T) {
	cache := NewCache(t.TempDir(), 1<<20)
	if err := cache.Put("large", []byte("12345")); err != nil {
		t.Fatal(err)
	}
	if _, ok := cache.GetLimited("large", 4); ok {
		t.Fatal("oversized entry returned")
	}
	if raw, ok := cache.GetLimited("large", 5); !ok || string(raw) != "12345" {
		t.Fatalf("bounded entry=%q,%v", raw, ok)
	}
}

func TestCacheUsesCollisionResistantShardedPaths(t *testing.T) {
	dir := t.TempDir()
	cache := NewCache(dir, 1<<20)
	for key, value := range map[string]string{"a/b": "slash", "a?b": "question"} {
		if err := cache.Put(key, []byte(value)); err != nil {
			t.Fatal(err)
		}
	}
	for key, want := range map[string]string{"a/b": "slash", "a?b": "question"} {
		got, ok := cache.Get(key)
		if !ok || string(got) != want {
			t.Fatalf("get %q=%q,%v want %q", key, got, ok, want)
		}
		rel := cacheRelativeName(key)
		parts := strings.Split(filepath.ToSlash(rel), "/")
		if len(parts) != 3 || len(parts[0]) != 2 || len(parts[1]) != 2 {
			t.Fatalf("relative cache path %q is not two-level sharded", rel)
		}
		if _, err := os.Stat(filepath.Join(dir, rel)); err != nil {
			t.Fatalf("stat sharded entry: %v", err)
		}
	}
}

func TestCacheBoundsIndexedEntriesAndReloadsShards(t *testing.T) {
	dir := t.TempDir()
	cache := NewCache(dir, 1<<20)
	cache.maxEntries = 2
	for _, key := range []string{"one", "two", "three"} {
		if err := cache.Put(key, []byte(key)); err != nil {
			t.Fatal(err)
		}
		time.Sleep(2 * time.Millisecond)
	}
	if cache.Has("one") {
		t.Fatal("oldest entry survived the entry-count bound")
	}

	reloaded := NewCache(dir, 1<<20)
	reloaded.maxEntries = 2
	for _, key := range []string{"two", "three"} {
		got, ok := reloaded.Get(key)
		if !ok || string(got) != key {
			t.Fatalf("reloaded %q=%q,%v", key, got, ok)
		}
	}
}

func TestCacheShardsAndFilesKeepPrivatePermissions(t *testing.T) {
	dir := t.TempDir()
	cache := NewCache(dir, 1<<20)
	if err := cache.Put("private", []byte("thumbnail")); err != nil {
		t.Fatal(err)
	}
	rel := cacheRelativeName("private")
	parts := strings.Split(filepath.ToSlash(rel), "/")
	for _, path := range []string{dir, filepath.Join(dir, parts[0]), filepath.Join(dir, parts[0], parts[1])} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != cacheDirMode {
			t.Fatalf("directory %s mode=%o, want %o", path, info.Mode().Perm(), cacheDirMode)
		}
	}
	info, err := os.Stat(filepath.Join(dir, rel))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != cacheFileMode {
		t.Fatalf("file mode=%o, want %o", info.Mode().Perm(), cacheFileMode)
	}
}

func TestCacheReadsLegacyFlatEntryAndMigratesOnPut(t *testing.T) {
	dir := t.TempDir()
	key := "legacy/key"
	legacyPath := filepath.Join(dir, legacyFileName(key))
	if err := os.WriteFile(legacyPath, []byte("legacy"), cacheFileMode); err != nil {
		t.Fatal(err)
	}
	cache := NewCache(dir, 1<<20)
	if got, ok := cache.Get(key); !ok || string(got) != "legacy" {
		t.Fatalf("legacy get=%q,%v", got, ok)
	}
	if err := cache.Put(key, []byte("sharded")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(legacyPath); !os.IsNotExist(err) {
		t.Fatalf("legacy cache file remains after migration: %v", err)
	}
	if got, ok := cache.Get(key); !ok || string(got) != "sharded" {
		t.Fatalf("sharded get=%q,%v", got, ok)
	}
}
