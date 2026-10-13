package processlock

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func withConfigHome(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("XDG_CONFIG_HOME", dir)
	path, err := Path()
	if err != nil {
		t.Fatalf("lock path: %v", err)
	}
	return filepath.Dir(path)
}

func TestAcquireIgnoresReleasedOwnerMetadata(t *testing.T) {
	dir := withConfigHome(t)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	metadata, err := json.Marshal(map[string]any{"role": "gui", "pid": os.Getpid(), "started_at": 1, "lock_version": 1})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "backend.lock"), metadata, 0o600); err != nil {
		t.Fatal(err)
	}
	_, active, err := ReadActive()
	if err != nil || active {
		t.Fatalf("stale owner active=%v error=%v", active, err)
	}
	lock, err := Acquire("gui")
	if err != nil {
		t.Fatalf("stale live PID blocked startup: %v", err)
	}
	t.Cleanup(func() {
		if err := lock.Release(); err != nil {
			t.Error(err)
		}
	})
	info, active, err := ReadActive()
	if err != nil || !active || info.PID != os.Getpid() || info.Role != "gui" {
		t.Fatalf("active owner=%+v active=%v error=%v", info, active, err)
	}
}

func TestReleaseKeepsGuardInodeAndCannotReleaseSuccessor(t *testing.T) {
	withConfigHome(t)
	first, err := Acquire("gui")
	if err != nil {
		t.Fatal(err)
	}
	path, err := Path()
	if err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(path + ".guard")
	if err != nil {
		t.Fatal(err)
	}
	if err := first.Release(); err != nil {
		t.Fatal(err)
	}
	second, err := Acquire("daemon")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := second.Release(); err != nil {
			t.Error(err)
		}
	})
	if err := first.Release(); err != nil {
		t.Fatal(err)
	}
	after, err := os.Stat(path + ".guard")
	if err != nil || !os.SameFile(before, after) {
		t.Fatalf("guard inode was replaced: %v", err)
	}
	if lock, err := Acquire("gui"); !errors.Is(err, ErrAlreadyRunning) {
		if lock != nil {
			_ = lock.Release()
		}
		t.Fatalf("old release unlocked successor: %v", err)
	}
}

func TestConcurrentAcquisitionsHaveOneOwner(t *testing.T) {
	withConfigHome(t)
	start := make(chan struct{})
	results := make(chan *Lock, 16)
	failures := make(chan error, 16)
	var wg sync.WaitGroup
	for range 16 {
		wg.Go(func() {
			<-start
			lock, err := Acquire("gui")
			if err != nil {
				failures <- err
				return
			}
			results <- lock
		})
	}
	close(start)
	wg.Wait()
	close(results)
	close(failures)
	winners := 0
	for lock := range results {
		winners++
		if err := lock.Release(); err != nil {
			t.Error(err)
		}
	}
	if winners != 1 {
		t.Fatalf("owners=%d, want exactly one", winners)
	}
	for err := range failures {
		if !errors.Is(err, ErrAlreadyRunning) {
			t.Errorf("contender error=%v", err)
		}
	}
}

func TestAcquireRejectsSecondBackend(t *testing.T) {
	withConfigHome(t)

	first, err := Acquire("gui")
	if err != nil {
		t.Fatalf("first acquire: %v", err)
	}
	defer func() { _ = first.Release() }()

	_, err = Acquire("daemon")
	if !errors.Is(err, ErrAlreadyRunning) {
		t.Fatalf("second acquire err = %v, want ErrAlreadyRunning", err)
	}
}

func TestActiveGuardDoesNotDependOnReadableMetadata(t *testing.T) {
	withConfigHome(t)
	lock, err := Acquire("gui")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := lock.Release(); err != nil {
			t.Error(err)
		}
	})
	path, err := Path()
	if err != nil {
		t.Fatal(err)
	}
	for _, remove := range []bool{false, true} {
		if remove {
			err = os.Remove(path)
		} else {
			err = os.WriteFile(path, []byte("broken metadata"), fileMode)
		}
		if err != nil {
			t.Fatal(err)
		}
		info, active, err := ReadActive()
		if err != nil || !active || info.Role != "unknown" {
			t.Fatalf("unreadable metadata owner=%+v active=%v error=%v", info, active, err)
		}
		if second, err := Acquire("daemon"); !errors.Is(err, ErrAlreadyRunning) {
			if second != nil {
				_ = second.Release()
			}
			t.Fatalf("metadata change bypassed live guard: %v", err)
		}
	}
}

func TestAcquireReclaimsStaleLock(t *testing.T) {
	dir := withConfigHome(t)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "backend.lock"), []byte(`{"role":"daemon","pid":-1,"started_at":1}`), 0o600); err != nil {
		t.Fatalf("write stale lock: %v", err)
	}

	lock, err := Acquire("gui")
	if err != nil {
		t.Fatalf("acquire after stale lock: %v", err)
	}
	defer func() { _ = lock.Release() }()
	if lock.Info().Role != "gui" {
		t.Fatalf("lock role = %q, want gui", lock.Info().Role)
	}
}
