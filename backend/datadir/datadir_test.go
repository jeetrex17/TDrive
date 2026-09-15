package datadir

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// clearOverride resets the package-global override around a test so cases do
// not leak into each other.
func clearOverride(t *testing.T) {
	t.Helper()
	Set("")
	SetCache("")
	t.Cleanup(func() {
		Set("")
		SetCache("")
	})
}

func TestDirDefaultsToUserConfigDir(t *testing.T) {
	clearOverride(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "config"))
	t.Setenv("AppData", filepath.Join(home, "AppData"))

	base, err := os.UserConfigDir()
	if err != nil {
		t.Fatalf("UserConfigDir: %v", err)
	}
	want := filepath.Join(base, "TDrive")

	got, err := Dir()
	if err != nil {
		t.Fatalf("Dir: %v", err)
	}
	if got != want {
		t.Fatalf("Dir = %q, want %q", got, want)
	}
	assertPrivateDir(t, got)
}

func TestCacheDirDefaultsToUserCacheDir(t *testing.T) {
	clearOverride(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, "cache"))
	t.Setenv("LocalAppData", filepath.Join(home, "LocalAppData"))

	base, err := os.UserCacheDir()
	if err != nil {
		t.Fatalf("UserCacheDir: %v", err)
	}
	want := filepath.Join(base, "TDrive")

	got, err := CacheDir()
	if err != nil {
		t.Fatalf("CacheDir: %v", err)
	}
	if got != want {
		t.Fatalf("CacheDir = %q, want %q", got, want)
	}
	assertPrivateDir(t, got)
}

func TestOverridesAreIndependent(t *testing.T) {
	clearOverride(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, "cache"))
	t.Setenv("LocalAppData", filepath.Join(home, "LocalAppData"))

	data := t.TempDir()
	Set(data)
	if got, _ := Dir(); got != filepath.Join(data, "TDrive") {
		t.Fatalf("Dir = %q, want under %q", got, data)
	}
	// Set must leave the cache on its default, which is what iOS relies on.
	base, err := os.UserCacheDir()
	if err != nil {
		t.Fatalf("UserCacheDir: %v", err)
	}
	if got, _ := CacheDir(); got != filepath.Join(base, "TDrive") {
		t.Fatalf("CacheDir after Set = %q, want default under %q", got, base)
	}

	cache := t.TempDir()
	SetCache(cache)
	got, err := CacheDir()
	if err != nil {
		t.Fatalf("CacheDir: %v", err)
	}
	if got != filepath.Join(cache, "TDrive") {
		t.Fatalf("CacheDir = %q, want under %q", got, cache)
	}
	assertPrivateDir(t, got)
}

func TestOverrideResolvedAtCallTime(t *testing.T) {
	clearOverride(t)
	first := t.TempDir()
	Set(first)
	if got, _ := Dir(); got != filepath.Join(first, "TDrive") {
		t.Fatalf("Dir = %q, want under %q", got, first)
	}

	second := t.TempDir()
	Set(second)
	if got, _ := Dir(); got != filepath.Join(second, "TDrive") {
		t.Fatalf("Dir after re-Set = %q, want under %q", got, second)
	}
}

func assertPrivateDir(t *testing.T, dir string) {
	t.Helper()
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("stat %q: %v", dir, err)
	}
	if !info.IsDir() {
		t.Fatalf("%q is not a directory", dir)
	}
	// Windows does not honour unix permission bits.
	if runtime.GOOS != "windows" {
		if perm := info.Mode().Perm(); perm != 0o700 {
			t.Fatalf("dir mode = %04o, want 0700", perm)
		}
	}
}
