package updater

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Target is the on-disk artifact the updater replaces: the .app bundle on
// macOS, the executable (plus its media folder) on Windows, the AppImage file
// on Linux.
type Target struct {
	Path string
	Kind string // "bundle" | "exe" | "appimage"
}

// NotInstallableError explains, in user-facing words, why the running copy
// cannot replace itself. The panel shows Reason and falls back to the manual
// download page.
type NotInstallableError struct {
	Reason string
}

func (e *NotInstallableError) Error() string { return e.Reason }

// Installer performs the platform-specific swap. Implementations must leave
// the target untouched when Install fails and keep the replaced copy at
// target+previousSuffix until Cleanup runs after the next successful start.
type Installer interface {
	// Target resolves the running app's install location without touching
	// the filesystem beyond reading paths.
	Target() (Target, error)
	// Install replaces target with the verified payload (zip or AppImage).
	Install(payload string, target Target) error
	// Relaunch starts a fresh instance of target that waits for waitPID to
	// exit before initialising, so the single-backend lock is free.
	Relaunch(target Target, waitPID int) error
	// Cleanup removes leftovers from an earlier Install (previous copy,
	// staging directories). Best effort; errors are informational.
	Cleanup(target Target) error
}

const (
	previousSuffix = ".previous"
	stagingPrefix  = ".tdrive-update-"
)

// stagingPath returns a per-process staging location inside dir. Staging next
// to the target guarantees the final rename never crosses filesystems.
func stagingPath(dir string) string {
	return filepath.Join(dir, stagingPrefix+strconv.Itoa(os.Getpid()))
}

// dirWritable probes whether the process can create entries in dir.
func dirWritable(dir string) bool {
	f, err := os.CreateTemp(dir, ".tdrive-write-probe-*")
	if err != nil {
		return false
	}
	name := f.Name()
	_ = f.Close()
	_ = os.Remove(name)
	return true
}

// removeStaging deletes every staging entry left in dir, including those of
// earlier processes that crashed mid-install.
func removeStaging(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	var firstErr error
	for _, entry := range entries {
		if !strings.HasPrefix(entry.Name(), stagingPrefix) {
			continue
		}
		if err := os.RemoveAll(filepath.Join(dir, entry.Name())); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// swapPaths moves current aside as current+previousSuffix and renames fresh
// into its place. On failure the original is restored so the app on disk is
// never left missing.
func swapPaths(current, fresh string) error {
	previous := current + previousSuffix
	if err := os.RemoveAll(previous); err != nil {
		return fmt.Errorf("remove previous version: %w", err)
	}
	if err := os.Rename(current, previous); err != nil {
		return fmt.Errorf("move current version aside: %w", err)
	}
	if err := os.Rename(fresh, current); err != nil {
		if restore := os.Rename(previous, current); restore != nil {
			return fmt.Errorf("install new version: %w (restoring the previous version also failed: %v)", err, restore)
		}
		return fmt.Errorf("install new version: %w", err)
	}
	return nil
}

// copyFile writes src to dst with mode, fsyncing before returning.
func copyFile(src, dst string, mode os.FileMode) (err error) {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	defer func() {
		if cerr := out.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			_ = os.Remove(dst)
		}
	}()
	if _, err = io.Copy(out, in); err != nil {
		return err
	}
	if err = out.Sync(); err != nil {
		return err
	}
	return os.Chmod(dst, mode)
}

// errNoBundle and friends are internal sentinels for payload validation.
var (
	errNoAppInPayload       = errors.New("payload does not contain exactly one app")
	errNoExecutableInBundle = errors.New("payload app has no executable")
)
