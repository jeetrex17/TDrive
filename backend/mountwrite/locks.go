package mountwrite

import (
	"context"
	"fmt"
	"sort"
	"sync"
)

type KeyedLocker struct {
	mu      sync.Mutex
	entries map[string]*lockEntry
}

type lockEntry struct {
	token chan struct{}
	refs  int
}

type heldLock struct {
	key   string
	entry *lockEntry
}

func NewKeyedLocker() *KeyedLocker {
	return &KeyedLocker{entries: make(map[string]*lockEntry)}
}

func (l *KeyedLocker) Lock(ctx context.Context, keys ...string) (func(), error) {
	normalized := normalizeLockKeys(keys)
	if len(normalized) == 0 {
		return func() {}, nil
	}
	held := make([]heldLock, 0, len(normalized))
	for _, key := range normalized {
		entry := l.retain(key)
		select {
		case <-entry.token:
			held = append(held, heldLock{key: key, entry: entry})
		case <-ctx.Done():
			l.dropReference(key, entry)
			l.releaseHeld(held)
			return nil, ErrCanceled
		}
	}
	var once sync.Once
	return func() {
		once.Do(func() { l.releaseHeld(held) })
	}, nil
}

func (l *KeyedLocker) Len() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.entries)
}

func (l *KeyedLocker) retain(key string) *lockEntry {
	l.mu.Lock()
	defer l.mu.Unlock()
	entry := l.entries[key]
	if entry == nil {
		entry = &lockEntry{token: make(chan struct{}, 1)}
		entry.token <- struct{}{}
		l.entries[key] = entry
	}
	entry.refs++
	return entry
}

func (l *KeyedLocker) releaseHeld(held []heldLock) {
	for index := len(held) - 1; index >= 0; index-- {
		item := held[index]
		item.entry.token <- struct{}{}
		l.dropReference(item.key, item.entry)
	}
}

func (l *KeyedLocker) dropReference(key string, entry *lockEntry) {
	l.mu.Lock()
	defer l.mu.Unlock()
	current := l.entries[key]
	if current != entry {
		return
	}
	entry.refs--
	if entry.refs == 0 {
		delete(l.entries, key)
	}
}

func normalizeLockKeys(keys []string) []string {
	copyOfKeys := make([]string, 0, len(keys))
	for _, key := range keys {
		if key != "" {
			copyOfKeys = append(copyOfKeys, key)
		}
	}
	sort.Strings(copyOfKeys)
	result := copyOfKeys[:0]
	for _, key := range copyOfKeys {
		if len(result) == 0 || result[len(result)-1] != key {
			result = append(result, key)
		}
	}
	return result
}

func objectLockKey(driveID int64, objectID string) string {
	return fmt.Sprintf("d:%d:o:%s", driveID, objectID)
}

func namespaceLockKey(driveID int64, parentID, name string) string {
	return fmt.Sprintf("d:%d:p:%s:n:%s", driveID, parentID, name)
}
