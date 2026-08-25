// Package mountscratch provides secure, quota-bounded scratch storage for
// mount implementations. Callers retain ownership of object-specific limits,
// lifecycle sequencing, and domain error mapping.
package mountscratch

import (
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

const (
	privateDirMode  os.FileMode = 0o700
	privateFileMode os.FileMode = 0o600
)

var (
	ErrInvalidConfig = errors.New("mountscratch: invalid configuration")
	ErrInvalidName   = errors.New("mountscratch: invalid file name")
	ErrInvalidSize   = errors.New("mountscratch: invalid reservation size")
	ErrQuotaExceeded = errors.New("mountscratch: aggregate quota exceeded")
)

// Config controls a Store's private root and aggregate byte budget.
type Config struct {
	Root            string
	MaxBytes        int64
	AccountExisting bool
}

// Store owns a private scratch directory and serializes aggregate quota
// accounting. Quota reservations are independent from file creation so
// callers can preserve their own write and rollback sequencing.
type Store struct {
	root         string
	maxBytes     int64
	mu           sync.Mutex
	usedBytes    int64
	trackedBytes map[string]int64
}

// NewStore creates or secures the configured scratch directory. When
// AccountExisting is true, regular files already present are included in the
// aggregate reservation and can later be released by name.
func NewStore(config Config) (*Store, error) {
	if config.Root == "" || config.MaxBytes <= 0 {
		return nil, ErrInvalidConfig
	}
	root, err := filepath.Abs(config.Root)
	if err != nil {
		return nil, fmt.Errorf("mountscratch: resolve root: %w", err)
	}
	if err := os.MkdirAll(root, privateDirMode); err != nil {
		return nil, fmt.Errorf("mountscratch: create root: %w", err)
	}
	if err := os.Chmod(root, privateDirMode); err != nil {
		return nil, fmt.Errorf("mountscratch: protect root: %w", err)
	}

	usedBytes := int64(0)
	trackedBytes := make(map[string]int64)
	if config.AccountExisting {
		usedBytes, trackedBytes, err = scanRegularFiles(root)
		if err != nil {
			return nil, err
		}
	}
	return &Store{
		root:         root,
		maxBytes:     config.MaxBytes,
		usedBytes:    usedBytes,
		trackedBytes: trackedBytes,
	}, nil
}

func (s *Store) Root() string {
	return s.root
}

func (s *Store) UsedBytes() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.usedBytes
}

// CreateExclusive creates name under the private root with mode 0600. The
// supplied flags select the access mode; creation and exclusivity are always
// enforced by the store.
func (s *Store) CreateExclusive(name string, flags int) (*os.File, error) {
	path, err := s.pathForName(name)
	if err != nil {
		return nil, err
	}
	return os.OpenFile(path, flags|os.O_CREATE|os.O_EXCL, privateFileMode)
}

// Reserve atomically adds size to the aggregate scratch usage.
func (s *Store) Reserve(size int64) error {
	if size < 0 {
		return ErrInvalidSize
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if size > s.maxBytes || s.usedBytes > s.maxBytes-size {
		return ErrQuotaExceeded
	}
	s.usedBytes += size
	return nil
}

// Release returns a prior reservation. Releasing more than the current usage
// clamps the counter at zero so cleanup paths remain idempotent.
func (s *Store) Release(size int64) {
	if size <= 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.releaseLocked(size)
}

// Track associates an existing reservation with a file name. It does not
// alter aggregate usage.
func (s *Store) Track(name string, size int64) {
	if name == "" || size < 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.trackedBytes[name] = size
}

// ReleaseTracked removes name's tracked reservation. fallbackSize is used
// for files created before this process when no explicit record exists.
func (s *Store) ReleaseTracked(name string, fallbackSize int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	size, found := s.trackedBytes[name]
	if !found {
		size = fallbackSize
	}
	delete(s.trackedBytes, name)
	s.releaseLocked(size)
}

func (s *Store) releaseLocked(size int64) {
	if size <= 0 {
		return
	}
	s.usedBytes -= size
	if s.usedBytes < 0 {
		s.usedBytes = 0
	}
}

func (s *Store) pathForName(name string) (string, error) {
	if name == "" || filepath.Base(name) != name || strings.ContainsAny(name, `/\`) {
		return "", ErrInvalidName
	}
	path := filepath.Join(s.root, name)
	if filepath.Dir(path) != s.root {
		return "", ErrInvalidName
	}
	return path, nil
}

func scanRegularFiles(root string) (int64, map[string]int64, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return 0, nil, fmt.Errorf("mountscratch: scan root: %w", err)
	}
	total := int64(0)
	tracked := make(map[string]int64)
	for _, entry := range entries {
		if !entry.Type().IsRegular() {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return 0, nil, fmt.Errorf("mountscratch: inspect file: %w", err)
		}
		if info.Size() < 0 || total > math.MaxInt64-info.Size() {
			return 0, nil, ErrQuotaExceeded
		}
		total += info.Size()
		tracked[entry.Name()] = info.Size()
	}
	return total, tracked, nil
}
