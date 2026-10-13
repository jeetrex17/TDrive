// Package processlock enforces one TDrive backend per user data directory: the
// GUI and the CLI daemon cannot run at the same time.
//
// A persistent guard file carries the OS-held exclusive lock. Process death
// releases ownership; PID metadata is diagnostic for current-format owners.
// The guard is never unlinked, so contenders always lock the same inode.
// Legacy PID-file owners remain protected during upgrades. On Android, readable
// process identity can disprove stale legacy ownership; uncertainty stays closed.
package processlock

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"TDrive/backend/datadir"
)

const (
	fileMode os.FileMode = 0o600
)

var ErrAlreadyRunning = errors.New("tdrive backend already running")

type Info struct {
	Role        string `json:"role"`
	PID         int    `json:"pid"`
	StartedAt   int64  `json:"started_at"`
	LockVersion int    `json:"lock_version,omitzero"`
}

type Lock struct {
	mu   sync.Mutex
	file *os.File
	info Info
}

func Acquire(role string) (*Lock, error) {
	if role == "" {
		role = "unknown"
	}
	path, err := Path()
	if err != nil {
		return nil, err
	}

	f, err := createLockFile(path)
	if err != nil {
		return nil, err
	}

	info := Info{
		Role:        role,
		PID:         os.Getpid(),
		StartedAt:   time.Now().Unix(),
		LockVersion: 1,
	}
	if err := writeInfo(path, info); err != nil {
		_ = f.Close()
		return nil, err
	}
	return &Lock{file: f, info: info}, nil
}

func createLockFile(path string) (*os.File, error) {
	f, err := os.OpenFile(path+".guard", os.O_RDWR|os.O_CREATE, fileMode)
	if err != nil {
		return nil, fmt.Errorf("backend lock: acquire: %w", err)
	}
	if err := tryLockFile(f); err != nil {
		_ = f.Close()
		if errors.Is(err, ErrAlreadyRunning) {
			return nil, ownerError(path)
		}
		return nil, fmt.Errorf("backend lock: acquire: %w", err)
	}
	if legacyOwnerActive(path) {
		_ = f.Close()
		return nil, ownerError(path)
	}
	return f, nil
}

func legacyOwnerActive(path string) bool {
	info, err := readInfo(path)
	if errors.Is(err, os.ErrNotExist) {
		return false
	}
	if err != nil {
		return true
	}
	if info.LockVersion == 1 {
		return false
	}
	if info.LockVersion != 0 {
		return true
	}
	return legacyOwnerRunning(info, path)
}

func ownerError(path string) error {
	info, err := readInfo(path)
	if err == nil && info.Role != "" {
		return fmt.Errorf("%w as %s (pid %d)", ErrAlreadyRunning, info.Role, info.PID)
	}
	return ErrAlreadyRunning
}

func writeInfo(path string, info Info) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".backend-lock-*")
	if err != nil {
		return fmt.Errorf("backend lock: metadata: %w", err)
	}
	temporary := f.Name()
	defer os.Remove(temporary)
	writeErr := json.NewEncoder(f).Encode(info)
	if writeErr == nil {
		writeErr = f.Chmod(fileMode)
	}
	if writeErr == nil {
		writeErr = f.Sync()
	}
	if err := errors.Join(writeErr, f.Close()); err != nil {
		return fmt.Errorf("backend lock: metadata: %w", err)
	}
	// Older builds do not use the guard. Reserve a missing metadata path with
	// O_EXCL and recheck an existing legacy owner before publishing atomically.
	reserved, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, fileMode)
	created := err == nil
	if created {
		if err := reserved.Close(); err != nil {
			_ = os.Remove(path)
			return err
		}
	} else if errors.Is(err, os.ErrExist) {
		if legacyOwnerActive(path) {
			return ownerError(path)
		}
	} else {
		return fmt.Errorf("backend lock: metadata: %w", err)
	}
	if err := os.Rename(temporary, path); err != nil {
		if created {
			_ = os.Remove(path)
		}
		return fmt.Errorf("backend lock: metadata: %w", err)
	}
	return nil
}

func (l *Lock) Release() error {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.file == nil {
		return nil
	}
	err := l.file.Close()
	l.file = nil
	return err
}

func (l *Lock) Info() Info {
	if l == nil {
		return Info{}
	}
	return l.info
}

func Path() (string, error) {
	dir, err := datadir.Dir()
	if err != nil {
		return "", fmt.Errorf("backend lock: config dir: %w", err)
	}
	return filepath.Join(dir, "backend.lock"), nil
}

func Read() (Info, error) {
	path, err := Path()
	if err != nil {
		return Info{}, err
	}
	return readInfo(path)
}

func readInfo(path string) (Info, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return Info{}, err
	}
	var info Info
	if err := json.Unmarshal(b, &info); err != nil {
		return Info{}, err
	}
	return info, nil
}

func ReadActive() (Info, bool, error) {
	path, err := Path()
	if err != nil {
		return Info{}, false, err
	}
	f, err := createLockFile(path)
	info, readErr := readInfo(path)
	if errors.Is(err, ErrAlreadyRunning) {
		if readErr != nil {
			info = Info{Role: "unknown"}
		}
		return info, true, nil
	}
	if err != nil {
		return Info{}, false, err
	}
	if err := f.Close(); err != nil {
		return Info{}, false, err
	}
	return info, false, nil
}

// ProcessRunning reports whether a process with pid is still alive. The
// updater's relaunch handshake uses it to wait for the instance that spawned
// the new one to exit before contending for the lock.
func ProcessRunning(pid int) bool {
	return processRunning(pid)
}
