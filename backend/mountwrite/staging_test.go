package mountwrite

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"
)

func TestDiskStagingStoreStreamsHashesAndUsesPrivatePermissions(t *testing.T) {
	t.Parallel()

	root := filepath.Join(t.TempDir(), "staging")
	store, err := NewDiskStagingStore(DiskStagingConfig{
		Root:              root,
		MaxObjectBytes:    1 << 20,
		MaxAggregateBytes: 2 << 20,
		MaxConcurrent:     2,
	})
	if err != nil {
		t.Fatalf("new store: %v", err)
	}

	payload := bytes.Repeat([]byte("tdrive"), 4096)
	staged, err := store.Stage(context.Background(), plaintextStageRequest("../../unsafe-operation-id", int64(len(payload)), int64(len(payload))), bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("stage: %v", err)
	}
	t.Cleanup(func() { _ = store.Remove(context.Background(), staged) })

	wantHash := sha256.Sum256(payload)
	if staged.PlaintextSize != int64(len(payload)) || staged.StoredSize != int64(len(payload)) || staged.SHA256 != wantHash {
		t.Fatalf("staged metadata = plaintext %d stored %d hash %x", staged.PlaintextSize, staged.StoredSize, staged.SHA256)
	}
	if filepath.Dir(staged.Path) != root {
		t.Fatalf("staged path escaped root: %q", staged.Path)
	}
	if filepath.Base(staged.Path) == "unsafe-operation-id" {
		t.Fatalf("operation ID should not be used directly as a filename: %q", staged.Path)
	}

	reader, err := store.Open(staged)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	got, err := io.ReadAll(reader)
	closeErr := reader.Close()
	if err != nil || closeErr != nil {
		t.Fatalf("read staged: read=%v close=%v", err, closeErr)
	}
	if !bytes.Equal(got, payload) {
		t.Fatal("staged payload differs")
	}

	if runtime.GOOS != "windows" {
		assertFileMode(t, root, 0o700)
		assertFileMode(t, staged.Path, 0o600)
	}
}

func TestDiskStagingStoreEnforcesObjectAndAggregateQuota(t *testing.T) {
	t.Parallel()

	store, err := NewDiskStagingStore(DiskStagingConfig{
		Root:              filepath.Join(t.TempDir(), "staging"),
		MaxObjectBytes:    8,
		MaxAggregateBytes: 10,
		MaxConcurrent:     1,
	})
	if err != nil {
		t.Fatalf("new store: %v", err)
	}

	if _, err := store.Stage(context.Background(), plaintextStageRequest("too-big", -1, 0), bytes.NewReader([]byte("123456789"))); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("object limit error = %v, want ErrTooLarge", err)
	}
	if entries, err := os.ReadDir(store.Root()); err != nil || len(entries) != 0 {
		t.Fatalf("failed stage left data: entries=%v err=%v", entries, err)
	}

	first, err := store.Stage(context.Background(), plaintextStageRequest("first", 7, 0), bytes.NewReader([]byte("1234567")))
	if err != nil {
		t.Fatalf("first stage: %v", err)
	}
	if _, err := store.Stage(context.Background(), plaintextStageRequest("second", 4, 0), bytes.NewReader([]byte("1234"))); !errors.Is(err, ErrQuotaExceeded) {
		t.Fatalf("aggregate limit error = %v, want ErrQuotaExceeded", err)
	}
	if err := store.Remove(context.Background(), first); err != nil {
		t.Fatalf("remove first: %v", err)
	}
	if _, err := store.Stage(context.Background(), plaintextStageRequest("second", 4, 0), bytes.NewReader([]byte("1234"))); err != nil {
		t.Fatalf("quota was not released: %v", err)
	}
}

func TestDiskStagingStoreRejectsLengthMismatchAndCleansPartialFile(t *testing.T) {
	t.Parallel()

	store, err := NewDiskStagingStore(DiskStagingConfig{
		Root:              filepath.Join(t.TempDir(), "staging"),
		MaxObjectBytes:    1024,
		MaxAggregateBytes: 1024,
		MaxConcurrent:     1,
	})
	if err != nil {
		t.Fatalf("new store: %v", err)
	}

	_, err = store.Stage(context.Background(), plaintextStageRequest("wrong-length", 8, 0), bytes.NewReader([]byte("short")))
	if !errors.Is(err, ErrLengthMismatch) {
		t.Fatalf("stage error = %v, want ErrLengthMismatch", err)
	}
	entries, readErr := os.ReadDir(store.Root())
	if readErr != nil || len(entries) != 0 {
		t.Fatalf("length mismatch left data: entries=%v err=%v", entries, readErr)
	}
}

func TestDiskStagingStoreHonorsCancellationWhileWaitingForSlot(t *testing.T) {
	t.Parallel()

	store, err := NewDiskStagingStore(DiskStagingConfig{
		Root:              filepath.Join(t.TempDir(), "staging"),
		MaxObjectBytes:    1024,
		MaxAggregateBytes: 2048,
		MaxConcurrent:     1,
	})
	if err != nil {
		t.Fatalf("new store: %v", err)
	}

	blocking := newBlockingReader()
	firstDone := make(chan error, 1)
	go func() {
		_, stageErr := store.Stage(context.Background(), plaintextStageRequest("first", -1, 0), blocking)
		firstDone <- stageErr
	}()
	blocking.waitUntilRead(t)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	_, err = store.Stage(ctx, plaintextStageRequest("second", -1, 0), bytes.NewReader(nil))
	if !errors.Is(err, ErrCanceled) {
		t.Fatalf("waiting stage error = %v, want ErrCanceled", err)
	}

	blocking.unblock()
	if err := <-firstDone; err != nil {
		t.Fatalf("first stage: %v", err)
	}
}

func TestDiskStagingStoreCountsExistingFilesOnStartup(t *testing.T) {
	t.Parallel()

	root := filepath.Join(t.TempDir(), "staging")
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "existing.stage"), []byte("1234567"), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}

	store, err := NewDiskStagingStore(DiskStagingConfig{
		Root:              root,
		MaxObjectBytes:    10,
		MaxAggregateBytes: 10,
		MaxConcurrent:     1,
	})
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	if _, err := store.Stage(context.Background(), plaintextStageRequest("new", 4, 0), bytes.NewReader([]byte("1234"))); !errors.Is(err, ErrQuotaExceeded) {
		t.Fatalf("startup quota error = %v, want ErrQuotaExceeded", err)
	}
}

func TestDiskStagingStoreValidatesConfigurationAndPerRequestLimit(t *testing.T) {
	t.Parallel()

	root := filepath.Join(t.TempDir(), "staging")
	invalid := []DiskStagingConfig{
		{},
		{Root: root, MaxObjectBytes: 0, MaxAggregateBytes: 1, MaxConcurrent: 1},
		{Root: root, MaxObjectBytes: 1, MaxAggregateBytes: 0, MaxConcurrent: 1},
		{Root: root, MaxObjectBytes: 1, MaxAggregateBytes: 1, MaxConcurrent: 0},
	}
	for _, config := range invalid {
		if _, err := NewDiskStagingStore(config); !errors.Is(err, ErrInvalidRequest) {
			t.Fatalf("invalid config %#v error = %v", config, err)
		}
	}
	store, err := NewDiskStagingStore(DiskStagingConfig{Root: root, MaxObjectBytes: 100, MaxAggregateBytes: 100, MaxConcurrent: 1})
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	if _, err := store.Stage(context.Background(), plaintextStageRequest("per-request", 6, 5), bytes.NewReader([]byte("123456"))); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("per-request limit error = %v, want ErrTooLarge", err)
	}
	if _, err := store.Stage(context.Background(), plaintextStageRequest("invalid-length", -2, 0), bytes.NewReader(nil)); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("invalid length error = %v, want ErrInvalidRequest", err)
	}
}

func TestDiskStagingStoreRejectsCollisionCorruptionAndUnsafeReferences(t *testing.T) {
	t.Parallel()

	store, err := NewDiskStagingStore(DiskStagingConfig{
		Root:              filepath.Join(t.TempDir(), "staging"),
		MaxObjectBytes:    1024,
		MaxAggregateBytes: 2048,
		MaxConcurrent:     1,
	})
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	staged, err := store.Stage(context.Background(), plaintextStageRequest("collision", 4, 0), bytes.NewReader([]byte("data")))
	if err != nil {
		t.Fatalf("stage: %v", err)
	}
	if _, err := store.Stage(context.Background(), plaintextStageRequest("collision", 4, 0), bytes.NewReader([]byte("data"))); !errors.Is(err, ErrOperationExists) {
		t.Fatalf("collision error = %v, want ErrOperationExists", err)
	}

	unsafe := staged
	unsafe.Key = "../escape.stage"
	if _, err := store.Open(unsafe); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("unsafe open error = %v, want ErrInvalidRequest", err)
	}
	if err := store.Remove(context.Background(), unsafe); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("unsafe remove error = %v, want ErrInvalidRequest", err)
	}
	wrongPath := staged
	wrongPath.Path = filepath.Join(t.TempDir(), staged.Key)
	if _, err := store.Open(wrongPath); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("mismatched stage path error = %v, want ErrInvalidRequest", err)
	}
	missing := StagedObject{Key: "missing.stage", PlaintextSize: 1, StoredSize: 1}
	if _, err := store.Open(missing); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing open error = %v, want ErrNotFound", err)
	}
	if err := store.Remove(context.Background(), missing); err != nil {
		t.Fatalf("remove missing should be idempotent: %v", err)
	}

	if err := os.Truncate(staged.Path, 1); err != nil {
		t.Fatalf("corrupt stage: %v", err)
	}
	if _, err := store.Open(staged); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("corrupt open error = %v, want ErrInvalidRequest", err)
	}
	if err := store.Remove(context.Background(), staged); err != nil {
		t.Fatalf("remove corrupted stage: %v", err)
	}
	if err := store.Remove(context.Background(), staged); err != nil {
		t.Fatalf("repeat remove: %v", err)
	}
	if err := store.RemoveOperation(context.Background(), ""); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("empty operation cleanup error = %v, want ErrInvalidRequest", err)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := store.Remove(canceled, missing); !errors.Is(err, ErrCanceled) {
		t.Fatalf("canceled remove error = %v, want ErrCanceled", err)
	}
}

func TestDiskStagingStoreCleansReaderFailureAndNoProgress(t *testing.T) {
	t.Parallel()

	store, err := NewDiskStagingStore(DiskStagingConfig{
		Root:              filepath.Join(t.TempDir(), "staging"),
		MaxObjectBytes:    1024,
		MaxAggregateBytes: 2048,
		MaxConcurrent:     1,
	})
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	if _, err := store.Stage(context.Background(), plaintextStageRequest("reader-error", -1, 0), &errorReader{}); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("reader error = %v, want ErrUnavailable", err)
	}
	if _, err := store.Stage(context.Background(), plaintextStageRequest("no-progress", -1, 0), zeroReader{}); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("no-progress error = %v, want ErrUnavailable", err)
	}
	if store.UsedBytes() != 0 {
		t.Fatalf("failed readers retained %d bytes", store.UsedBytes())
	}
}

func assertFileMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %q: %v", path, err)
	}
	if got := info.Mode().Perm(); got != want {
		t.Fatalf("mode for %q = %#o, want %#o", path, got, want)
	}
}

type blockingReader struct {
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

type errorReader struct {
	read bool
}

func (r *errorReader) Read(buffer []byte) (int, error) {
	if !r.read {
		r.read = true
		copy(buffer, "partial")
		return len("partial"), errors.New("source failure")
	}
	return 0, io.EOF
}

type zeroReader struct{}

func (zeroReader) Read([]byte) (int, error) { return 0, nil }

func newBlockingReader() *blockingReader {
	return &blockingReader{started: make(chan struct{}), release: make(chan struct{})}
}

func (r *blockingReader) Read(_ []byte) (int, error) {
	r.once.Do(func() { close(r.started) })
	<-r.release
	return 0, io.EOF
}

func (r *blockingReader) waitUntilRead(t *testing.T) {
	t.Helper()
	select {
	case <-r.started:
	case <-time.After(time.Second):
		t.Fatal("reader was not started")
	}
}

func (r *blockingReader) unblock() {
	close(r.release)
}

func plaintextStageRequest(operationID string, plaintextSize, maxBytes int64) StageRequest {
	return StageRequest{OperationID: operationID, PlaintextSize: plaintextSize, MaxBytes: maxBytes}
}
