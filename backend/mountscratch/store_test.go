package mountscratch

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
)

func TestNewStoreSecuresRootAndAccountsExistingFiles(t *testing.T) {
	t.Parallel()

	root := filepath.Join(t.TempDir(), "scratch")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatalf("create root: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "existing.tmp"), []byte("data"), 0o600); err != nil {
		t.Fatalf("write existing file: %v", err)
	}
	if err := os.Mkdir(filepath.Join(root, "ignored"), 0o700); err != nil {
		t.Fatalf("create nested directory: %v", err)
	}

	store, err := NewStore(Config{Root: root, MaxBytes: 10, AccountExisting: true})
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	if store.Root() != root {
		t.Fatalf("root = %q, want %q", store.Root(), root)
	}
	if store.UsedBytes() != 4 {
		t.Fatalf("used bytes = %d, want 4", store.UsedBytes())
	}
	if runtime.GOOS != "windows" {
		info, statErr := os.Stat(root)
		if statErr != nil {
			t.Fatalf("stat root: %v", statErr)
		}
		if got := info.Mode().Perm(); got != 0o700 {
			t.Fatalf("root mode = %#o, want %#o", got, os.FileMode(0o700))
		}
	}
}

func TestStoreCreatesPrivateExclusiveFiles(t *testing.T) {
	t.Parallel()

	store, err := NewStore(Config{Root: filepath.Join(t.TempDir(), "scratch"), MaxBytes: 10})
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	file, err := store.CreateExclusive("upload.tmp", os.O_RDWR)
	if err != nil {
		t.Fatalf("create file: %v", err)
	}
	path := file.Name()
	if err := file.Close(); err != nil {
		t.Fatalf("close file: %v", err)
	}
	if path != filepath.Join(store.Root(), "upload.tmp") {
		t.Fatalf("file path = %q", path)
	}
	if runtime.GOOS != "windows" {
		info, statErr := os.Stat(path)
		if statErr != nil {
			t.Fatalf("stat file: %v", statErr)
		}
		if got := info.Mode().Perm(); got != 0o600 {
			t.Fatalf("file mode = %#o, want %#o", got, os.FileMode(0o600))
		}
	}
	if _, err := store.CreateExclusive("upload.tmp", os.O_RDWR); !errors.Is(err, os.ErrExist) {
		t.Fatalf("duplicate create error = %v, want os.ErrExist", err)
	}
	for _, name := range []string{"", "../escape.tmp", "nested/file.tmp", `nested\file.tmp`} {
		if _, err := store.CreateExclusive(name, os.O_RDWR); !errors.Is(err, ErrInvalidName) {
			t.Fatalf("create %q error = %v, want ErrInvalidName", name, err)
		}
	}
}

func TestStoreTracksAggregateReservationsConcurrently(t *testing.T) {
	t.Parallel()

	store, err := NewStore(Config{Root: filepath.Join(t.TempDir(), "scratch"), MaxBytes: 10})
	if err != nil {
		t.Fatalf("new store: %v", err)
	}

	var accepted atomic.Int64
	var workers sync.WaitGroup
	for range 32 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			if reserveErr := store.Reserve(1); reserveErr == nil {
				accepted.Add(1)
			} else if !errors.Is(reserveErr, ErrQuotaExceeded) {
				t.Errorf("reserve error = %v", reserveErr)
			}
		}()
	}
	workers.Wait()

	if got := accepted.Load(); got != 10 {
		t.Fatalf("accepted reservations = %d, want 10", got)
	}
	if got := store.UsedBytes(); got != 10 {
		t.Fatalf("used bytes = %d, want 10", got)
	}
	store.Track("first.tmp", 4)
	store.ReleaseTracked("first.tmp", 1)
	if got := store.UsedBytes(); got != 6 {
		t.Fatalf("used bytes after tracked release = %d, want 6", got)
	}
	store.Release(20)
	if got := store.UsedBytes(); got != 0 {
		t.Fatalf("used bytes after clamped release = %d, want 0", got)
	}
	if err := store.Reserve(-1); !errors.Is(err, ErrInvalidSize) {
		t.Fatalf("negative reservation error = %v, want ErrInvalidSize", err)
	}
}

func TestNewStoreValidatesConfiguration(t *testing.T) {
	t.Parallel()

	for _, config := range []Config{
		{},
		{Root: t.TempDir()},
		{Root: t.TempDir(), MaxBytes: -1},
	} {
		if _, err := NewStore(config); !errors.Is(err, ErrInvalidConfig) {
			t.Fatalf("config %#v error = %v, want ErrInvalidConfig", config, err)
		}
	}
}
