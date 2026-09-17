package thumbnail

import (
	"container/heap"
	"container/list"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
)

const (
	cacheFileSuffix = ".bin"
	cacheTempPrefix = ".tmp-"
	cacheDirMode    = 0o700
	cacheFileMode   = 0o600

	// The byte budget normally limits the cache first. The entry cap also
	// bounds metadata for deployments containing millions of tiny files.
	defaultMaxCacheEntries = 64 << 10
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
	dir        string
	maxBytes   int64
	maxEntries int

	mu      sync.Mutex
	entries map[string]*list.Element
	lru     *list.List
	used    int64
	inited  bool
}

type entry struct {
	name string
	size int64
	used time.Time
}

// NewCache returns a cache rooted at dir, holding at most maxBytes of
// thumbnails. The directory is created lazily on first write.
func NewCache(dir string, maxBytes int64) *Cache {
	return &Cache{
		dir:        dir,
		maxBytes:   maxBytes,
		maxEntries: defaultMaxCacheEntries,
		entries:    make(map[string]*list.Element),
		lru:        list.New(),
	}
}

// Get returns the cached bytes for key and refreshes its recency. The boolean
// is false on a miss (including a disabled cache or an entry that vanished
// from disk underneath us).
func (c *Cache) Get(key string) ([]byte, bool) {
	return c.GetLimited(key, 64<<20)
}

// GetLimited bounds disk input before allocation, including files modified after
// the LRU index was built. Corrupt or oversized entries behave as a cache miss.
func (c *Cache) GetLimited(key string, limit int64) ([]byte, bool) {
	if c == nil || c.dir == "" || limit <= 0 || limit > 64<<20 {
		return nil, false
	}
	c.mu.Lock()
	c.ensureInitLocked()
	name, known := c.lookupNameLocked(key)
	c.mu.Unlock()
	if !known {
		return nil, false
	}

	path := filepath.Join(c.dir, name)
	file, err := os.Open(path)
	if err != nil {
		c.mu.Lock()
		c.forgetLocked(name)
		c.mu.Unlock()
		return nil, false
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || info.Size() <= 0 || info.Size() > limit {
		return nil, false
	}
	data, err := io.ReadAll(io.LimitReader(file, limit+1))
	if int64(len(data)) > limit {
		return nil, false
	}
	if err != nil {
		c.mu.Lock()
		c.forgetLocked(name)
		c.mu.Unlock()
		return nil, false
	}

	now := time.Now()
	c.mu.Lock()
	c.touchLocked(name, now)
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
	c.mu.Lock()
	c.ensureInitLocked()
	name, known := c.lookupNameLocked(key)
	if !known {
		c.mu.Unlock()
		return false
	}
	c.mu.Unlock()

	path := filepath.Join(c.dir, name)
	if _, err := os.Stat(path); err != nil {
		c.mu.Lock()
		c.forgetLocked(name)
		c.mu.Unlock()
		return false
	}
	now := time.Now()
	c.mu.Lock()
	c.touchLocked(name, now)
	c.mu.Unlock()
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
	// Build the index before publishing any temporary file. Otherwise the first
	// concurrent Put could classify another writer's live temp file as crash
	// residue and remove it before rename.
	c.mu.Lock()
	c.ensureInitLocked()
	c.mu.Unlock()
	name := cacheRelativeName(key)

	path := filepath.Join(c.dir, name)
	entryDir := filepath.Dir(path)
	if err := os.MkdirAll(entryDir, cacheDirMode); err != nil {
		return err
	}
	// Chmod after MkdirAll repairs directories created by older versions or a
	// process umask while retaining private cache contents.
	_ = os.Chmod(c.dir, cacheDirMode)
	_ = os.Chmod(filepath.Dir(entryDir), cacheDirMode)
	_ = os.Chmod(entryDir, cacheDirMode)

	tmp, err := os.CreateTemp(entryDir, cacheTempPrefix+"*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	if err := tmp.Chmod(cacheFileMode); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}

	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	_ = os.Chmod(path, cacheFileMode)

	now := time.Now()
	_ = os.Chtimes(path, now, now)

	c.mu.Lock()
	defer c.mu.Unlock()
	c.ensureInitLocked()
	// Another Put for this key may have published after our rename. Account the
	// file that is actually on disk, rather than this caller's input length, so
	// concurrent same-key writes cannot drift the byte budget.
	info, statErr := os.Stat(path)
	if statErr != nil {
		c.forgetLocked(name)
		if os.IsNotExist(statErr) {
			return nil
		}
		return statErr
	}
	c.forgetLocked(name)
	legacy := legacyFileName(key)
	if legacy != name {
		c.removeLocked(legacy)
	}
	elem := c.lru.PushFront(entry{name: name, size: info.Size(), used: now})
	c.entries[name] = elem
	c.used += info.Size()
	c.evictLocked()
	return nil
}

// ensureInitLocked walks the sharded cache once to rebuild the bounded
// in-memory index. It also recognizes legacy flat entries so upgrades keep
// their warm cache. Stray temporary and zero-byte files are removed.
func (c *Cache) ensureInitLocked() {
	if c.inited {
		return
	}
	c.inited = true

	found := make(entryHeap, 0, min(c.entryLimit(), 1024))
	var foundBytes int64
	err := filepath.WalkDir(c.dir, func(path string, de os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		if de.IsDir() {
			_ = os.Chmod(path, cacheDirMode)
			return nil
		}
		if de.Type()&os.ModeSymlink != 0 {
			return nil
		}
		name := de.Name()
		if strings.HasPrefix(name, cacheTempPrefix) {
			_ = os.Remove(path)
			return nil
		}
		if !strings.HasSuffix(name, cacheFileSuffix) {
			return nil
		}
		info, err := de.Info()
		if err != nil {
			return nil
		}
		if info.Size() <= 0 {
			_ = os.Remove(path)
			return nil
		}
		relative, err := filepath.Rel(c.dir, path)
		if err != nil || relative == "." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return nil
		}
		candidate := entry{name: relative, size: info.Size(), used: info.ModTime()}
		heap.Push(&found, candidate)
		foundBytes += candidate.size
		for len(found) > 1 && (len(found) > c.entryLimit() || foundBytes > c.maxBytes) {
			oldest := heap.Pop(&found).(entry)
			foundBytes -= oldest.size
			_ = os.Remove(filepath.Join(c.dir, oldest.name))
		}
		return nil
	})
	if err != nil {
		return // a missing directory is an empty cache
	}
	// Most recently used first, so the LRU list is rebuilt newest to oldest.
	slices.SortFunc(found, func(a, b entry) int {
		if order := b.used.Compare(a.used); order != 0 {
			return order
		}
		return strings.Compare(a.name, b.name)
	})
	for _, e := range found {
		elem := c.lru.PushBack(e)
		c.entries[e.name] = elem
		c.used += e.size
	}
	c.evictLocked()
}

// evictLocked removes least-recently-used entries until the cache fits its
// budget. It never evicts the last remaining entry, so a single oversized
// thumbnail is kept rather than deleted on the spot. Must hold c.mu.
func (c *Cache) evictLocked() {
	for (c.used > c.maxBytes || len(c.entries) > c.entryLimit()) && len(c.entries) > 1 {
		elem := c.lru.Back()
		if elem == nil {
			return
		}
		e := elem.Value.(entry)
		c.removeLocked(e.name)
	}
}

func (c *Cache) entryLimit() int {
	if c.maxEntries < 1 {
		return 1
	}
	return c.maxEntries
}

func (c *Cache) removeLocked(name string) {
	if _, ok := c.entries[name]; !ok {
		return
	}
	_ = os.Remove(filepath.Join(c.dir, name))
	c.forgetLocked(name)
	// Best-effort cleanup prevents empty hash directories from accumulating.
	parent := filepath.Dir(filepath.Join(c.dir, name))
	if parent != c.dir {
		_ = os.Remove(parent)
		grandparent := filepath.Dir(parent)
		if grandparent != c.dir {
			_ = os.Remove(grandparent)
		}
	}
}

func (c *Cache) forgetLocked(name string) {
	if elem, ok := c.entries[name]; ok {
		e := elem.Value.(entry)
		c.used -= e.size
		c.lru.Remove(elem)
		delete(c.entries, name)
	}
}

func (c *Cache) touchLocked(name string, now time.Time) {
	elem, ok := c.entries[name]
	if !ok {
		return
	}
	e := elem.Value.(entry)
	e.used = now
	elem.Value = e
	c.lru.MoveToFront(elem)
}

func (c *Cache) lookupNameLocked(key string) (string, bool) {
	name := cacheRelativeName(key)
	if _, ok := c.entries[name]; ok {
		return name, true
	}
	legacy := legacyFileName(key)
	_, ok := c.entries[legacy]
	return legacy, ok
}

// cacheRelativeName maps arbitrary key bytes to a deterministic, collision-
// resistant two-level path. No caller-controlled path component reaches disk.
func cacheRelativeName(key string) string {
	digest := sha256.Sum256([]byte(key))
	encoded := hex.EncodeToString(digest[:])
	return filepath.Join(encoded[:2], encoded[2:4], encoded+cacheFileSuffix)
}

// legacyFileName supports caches created before hash sharding. New writes never
// use this lossy mapping, and Put removes a matching legacy entry.
func legacyFileName(key string) string {
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

type entryHeap []entry

func (h entryHeap) Len() int { return len(h) }
func (h entryHeap) Less(i, j int) bool {
	if h[i].used.Equal(h[j].used) {
		return h[i].name > h[j].name
	}
	return h[i].used.Before(h[j].used)
}
func (h entryHeap) Swap(i, j int)   { h[i], h[j] = h[j], h[i] }
func (h *entryHeap) Push(value any) { *h = append(*h, value.(entry)) }
func (h *entryHeap) Pop() any {
	old := *h
	last := len(old) - 1
	value := old[last]
	*h = old[:last]
	return value
}
