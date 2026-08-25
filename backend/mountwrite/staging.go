package mountwrite

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"

	tdcrypto "TDrive/backend/crypto"
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

func (s *DiskStagingStore) Stage(ctx context.Context, request StageRequest, source io.Reader) (StagedObject, error) {
	if ctx == nil || source == nil || request.OperationID == "" || !validOperationID(request.OperationID) ||
		request.PlaintextSize < -1 || request.MaxBytes < 0 || !validEncryptionVersion(request.EncryptionVersion) {
		return StagedObject{}, ErrInvalidRequest
	}
	if request.EncryptionVersion == EncryptionNone && len(request.MasterKey) != 0 {
		return StagedObject{}, ErrInvalidRequest
	}
	if request.EncryptionVersion == EncryptionTDE1 && (request.PlaintextSize < 0 || len(request.MasterKey) != masterKeySize) {
		return StagedObject{}, ErrInvalidRequest
	}

	objectLimit := s.maxObjectBytes
	if request.MaxBytes > 0 && request.MaxBytes < objectLimit {
		objectLimit = request.MaxBytes
	}
	if request.PlaintextSize > objectLimit {
		return StagedObject{}, ErrTooLarge
	}
	if request.EncryptionVersion == EncryptionTDE1 {
		storedSize := tdcrypto.CiphertextSize(request.PlaintextSize)
		if storedSize == math.MaxInt64 || storedSize <= request.PlaintextSize || storedSize > objectLimit {
			return StagedObject{}, ErrTooLarge
		}
	}
	if err := s.acquireSlot(ctx); err != nil {
		return StagedObject{}, err
	}
	defer s.releaseSlot()

	key := stagingKey(request.OperationID)
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

	staged, reserved, stageErr := s.copyToStage(ctx, file, key, path, request, objectLimit, source)
	closeErr := file.Close()
	if stageErr == nil && closeErr != nil {
		stageErr = fmt.Errorf("close staged file: %w", closeErr)
	}
	if stageErr != nil {
		removeErr := os.Remove(path)
		if removeErr == nil || errors.Is(removeErr, os.ErrNotExist) {
			s.releaseBytes(reserved)
			return StagedObject{}, stageErr
		}
		// Keep the reservation tracked when cleanup fails so aggregate quota
		// cannot be bypassed. Coordinator recovery retries by operation ID.
		s.trackFile(key, reserved)
		stageErr = errors.Join(stageErr, fmt.Errorf("remove partial staged file: %w", removeErr))
		return StagedObject{}, stageErr
	}
	s.trackFile(key, staged.StoredSize)
	return staged, nil
}

func (s *DiskStagingStore) copyToStage(
	ctx context.Context,
	file *os.File,
	key, path string,
	request StageRequest,
	objectLimit int64,
	source io.Reader,
) (StagedObject, int64, error) {
	if request.EncryptionVersion == EncryptionTDE1 {
		return s.copyEncryptedToStage(ctx, file, key, path, request, source)
	}
	return s.copyPlaintextToStage(ctx, file, key, path, request.PlaintextSize, objectLimit, source)
}

func (s *DiskStagingStore) copyPlaintextToStage(
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
			increment := int64(readCount)
			if increment > objectLimit || size > objectLimit-increment {
				return StagedObject{}, reserved, ErrTooLarge
			}
			nextSize := size + increment
			if err := s.reserveBytes(increment); err != nil {
				return StagedObject{}, reserved, err
			}
			reserved += increment
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
	if err := ctx.Err(); err != nil {
		return StagedObject{}, reserved, ErrCanceled
	}
	if err := file.Sync(); err != nil {
		return StagedObject{}, reserved, fmt.Errorf("sync staged file: %w", err)
	}
	digest := [sha256.Size]byte{}
	copy(digest[:], hasher.Sum(nil))
	return StagedObject{
		Key:           key,
		Path:          path,
		PlaintextSize: size,
		StoredSize:    size,
		SHA256:        digest,
		StoredSHA256:  digest,
	}, reserved, nil
}

func (s *DiskStagingStore) copyEncryptedToStage(
	ctx context.Context,
	file *os.File,
	key, path string,
	request StageRequest,
	source io.Reader,
) (StagedObject, int64, error) {
	storedSize := tdcrypto.CiphertextSize(request.PlaintextSize)
	if storedSize == math.MaxInt64 || storedSize <= request.PlaintextSize {
		return StagedObject{}, 0, ErrTooLarge
	}
	if err := s.reserveBytes(storedSize); err != nil {
		return StagedObject{}, 0, err
	}

	privateKey := append([]byte(nil), request.MasterKey...)
	defer clearBytes(privateKey)
	exactSource := &exactLengthReader{
		ctx:       ctx,
		source:    source,
		remaining: request.PlaintextSize,
	}
	storedHasher := sha256.New()
	writer := &stageWriter{
		ctx:    ctx,
		file:   file,
		hasher: storedHasher,
		limit:  storedSize,
	}
	if err := tdcrypto.EncryptStream(exactSource, writer, privateKey, request.PlaintextSize); err != nil {
		return StagedObject{}, storedSize, classifyStageStreamError(key, err)
	}
	if writer.written != storedSize || exactSource.remaining != 0 {
		return StagedObject{}, storedSize, ErrLengthMismatch
	}
	if err := ctx.Err(); err != nil {
		return StagedObject{}, storedSize, ErrCanceled
	}
	if err := file.Sync(); err != nil {
		return StagedObject{}, storedSize, fmt.Errorf("sync staged file: %w", err)
	}
	digest := [sha256.Size]byte{}
	copy(digest[:], storedHasher.Sum(nil))
	return StagedObject{
		Key:               key,
		Path:              path,
		PlaintextSize:     request.PlaintextSize,
		StoredSize:        storedSize,
		StoredSHA256:      digest,
		EncryptionVersion: EncryptionTDE1,
	}, storedSize, nil
}

func (s *DiskStagingStore) Open(staged StagedObject) (ReadSeekCloser, error) {
	if err := validateStagedObject(staged); err != nil {
		return nil, err
	}
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
	if !info.Mode().IsRegular() || info.Size() != staged.StoredSize {
		_ = file.Close()
		return nil, ErrInvalidRequest
	}
	if staged.StoredSHA256 != ([sha256.Size]byte{}) {
		hasher := sha256.New()
		if _, err := io.CopyBuffer(hasher, file, make([]byte, stagingBufferSize)); err != nil {
			_ = file.Close()
			return nil, fmt.Errorf("verify staged file: %w", err)
		}
		digest := [sha256.Size]byte{}
		copy(digest[:], hasher.Sum(nil))
		if digest != staged.StoredSHA256 {
			_ = file.Close()
			return nil, ErrInvalidRequest
		}
		if _, err := file.Seek(0, io.SeekStart); err != nil {
			_ = file.Close()
			return nil, fmt.Errorf("rewind staged file: %w", err)
		}
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
		if info.Size() < 0 || total > math.MaxInt64-info.Size() {
			return 0, nil, ErrQuotaExceeded
		}
		total += info.Size()
		tracked[entry.Name()] = info.Size()
	}
	return total, tracked, nil
}

type exactLengthReader struct {
	ctx        context.Context
	source     io.Reader
	remaining  int64
	emptyReads int
}

func (r *exactLengthReader) Read(buffer []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, ErrCanceled
	}
	if len(buffer) == 0 {
		return 0, nil
	}
	if r.remaining == 0 {
		var extra [1]byte
		readCount, err := r.source.Read(extra[:])
		if readCount > 0 {
			return 0, ErrLengthMismatch
		}
		if errors.Is(err, io.EOF) {
			return 0, io.EOF
		}
		if err != nil {
			return 0, err
		}
		r.emptyReads++
		if r.emptyReads >= 100 {
			return 0, io.ErrNoProgress
		}
		return 0, nil
	}
	if int64(len(buffer)) > r.remaining {
		buffer = buffer[:r.remaining]
	}
	readCount, err := r.source.Read(buffer)
	if readCount < 0 || int64(readCount) > r.remaining || readCount > len(buffer) {
		return 0, ErrLengthMismatch
	}
	if readCount > 0 {
		r.emptyReads = 0
		r.remaining -= int64(readCount)
	}
	if errors.Is(err, io.EOF) {
		if r.remaining != 0 {
			return readCount, ErrLengthMismatch
		}
		return readCount, io.EOF
	}
	if err != nil {
		return readCount, err
	}
	if readCount == 0 {
		r.emptyReads++
		if r.emptyReads >= 100 {
			return 0, io.ErrNoProgress
		}
	}
	return readCount, nil
}

type stageWriter struct {
	ctx     context.Context
	file    *os.File
	hasher  hash.Hash
	limit   int64
	written int64
}

func (w *stageWriter) Write(data []byte) (int, error) {
	if err := w.ctx.Err(); err != nil {
		return 0, ErrCanceled
	}
	if int64(len(data)) > w.limit-w.written {
		return 0, ErrTooLarge
	}
	if err := writeAll(w.file, data); err != nil {
		return 0, err
	}
	_, _ = w.hasher.Write(data)
	w.written += int64(len(data))
	return len(data), nil
}

func classifyStageStreamError(operationID string, err error) error {
	switch {
	case errors.Is(err, ErrCanceled), errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return ErrCanceled
	case errors.Is(err, ErrLengthMismatch):
		return ErrLengthMismatch
	case errors.Is(err, ErrTooLarge):
		return ErrTooLarge
	default:
		return newOperationError(operationID, MutationPut, err)
	}
}

func validateStagedObject(staged StagedObject) error {
	if staged.Key == "" || staged.PlaintextSize < 0 || staged.StoredSize < 0 || !validEncryptionVersion(staged.EncryptionVersion) {
		return ErrInvalidRequest
	}
	if staged.EncryptionVersion == EncryptionNone {
		if staged.PlaintextSize != staged.StoredSize {
			return ErrInvalidRequest
		}
		return nil
	}
	if err := tdcrypto.ValidatePlaintextSize(staged.PlaintextSize); err != nil {
		return ErrInvalidRequest
	}
	if staged.SHA256 != ([sha256.Size]byte{}) || staged.StoredSHA256 == ([sha256.Size]byte{}) ||
		tdcrypto.CiphertextSize(staged.PlaintextSize) != staged.StoredSize {
		return ErrInvalidRequest
	}
	return nil
}

func clearBytes(value []byte) {
	for index := range value {
		value[index] = 0
	}
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
