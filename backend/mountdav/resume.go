package mountdav

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	defaultResumeIdleTimeout                   = 2 * time.Minute
	defaultMaxResumeObjectBytes                = 8 << 30
	defaultMaxResumeAggregateBytes             = 16 << 30
	resumeDirMode                  os.FileMode = 0o700
	resumeFileMode                 os.FileMode = 0o600
)

var (
	ErrResumeRangeInvalid   = errors.New("mountdav: invalid Content-Range")
	ErrResumeOffsetMismatch = errors.New("mountdav: Content-Range does not continue from the current content")
	ErrResumeTooLarge       = errors.New("mountdav: resumed upload exceeds the size limit")
)

// contentRange is a parsed "Content-Range: bytes start-end/total" PUT
// header. This is non-standard for PUT under HTTP/WebDAV, but it is real,
// observed behavior from macOS's built-in WebDAV client (mount_webdav),
// which uses it to resume an interrupted large-file upload: an initial
// plain PUT (no Content-Range) delivers whatever bytes it manages to send,
// then follow-up PUT(s) carry Content-Range to continue from where the
// previous attempt left off.
type contentRange struct {
	start, end, total int64
}

func (r contentRange) length() int64 { return r.end - r.start + 1 }
func (r contentRange) isFinal() bool { return r.end+1 == r.total }

// parseContentRange parses a PUT request's Content-Range header. An empty
// header is not an error: it reports hasRange=false so the caller takes the
// normal, non-resumed PUT path unchanged.
func parseContentRange(header string) (rng contentRange, hasRange bool, err error) {
	if header == "" {
		return contentRange{}, false, nil
	}
	const prefix = "bytes "
	if !strings.HasPrefix(header, prefix) {
		return contentRange{}, true, ErrResumeRangeInvalid
	}
	spec := strings.TrimPrefix(header, prefix)
	dash := strings.IndexByte(spec, '-')
	slash := strings.IndexByte(spec, '/')
	if dash <= 0 || slash <= dash {
		return contentRange{}, true, ErrResumeRangeInvalid
	}
	start, ok1 := parseNonNegativeInt64(spec[:dash])
	end, ok2 := parseNonNegativeInt64(spec[dash+1 : slash])
	total, ok3 := parseNonNegativeInt64(spec[slash+1:])
	if !ok1 || !ok2 || !ok3 || start > end || end >= total {
		return contentRange{}, true, ErrResumeRangeInvalid
	}
	return contentRange{start: start, end: end, total: total}, true, nil
}

func parseNonNegativeInt64(value string) (int64, bool) {
	if value == "" {
		return 0, false
	}
	for _, character := range value {
		if character < '0' || character > '9' {
			return 0, false
		}
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return 0, false
	}
	return parsed, true
}

// resumeEntry tracks one in-progress Content-Range PUT resume sequence for
// one resource path.
type resumeEntry struct {
	file        *os.File
	diskPath    string
	total       int64
	accumulated int64
	lockTokens  []string
	lastActive  time.Time
	busy        bool
}

// resumeStore accumulates Content-Range PUT chunks on disk, keyed by
// resource path, until a sequence is complete and ready to commit through
// the normal write coordinator. It never talks to the coordinator itself
// and mountwrite/mountadapter have no awareness this chunking happened.
type resumeStore struct {
	mu                sync.Mutex
	root              string
	entries           map[string]*resumeEntry
	idleTimeout       time.Duration
	maxObjectBytes    int64
	maxAggregateBytes int64
	usedBytes         int64
	serial            uint64
	now               func() time.Time
}

func newResumeStore(root string, idleTimeout time.Duration, maxObjectBytes, maxAggregateBytes int64) (*resumeStore, error) {
	if root == "" || idleTimeout <= 0 || maxObjectBytes <= 0 || maxAggregateBytes <= 0 {
		return nil, fmt.Errorf("mountdav: invalid resume store configuration")
	}
	if err := os.MkdirAll(root, resumeDirMode); err != nil {
		return nil, fmt.Errorf("mountdav: create resume root: %w", err)
	}
	return &resumeStore{
		root:              root,
		entries:           make(map[string]*resumeEntry),
		idleTimeout:       idleTimeout,
		maxObjectBytes:    maxObjectBytes,
		maxAggregateBytes: maxAggregateBytes,
		now:               time.Now,
	}, nil
}

// newResumeStoreInTemp creates a resumeStore rooted in a fresh, private
// system-temp-directory scratch space, following this codebase's existing
// os.MkdirTemp convention for scratch directories (see e.g.
// backend/services/file/import.go, backend/media/video_thumbnails.go).
func newResumeStoreInTemp() (*resumeStore, error) {
	root, err := os.MkdirTemp("", "tdrive-mountdav-resume-*")
	if err != nil {
		return nil, fmt.Errorf("mountdav: create resume scratch directory: %w", err)
	}
	store, err := newResumeStore(root, defaultResumeIdleTimeout, defaultMaxResumeObjectBytes, defaultMaxResumeAggregateBytes)
	if err != nil {
		_ = os.RemoveAll(root)
		return nil, err
	}
	return store, nil
}

// Close discards every in-progress sequence and removes the store's on-disk
// root. Safe to call once, from Server.Stop.
func (s *resumeStore) Close() error {
	s.mu.Lock()
	entries := s.entries
	s.entries = nil
	s.mu.Unlock()
	for _, entry := range entries {
		_ = entry.file.Close()
	}
	return os.RemoveAll(s.root)
}

// appendResumeChunk validates and applies rng's bytes (read from source) to
// path's in-progress sequence, creating or restarting it as needed.
// openCurrent opens the currently committed content at path so a freshly
// (re)started sequence can be seeded with it; it must return (nil, 0, nil)
// when nothing is committed at path yet, not an error.
//
// It returns the fully assembled, rewound file once the final chunk
// completes the sequence (end+1 == total). The caller then owns that file
// and is responsible for closing and removing it (see closeAndRemoveResumeFile)
// exactly once, regardless of what it does with the content.
func (s *resumeStore) appendResumeChunk(
	path string,
	rng contentRange,
	lockTokens []string,
	openCurrent func() (io.ReadCloser, int64, error),
	source io.Reader,
) (assembled *os.File, complete bool, err error) {
	entry, err := s.claim(path, rng, lockTokens, openCurrent)
	if err != nil {
		return nil, false, err
	}

	limited := io.LimitReader(source, rng.length())
	written, copyErr := io.Copy(entry.file, limited)
	if copyErr == nil && written != rng.length() {
		copyErr = ErrResumeOffsetMismatch
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if copyErr != nil {
		slog.Warn("mountdav: resume chunk copy failed, discarding sequence", "path", path, "start", rng.start, "end", rng.end, "error", copyErr)
		s.discardLocked(path, entry)
		return nil, false, copyErr
	}
	entry.accumulated += written
	entry.lastActive = s.now()
	entry.busy = false

	if !rng.isFinal() || entry.accumulated != entry.total {
		slog.Debug("mountdav: resume chunk accepted, sequence still incomplete", "path", path, "accumulated", entry.accumulated, "total", entry.total)
		return nil, false, nil
	}
	if !s.removeIfCurrentLocked(path, entry) {
		// A racing, conflicting-total claim for this path discarded this
		// entry while its final chunk's body was being copied. This
		// goroutine's own file is not the live sequence anymore and there
		// is nothing left to correctly commit.
		slog.Warn("mountdav: resume sequence superseded by a conflicting claim before its final chunk committed", "path", path, "total", entry.total)
		_ = entry.file.Close()
		_ = os.Remove(entry.diskPath)
		return nil, false, ErrResumeOffsetMismatch
	}
	if _, err := entry.file.Seek(0, io.SeekStart); err != nil {
		slog.Error("mountdav: failed to rewind assembled resume file", "path", path, "total", entry.total, "error", err)
		_ = entry.file.Close()
		_ = os.Remove(entry.diskPath)
		return nil, false, fmt.Errorf("mountdav: rewind assembled resume file: %w", err)
	}
	slog.Info("mountdav: resume sequence complete, assembled content ready to commit", "path", path, "total", entry.total)
	return entry.file, true, nil
}

// claim resolves path's entry for rng, creating or restarting it if needed,
// and marks it busy so a concurrent chunk for the same path is rejected
// instead of corrupting the accumulator. Everything here runs under the
// store lock except opening/copying the seed (openCurrent reads local disk,
// not the network), matching this session's earlier fix elsewhere in this
// codebase for never mutating shared state without holding its lock.
func (s *resumeStore) claim(
	path string,
	rng contentRange,
	lockTokens []string,
	openCurrent func() (io.ReadCloser, int64, error),
) (*resumeEntry, error) {
	s.mu.Lock()
	s.reapLocked()
	if entry, exists := s.entries[path]; exists {
		if entry.total != rng.total || !slices.Equal(entry.lockTokens, lockTokens) {
			s.discardLocked(path, entry)
		} else if entry.busy {
			s.mu.Unlock()
			return nil, ErrResumeOffsetMismatch
		} else if rng.start != entry.accumulated {
			s.mu.Unlock()
			return nil, ErrResumeOffsetMismatch
		} else if rng.total > s.maxObjectBytes {
			s.mu.Unlock()
			return nil, ErrResumeTooLarge
		} else {
			entry.busy = true
			s.mu.Unlock()
			return entry, nil
		}
	}
	s.mu.Unlock()

	current, size, err := openCurrent()
	if err != nil {
		return nil, err
	}
	defer func() {
		if current != nil {
			_ = current.Close()
		}
	}()
	if rng.start != size {
		return nil, ErrResumeOffsetMismatch
	}
	if rng.total > s.maxObjectBytes {
		return nil, ErrResumeTooLarge
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.reapLocked()
	if existing, raced := s.entries[path]; raced {
		if existing.busy || existing.total != rng.total || !slices.Equal(existing.lockTokens, lockTokens) || rng.start != existing.accumulated {
			return nil, ErrResumeOffsetMismatch
		}
		existing.busy = true
		return existing, nil
	}
	if s.usedBytes+rng.total > s.maxAggregateBytes {
		return nil, ErrResumeTooLarge
	}
	created, err := s.createLocked(path, rng.total, lockTokens, current, size)
	if err != nil {
		return nil, err
	}
	created.busy = true
	return created, nil
}

func (s *resumeStore) createLocked(path string, total int64, lockTokens []string, seed io.Reader, seedSize int64) (*resumeEntry, error) {
	s.serial++
	diskPath := filepath.Join(s.root, strconv.FormatUint(s.serial, 10)+".resume")
	file, err := os.OpenFile(diskPath, os.O_RDWR|os.O_CREATE|os.O_EXCL, resumeFileMode)
	if err != nil {
		return nil, fmt.Errorf("mountdav: create resume buffer: %w", err)
	}
	if seed != nil && seedSize > 0 {
		if _, err := io.CopyN(file, seed, seedSize); err != nil {
			_ = file.Close()
			_ = os.Remove(diskPath)
			return nil, fmt.Errorf("mountdav: seed resume buffer: %w", err)
		}
	}
	entry := &resumeEntry{
		file:        file,
		diskPath:    diskPath,
		total:       total,
		accumulated: seedSize,
		lockTokens:  slices.Clone(lockTokens),
		lastActive:  s.now(),
	}
	s.entries[path] = entry
	s.usedBytes += total
	slog.Debug("mountdav: resume sequence started", "path", path, "total", total, "seed_size", seedSize)
	return entry, nil
}

// removeIfCurrentLocked removes entry from the map and releases its quota
// reservation only if it is still the live entry for path. A caller that
// captured entry before unlocking (to run the unlocked io.Copy of a chunk's
// body) may find, once it relocks, that a racing, conflicting-total claim
// for the same path has already discarded and replaced it — in that case the
// live entry (and its own accounting) belongs to that other sequence and
// must not be touched here, so this reports false instead of deleting
// whatever now happens to be at that key.
func (s *resumeStore) removeIfCurrentLocked(path string, entry *resumeEntry) bool {
	if current, exists := s.entries[path]; !exists || current != entry {
		return false
	}
	delete(s.entries, path)
	s.usedBytes -= entry.total
	return true
}

func (s *resumeStore) discardLocked(path string, entry *resumeEntry) {
	if entry == nil {
		return
	}
	slog.Debug("mountdav: discarding resume sequence", "path", path, "accumulated", entry.accumulated, "total", entry.total)
	s.removeIfCurrentLocked(path, entry)
	_ = entry.file.Close()
	_ = os.Remove(entry.diskPath)
}

// reapLocked drops sequences that have been idle past idleTimeout so an
// abandoned resume (client vanished mid-sequence, network died, no final
// chunk ever arrives) does not hold its temp file and quota reservation
// forever. Mirrors boundedLockSystem.collectExpired's lazy, no-background-
// goroutine approach: reaping happens as a side effect of the next access,
// backstopped by resumeStore.Close removing the whole root on server Stop.
func (s *resumeStore) reapLocked() {
	now := s.now()
	for path, entry := range s.entries {
		if entry.busy {
			continue
		}
		if now.Sub(entry.lastActive) >= s.idleTimeout {
			s.discardLocked(path, entry)
		}
	}
}

func closeAndRemoveResumeFile(file *os.File) {
	if file == nil {
		return
	}
	name := file.Name()
	_ = file.Close()
	_ = os.Remove(name)
}
