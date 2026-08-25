package mountwrite

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

const (
	privateDirMode    os.FileMode = 0o700
	privateFileMode   os.FileMode = 0o600
	stagingBufferSize             = 256 * 1024
)

type DiskStagingConfig struct {
	Root              string
	MaxObjectBytes    int64
	MaxAggregateBytes int64
	MaxConcurrent     int
}

type DiskStagingStore struct {
	root           string
	maxObjectBytes int64
	maxBytes       int64
	slots          chan struct{}
	mu             sync.Mutex
	usedBytes      int64
	trackedBytes   map[string]int64
}

// NewDiskStagingStore creates a private, quota-bounded staging store and
// accounts for files left by an earlier process.
func NewDiskStagingStore(config DiskStagingConfig) (*DiskStagingStore, error) {
	if config.Root == "" || config.MaxObjectBytes <= 0 || config.MaxAggregateBytes <= 0 || config.MaxConcurrent <= 0 {
		return nil, ErrInvalidRequest
	}
	root, err := filepath.Abs(config.Root)
	if err != nil {
		return nil, fmt.Errorf("resolve staging root: %w", err)
	}
	if err := os.MkdirAll(root, privateDirMode); err != nil {
		return nil, fmt.Errorf("create staging root: %w", err)
	}
	if err := os.Chmod(root, privateDirMode); err != nil {
		return nil, fmt.Errorf("protect staging root: %w", err)
	}
	usedBytes, trackedBytes, err := scanRegularFiles(root)
	if err != nil {
		return nil, err
	}
	return &DiskStagingStore{
		root:           root,
		maxObjectBytes: config.MaxObjectBytes,
		maxBytes:       config.MaxAggregateBytes,
		slots:          make(chan struct{}, config.MaxConcurrent),
		usedBytes:      usedBytes,
		trackedBytes:   trackedBytes,
	}, nil
}

func (s *DiskStagingStore) Root() string {
	return s.root
}

func (s *DiskStagingStore) UsedBytes() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.usedBytes
}

func (s *DiskStagingStore) Stage(ctx context.Context, operationID string, contentLength, maxBytes int64, source io.Reader) (StagedObject, error) {
	if operationID == "" || source == nil || contentLength < -1 || maxBytes < 0 {
		return StagedObject{}, ErrInvalidRequest
	}
	objectLimit := s.maxObjectBytes
	if maxBytes > 0 && maxBytes < objectLimit {
		objectLimit = maxBytes
	}
	if contentLength > objectLimit {
		return StagedObject{}, ErrTooLarge
	}
	if err := s.acquireSlot(ctx); err != nil {
		return StagedObject{}, err
	}
	defer s.releaseSlot()

	key := stagingKey(operationID)
	path, err := s.pathForKey(key)
	if err != nil {
		return StagedObject{}, err
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, privateFileMode)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return StagedObject{}, ErrOperationExists
		}
		return StagedObject{}, fmt.Errorf("create staged file: %w", err)
	}

	staged, reserved, stageErr := s.copyToStage(ctx, file, key, path, contentLength, objectLimit, source)
	closeErr := file.Close()
	if stageErr == nil && closeErr != nil {
		stageErr = fmt.Errorf("close staged file: %w", closeErr)
	}
	if stageErr != nil {
		_ = os.Remove(path)
		s.releaseBytes(reserved)
		return StagedObject{}, stageErr
	}
	s.trackFile(key, staged.Size)
	return staged, nil
}

func (s *DiskStagingStore) copyToStage(
	ctx context.Context,
	file *os.File,
	key, path string,
	contentLength, objectLimit int64,
	source io.Reader,
) (StagedObject, int64, error) {
	hasher := sha256.New()
	buffer := make([]byte, stagingBufferSize)
	var size int64
	var reserved int64
	emptyReads := 0
	for {
		if err := ctx.Err(); err != nil {
			return StagedObject{}, reserved, ErrCanceled
		}
		readCount, readErr := source.Read(buffer)
		if readCount > 0 {
			emptyReads = 0
			nextSize := size + int64(readCount)
			if nextSize > objectLimit {
				return StagedObject{}, reserved, ErrTooLarge
			}
			if err := s.reserveBytes(int64(readCount)); err != nil {
				return StagedObject{}, reserved, err
			}
			reserved += int64(readCount)
			if err := writeAll(file, buffer[:readCount]); err != nil {
				return StagedObject{}, reserved, fmt.Errorf("write staged file: %w", err)
			}
			_, _ = hasher.Write(buffer[:readCount])
			size = nextSize
		}
		if readErr != nil {
			if errors.Is(readErr, io.EOF) {
				break
			}
			return StagedObject{}, reserved, newOperationError(key, MutationPut, readErr)
		}
		if readCount == 0 {
			emptyReads++
			if emptyReads >= 100 {
				return StagedObject{}, reserved, newOperationError(key, MutationPut, io.ErrNoProgress)
			}
			continue
		}
	}
	if contentLength >= 0 && size != contentLength {
		return StagedObject{}, reserved, ErrLengthMismatch
	}
	if err := file.Sync(); err != nil {
		return StagedObject{}, reserved, fmt.Errorf("sync staged file: %w", err)
	}
	digest := [sha256.Size]byte{}
	copy(digest[:], hasher.Sum(nil))
	return StagedObject{Key: key, Path: path, Size: size, SHA256: digest}, reserved, nil
}

func (s *DiskStagingStore) Open(staged StagedObject) (ReadSeekCloser, error) {
	path, err := s.pathForKey(staged.Key)
	if err != nil {
		return nil, err
	}
	if staged.Path != "" {
		candidate, err := filepath.Abs(staged.Path)
		if err != nil || candidate != path {
			return nil, ErrInvalidRequest
		}
	}
	file, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("open staged file: %w", err)
	}
	info, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("inspect staged file: %w", err)
	}
	if !info.Mode().IsRegular() || info.Size() != staged.Size {
		_ = file.Close()
		return nil, ErrInvalidRequest
	}
	return file, nil
}

func (s *DiskStagingStore) Remove(ctx context.Context, staged StagedObject) error {
	if err := ctx.Err(); err != nil {
		return ErrCanceled
	}
	path, err := s.pathForKey(staged.Key)
	if err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("stat staged file: %w", err)
	}
	if !info.Mode().IsRegular() {
		return ErrInvalidRequest
	}
	if err := os.Remove(path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("remove staged file: %w", err)
	}
	s.releaseFile(staged.Key, info.Size())
	return nil
}

func (s *DiskStagingStore) RemoveOperation(ctx context.Context, operationID string) error {
	if operationID == "" {
		return ErrInvalidRequest
	}
	key := stagingKey(operationID)
	path, err := s.pathForKey(key)
	if err != nil {
		return err
	}
	return s.Remove(ctx, StagedObject{Key: key, Path: path})
}

func (s *DiskStagingStore) acquireSlot(ctx context.Context) error {
	select {
	case s.slots <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ErrCanceled
	}
}

func (s *DiskStagingStore) releaseSlot() {
	<-s.slots
}

func (s *DiskStagingStore) reserveBytes(size int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if size < 0 || s.usedBytes > s.maxBytes-size {
		return ErrQuotaExceeded
	}
	s.usedBytes += size
	return nil
}

func (s *DiskStagingStore) releaseBytes(size int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.usedBytes -= size
	if s.usedBytes < 0 {
		s.usedBytes = 0
	}
}

func (s *DiskStagingStore) trackFile(key string, size int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.trackedBytes[key] = size
}

func (s *DiskStagingStore) releaseFile(key string, fallbackSize int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	size, found := s.trackedBytes[key]
	if !found {
		size = fallbackSize
	}
	delete(s.trackedBytes, key)
	s.usedBytes -= size
	if s.usedBytes < 0 {
		s.usedBytes = 0
	}
}

func (s *DiskStagingStore) pathForKey(key string) (string, error) {
	if key == "" || filepath.Base(key) != key || strings.ContainsAny(key, `/\\`) {
		return "", ErrInvalidRequest
	}
	path := filepath.Join(s.root, key)
	if filepath.Dir(path) != s.root {
		return "", ErrInvalidRequest
	}
	return path, nil
}

func stagingKey(operationID string) string {
	digest := sha256.Sum256([]byte(operationID))
	return hex.EncodeToString(digest[:]) + ".stage"
}

func scanRegularFiles(root string) (int64, map[string]int64, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return 0, nil, fmt.Errorf("scan staging root: %w", err)
	}
	var total int64
	tracked := make(map[string]int64)
	for _, entry := range entries {
		if !entry.Type().IsRegular() {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return 0, nil, fmt.Errorf("inspect staged file: %w", err)
		}
		total += info.Size()
		tracked[entry.Name()] = info.Size()
	}
	return total, tracked, nil
}

func writeAll(writer io.Writer, data []byte) error {
	for len(data) > 0 {
		written, err := writer.Write(data)
		if err != nil {
			return err
		}
		if written == 0 {
			return io.ErrShortWrite
		}
		data = data[written:]
	}
	return nil
}

var _ StagingStore = (*DiskStagingStore)(nil)
