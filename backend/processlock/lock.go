package processlock

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

const (
	dirMode  os.FileMode = 0o700
	fileMode os.FileMode = 0o600
)

var ErrAlreadyRunning = errors.New("tdrive backend already running")

type Info struct {
	Role      string `json:"role"`
	PID       int    `json:"pid"`
	StartedAt int64  `json:"started_at"`
}

type Lock struct {
	path string
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
	if err := os.MkdirAll(filepath.Dir(path), dirMode); err != nil {
		return nil, fmt.Errorf("backend lock: create config dir: %w", err)
	}
	_ = os.Chmod(filepath.Dir(path), dirMode)

	f, err := createLockFile(path)
	if err != nil {
		return nil, err
	}

	info := Info{
		Role:      role,
		PID:       os.Getpid(),
		StartedAt: time.Now().Unix(),
	}
	if err := json.NewEncoder(f).Encode(info); err != nil {
		_ = f.Close()
		_ = os.Remove(path)
		return nil, fmt.Errorf("backend lock: write: %w", err)
	}
	if err := f.Chmod(fileMode); err != nil {
		_ = f.Close()
		_ = os.Remove(path)
		return nil, fmt.Errorf("backend lock: chmod: %w", err)
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		_ = os.Remove(path)
		return nil, fmt.Errorf("backend lock: sync: %w", err)
	}
	return &Lock{path: path, file: f, info: info}, nil
}

func createLockFile(path string) (*os.File, error) {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_EXCL, fileMode)
	if err == nil {
		return f, nil
	}
	if !errors.Is(err, os.ErrExist) {
		return nil, fmt.Errorf("backend lock: acquire: %w", err)
	}

	info, readErr := Read()
	if readErr == nil && !processRunning(info.PID) {
		_ = os.Remove(path)
		f, err = os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_EXCL, fileMode)
		if err == nil {
			return f, nil
		}
	}
	if readErr == nil && info.Role != "" {
		return nil, fmt.Errorf("%w as %s (pid %d)", ErrAlreadyRunning, info.Role, info.PID)
	}
	return nil, ErrAlreadyRunning
}

func (l *Lock) Release() error {
	if l == nil {
		return nil
	}
	var closeErr error
	if l.file != nil {
		closeErr = l.file.Close()
		l.file = nil
	}
	removeErr := os.Remove(l.path)
	if errors.Is(removeErr, os.ErrNotExist) {
		removeErr = nil
	}
	if closeErr != nil {
		return closeErr
	}
	return removeErr
}

func (l *Lock) Info() Info {
	if l == nil {
		return Info{}
	}
	return l.info
}

func Path() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("backend lock: config dir: %w", err)
	}
	return filepath.Join(base, "TDrive", "backend.lock"), nil
}

func Read() (Info, error) {
	path, err := Path()
	if err != nil {
		return Info{}, err
	}
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
	info, err := Read()
	if err != nil {
		return Info{}, false, err
	}
	return info, processRunning(info.PID), nil
}

// ProcessRunning reports whether a process with pid is still alive. The
// updater's relaunch handshake uses it to wait for the instance that spawned
// the new one to exit before contending for the lock.
func ProcessRunning(pid int) bool {
	return processRunning(pid)
}
