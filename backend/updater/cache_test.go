package updater

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func touch(t *testing.T, dir, name string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func TestResolveDefaultCacheDirKeepsPersistentUserCache(t *testing.T) {
	tempCalled := false
	got := resolveDefaultCacheDir(
		func() (string, error) { return filepath.Join("home", "cache"), nil },
		func(_, _ string) (string, error) {
			tempCalled = true
			return "", nil
		},
	)

	want := filepath.Join("home", "cache", "TDrive", "updates")
	if got != want {
		t.Fatalf("default cache dir = %q, want %q", got, want)
	}
	if tempCalled {
		t.Fatal("private temporary fallback must not run when the user cache is available")
	}
}

func TestResolveDefaultCacheDirCreatesPrivateUnpredictableFallback(t *testing.T) {
	tempRoot := t.TempDir()
	var requestedRoot, requestedPattern string
	makeTemp := func(root, pattern string) (string, error) {
		requestedRoot, requestedPattern = root, pattern
		return os.MkdirTemp(tempRoot, pattern)
	}
	userCacheUnavailable := func() (string, error) {
		return "", errors.New("user cache unavailable")
	}

	first := resolveDefaultCacheDir(userCacheUnavailable, makeTemp)
	second := resolveDefaultCacheDir(userCacheUnavailable, makeTemp)

	if first == "" || second == "" {
		t.Fatal("private temporary fallback must return a usable directory")
	}
	if first == second {
		t.Fatalf("fallback cache directories must be unpredictable, both were %q", first)
	}
	if filepath.Dir(first) != tempRoot || filepath.Dir(second) != tempRoot {
		t.Fatalf("fallback cache directories must come from MkdirTemp: %q and %q", first, second)
	}
	if requestedRoot != "" || requestedPattern != "TDrive-updates-*" {
		t.Fatalf("MkdirTemp called with root %q and pattern %q", requestedRoot, requestedPattern)
	}
	if runtime.GOOS != "windows" {
		for _, dir := range []string{first, second} {
			info, err := os.Stat(dir)
			if err != nil {
				t.Fatalf("stat fallback cache %q: %v", dir, err)
			}
			if got := info.Mode().Perm(); got != 0o700 {
				t.Fatalf("fallback cache permissions = %#o, want 0700", got)
			}
		}
	}
}

func TestResolveDefaultCacheDirFailsClosedWhenPrivateFallbackFails(t *testing.T) {
	got := resolveDefaultCacheDir(
		func() (string, error) { return "", errors.New("user cache unavailable") },
		func(_, _ string) (string, error) { return "", errors.New("temp unavailable") },
	)
	if got != "" {
		t.Fatalf("cache dir = %q, want empty path so download fails closed", got)
	}
}

func TestNewWithUnavailableCacheDoesNotPruneWorkingDirectory(t *testing.T) {
	work := t.TempDir()
	t.Chdir(work)
	name := "TDrive-v1.6.0-macos-arm64.zip"
	if err := os.WriteFile(name, []byte("working directory file"), 0o600); err != nil {
		t.Fatalf("write working-directory payload: %v", err)
	}

	New(Options{CurrentVersion: "1.7.0", CacheDir: "", Installer: &fakeInstaller{}, Source: &fakeSource{}})

	if !exists(filepath.Join(work, name)) {
		t.Fatalf("empty cache path must not prune matching files from the working directory")
	}
}

func TestCheckDoesNotReuseWorkingDirectoryPayloadWhenCacheIsUnavailable(t *testing.T) {
	f := newFixture(t, "1.6.0", "1.7.0")
	f.service.opts.CacheDir = "" // Simulate failure to allocate the private fallback.
	t.Chdir(t.TempDir())
	if err := os.WriteFile(f.assetName, f.payload, 0o600); err != nil {
		t.Fatalf("write working-directory payload: %v", err)
	}

	got := f.service.Check(t.Context())
	if got.Phase != PhaseAvailable || got.Installable || got.InstallHint == "" {
		t.Fatalf("unavailable cache must disable automatic install, got %+v", got)
	}
	var notInstallable *NotInstallableError
	if err := f.service.StartDownload(); !errors.As(err, &notInstallable) {
		t.Fatalf("StartDownload = %v, want NotInstallableError", err)
	}
}

func TestEnsureCacheDirCreatesOwnerOnlyDirectory(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "TDrive", "updates")
	if err := ensureCacheDir(dir); err != nil {
		t.Fatalf("ensure cache dir: %v", err)
	}
	if runtime.GOOS == "windows" {
		return
	}

	info, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("stat cache dir: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o700 {
		t.Fatalf("cache permissions = %#o, want 0700", got)
	}
}

func TestEnsureCacheDirTightensExistingDirectory(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows does not expose Unix permission bits")
	}
	dir := filepath.Join(t.TempDir(), "updates")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatalf("create legacy cache dir: %v", err)
	}
	if err := ensureCacheDir(dir); err != nil {
		t.Fatalf("ensure cache dir: %v", err)
	}

	info, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("stat cache dir: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o700 {
		t.Fatalf("legacy cache permissions = %#o, want 0700", got)
	}
}

func TestPruneCacheDropsOldAndPartialPayloads(t *testing.T) {
	dir := t.TempDir()
	touch(t, dir, "TDrive-v1.6.0-macos-arm64.zip")
	touch(t, dir, "TDrive-v1.7.0-macos-arm64.zip")
	touch(t, dir, "TDrive-v1.8.0-macos-arm64.zip")
	touch(t, dir, "TDrive-v1.8.0-macos-arm64.zip.part")
	touch(t, dir, "unrelated.txt")

	pruneCache(dir, mustVersion(t, "1.7.0"))

	if exists(filepath.Join(dir, "TDrive-v1.6.0-macos-arm64.zip")) || exists(filepath.Join(dir, "TDrive-v1.7.0-macos-arm64.zip")) {
		t.Fatalf("payloads at or below the running version must be removed")
	}
	if exists(filepath.Join(dir, "TDrive-v1.8.0-macos-arm64.zip.part")) {
		t.Fatalf("partial downloads must be removed")
	}
	if !exists(filepath.Join(dir, "TDrive-v1.8.0-macos-arm64.zip")) || !exists(filepath.Join(dir, "unrelated.txt")) {
		t.Fatalf("newer payloads and foreign files must survive")
	}
}

func TestPruneCacheExceptKeepsOnlyTheNamedPayload(t *testing.T) {
	dir := t.TempDir()
	touch(t, dir, "TDrive-v1.7.0-macos-arm64.zip")
	touch(t, dir, "TDrive-v1.8.0-macos-arm64.zip")
	pruneCacheExcept(dir, "TDrive-v1.8.0-macos-arm64.zip")
	if exists(filepath.Join(dir, "TDrive-v1.7.0-macos-arm64.zip")) {
		t.Fatalf("superseded payload must be removed")
	}
	if !exists(filepath.Join(dir, "TDrive-v1.8.0-macos-arm64.zip")) {
		t.Fatalf("kept payload must survive")
	}
}
