// Package datadir centralizes where TDrive keeps its own files. On desktop,
// daemon and CLI this stays os.UserConfigDir()/TDrive for data and
// os.UserCacheDir()/TDrive for cache, exactly as before. On mobile those
// defaults can be wrong -- Go's home dir is /sdcard on Android when HOME is
// unset, so the OS-convention dirs land on shared external storage -- and the
// host overrides the bases with the app-private sandbox at startup.
package datadir

import (
	"os"
	"path/filepath"
	"sync"
)

// appDir is the per-app subdirectory every TDrive file lives under, kept the
// same across platforms so the on-disk layout matches everywhere.
const appDir = "TDrive"

// dirMode is the private mode TDrive's data directories are created with,
// matching every caller's historical 0700.
const dirMode os.FileMode = 0o700

var (
	mu            sync.RWMutex
	dataOverride  string
	cacheOverride string
)

// Set overrides the base Dir builds on, replacing the os.UserConfigDir default.
// Mobile calls it once at startup, before any path is read, with the
// app-private storage path. An empty dir clears the override.
func Set(dir string) {
	mu.Lock()
	dataOverride = dir
	mu.Unlock()
}

// SetCache overrides the base CacheDir builds on, replacing the
// os.UserCacheDir default. Android needs it (no usable home dir); iOS keeps the
// default, which already resolves to the app's Library/Caches and stays out of
// backups. An empty dir clears the override.
func SetCache(dir string) {
	mu.Lock()
	cacheOverride = dir
	mu.Unlock()
}

// Dir returns TDrive's data/config directory, creating it 0700. Defaults to
// os.UserConfigDir()/TDrive; Set replaces the base.
func Dir() (string, error) {
	mu.RLock()
	root := dataOverride
	mu.RUnlock()
	return resolve(root, os.UserConfigDir)
}

// CacheDir returns TDrive's cache directory, creating it 0700. Defaults to
// os.UserCacheDir()/TDrive; SetCache replaces the base.
func CacheDir() (string, error) {
	mu.RLock()
	root := cacheOverride
	mu.RUnlock()
	return resolve(root, os.UserCacheDir)
}

// resolve derives the default base at call time, not at init, so tests that
// redirect HOME/XDG keep working through the defaults.
func resolve(root string, base func() (string, error)) (string, error) {
	if root == "" {
		var err error
		if root, err = base(); err != nil {
			return "", err
		}
	}

	dir := filepath.Join(root, appDir)
	if err := os.MkdirAll(dir, dirMode); err != nil {
		return "", err
	}
	_ = os.Chmod(dir, dirMode)
	return dir, nil
}
