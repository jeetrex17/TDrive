//go:build !windows

package daemon

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"syscall"
)

const socketFileMode os.FileMode = 0o600

func SocketPath() (string, error) {
	if base := os.Getenv("XDG_RUNTIME_DIR"); base != "" {
		return filepath.Join(base, "TDrive", "daemon.sock"), nil
	}

	// Unix socket paths have small platform-specific length limits, especially
	// on macOS. Keep the default short and put it in an owner-only directory.
	return filepath.Join(os.TempDir(), "tdrive-"+strconv.Itoa(os.Getuid()), "daemon.sock"), nil
}

func listenSocket(path string) (net.Listener, error) {
	if err := ensureSocketDir(filepath.Dir(path)); err != nil {
		return nil, err
	}
	_ = os.Remove(path)

	ln, err := net.Listen("unix", path)
	if err != nil {
		return nil, fmt.Errorf("daemon socket: listen: %w", err)
	}
	if err := os.Chmod(path, socketFileMode); err != nil {
		_ = ln.Close()
		_ = os.Remove(path)
		return nil, fmt.Errorf("daemon socket: chmod: %w", err)
	}
	return ln, nil
}

func dialSocket(path string) (net.Conn, error) {
	conn, err := net.Dial("unix", path)
	if err != nil {
		return nil, fmt.Errorf("daemon is not running. Run: tdrive daemon start")
	}
	return conn, nil
}

func cleanupSocket(path string) {
	_ = os.Remove(path)
}

func ensureSocketDir(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("daemon socket: create dir: %w", err)
	}
	info, err := os.Lstat(dir)
	if err != nil {
		return fmt.Errorf("daemon socket: stat dir: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("daemon socket: refusing symlink dir %s", dir)
	}
	if !info.IsDir() {
		return fmt.Errorf("daemon socket: %s is not a directory", dir)
	}
	if stat, ok := info.Sys().(*syscall.Stat_t); ok && int(stat.Uid) != os.Getuid() {
		return fmt.Errorf("daemon socket: %s is not owned by current user", dir)
	}
	if info.Mode().Perm() != 0o700 {
		if err := os.Chmod(dir, 0o700); err != nil {
			return fmt.Errorf("daemon socket: chmod dir: %w", err)
		}
	}
	return nil
}
