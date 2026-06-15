package processlock

import (
	"errors"
	"os"
	"path/filepath"
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
