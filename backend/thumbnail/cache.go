package thumbnail

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	cacheFileSuffix = ".bin"
	cacheTempPrefix = ".tmp-"
	cacheDirMode    = 0o700
	cacheFileMode   = 0o600
)

// Cache is a size-capped, on-disk store of generated thumbnails. Values are
// opaque bytes keyed by an arbitrary string; the caller is responsible for any
// encryption of the value before Put (encrypted drives store ciphertext here).
//
// Eviction is least-recently-used, approximated by an in-memory "last used"
// timestamp that is refreshed on every Get and Put. The index is built once by
// scanning the directory on first use, so the cache survives app restarts.
//
// All methods are safe for concurrent use. A nil *Cache is valid and behaves
// as a disabled cache (Get always misses, Put is a no-op), which lets the
// caller treat caching as optional.
type Cache struct {
	dir      string
	maxBytes int64

	mu      sync.Mutex
	entries map[string]entry
	used    int64
	inited  bool
}

type entry struct {
	size int64
	used time.Time
}

// NewCache returns a cache rooted at dir, holding at most maxBytes of
// thumbnails. The directory is created lazily on first write.
func NewCache(dir string, maxBytes int64) *Cache {
	return &Cache{
		dir:      dir,
		maxBytes: maxBytes,
		entries:  make(map[string]entry),
	}
}

// Get returns the cached bytes for key and refreshes its recency. The boolean
// is false on a miss (including a disabled cache or an entry that vanished
// from disk underneath us).
func (c *Cache) Get(key string) ([]byte, bool) {
	if c == nil || c.dir == "" {
		return nil, false
	}
	name := fileName(key)

	c.mu.Lock()
	c.ensureInitLocked()
	_, known := c.entries[name]
	c.mu.Unlock()
	if !known {
		return nil, false
	}

	path := filepath.Join(c.dir, name)
	data, err := os.ReadFile(path)
	if err != nil {
		c.mu.Lock()
		c.forgetLocked(name)
		c.mu.Unlock()
		return nil, false
	}

	now := time.Now()
	c.mu.Lock()
	if e, ok := c.entries[name]; ok {
		e.used = now
		c.entries[name] = e
	}
	c.mu.Unlock()
	_ = os.Chtimes(path, now, now)
	return data, true
}

// Has reports whether key is present and refreshes recency without reading the
// value. It is used by background schedulers that only need to avoid duplicate
// work; callers that need bytes should still use Get.
func (c *Cache) Has(key string) bool {
	if c == nil || c.dir == "" {
		return false
	}
	name := fileName(key)

	c.mu.Lock()
	c.ensureInitLocked()
	if _, known := c.entries[name]; !known {
		c.mu.Unlock()
		return false
	}
	now := time.Now()
	e := c.entries[name]
	e.used = now
	c.entries[name] = e
	c.mu.Unlock()

	path := filepath.Join(c.dir, name)
	if _, err := os.Stat(path); err != nil {
		c.mu.Lock()
		c.forgetLocked(name)
		c.mu.Unlock()
		return false
	}
	_ = os.Chtimes(path, now, now)
	return true
}

// Put stores data under key, replacing any previous value, and evicts the
// least-recently-used entries until the cache is within its size budget.
// Empty data and disabled caches are no-ops. Errors are I/O failures writing
// the file; a failed Put leaves the cache consistent.
func (c *Cache) Put(key string, data []byte) error {
	if c == nil || c.dir == "" || len(data) == 0 {
		return nil
	}
	name := fileName(key)

	if err := os.MkdirAll(c.dir, cacheDirMode); err != nil {
		return err
	}
	// Chmod after MkdirAll to match the app's 0700 convention and to repair an
	// existing dir that predates these perms (MkdirAll is a no-op then).
	_ = os.Chmod(c.dir, cacheDirMode)

	tmp, err := os.CreateTemp(c.dir, cacheTempPrefix+"*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}

	path := filepath.Join(c.dir, name)
	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	_ = os.Chmod(path, cacheFileMode)

	now := time.Now()
	_ = os.Chtimes(path, now, now)

	c.mu.Lock()
	c.ensureInitLocked()
	if old, ok := c.entries[name]; ok {
		c.used -= old.size
	}
	c.entries[name] = entry{size: int64(len(data)), used: now}
	c.used += int64(len(data))
	c.evictLocked()
	c.mu.Unlock()
	return nil
}

// ensureInitLocked scans the cache directory once to rebuild the in-memory
// index from whatever survived the last run. Stray temp files from an
// interrupted Put are cleaned up. Must be called with c.mu held.
func (c *Cache) ensureInitLocked() {
	if c.inited {
		return
	}
	c.inited = true

	dirEntries, err := os.ReadDir(c.dir)
	if err != nil {
		return // dir not created yet; nothing cached
	}
	for _, de := range dirEntries {
		if de.IsDir() {
			continue
		}
		fname := de.Name()
		if strings.HasPrefix(fname, cacheTempPrefix) {
			_ = os.Remove(filepath.Join(c.dir, fname))
			continue
		}
		if !strings.HasSuffix(fname, cacheFileSuffix) {
			continue
		}
		info, err := de.Info()
		if err != nil {
			continue
		}
		c.entries[fname] = entry{size: info.Size(), used: info.ModTime()}
		c.used += info.Size()
	}
	c.evictLocked()
}

// evictLocked removes least-recently-used entries until the cache fits its
// budget. It never evicts the last remaining entry, so a single oversized
// thumbnail is kept rather than deleted on the spot. Must hold c.mu.
func (c *Cache) evictLocked() {
	for c.used > c.maxBytes && len(c.entries) > 1 {
		oldestName := ""
		var oldestUsed time.Time
		for name, e := range c.entries {
			if oldestName == "" || e.used.Before(oldestUsed) {
				oldestName = name
				oldestUsed = e.used
			}
		}
		if oldestName == "" {
			return
		}
		_ = os.Remove(filepath.Join(c.dir, oldestName))
		c.forgetLocked(oldestName)
	}
}

func (c *Cache) forgetLocked(name string) {
	if e, ok := c.entries[name]; ok {
		c.used -= e.size
		delete(c.entries, name)
	}
}

// fileName maps an arbitrary key to a safe cache filename. Keys are produced
// from numeric IDs, so this is defensive rather than load-bearing.
func fileName(key string) string {
	var b strings.Builder
	b.Grow(len(key) + len(cacheFileSuffix))
	for _, r := range key {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	b.WriteString(cacheFileSuffix)
	return b.String()
}
