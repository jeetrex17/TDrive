package auth

import (
	"os"
	"path/filepath"
	"testing"
)

func TestClearUserData(t *testing.T) {
	t.Run("soft keeps everything but session", func(t *testing.T) {
		dir := setupConfigDir(t, "session.json", "config.json", "tdrive.db", "imp_config.json", "photo-backup.db", "photo-backup.db-wal", "photo-backup.db-shm")
		if err := ClearUserData(LogoutSoft); err != nil {
			t.Fatalf("ClearUserData: %v", err)
		}
		assertGone(t, dir, "session.json")
		assertPresent(t, dir, "config.json", "tdrive.db", "imp_config.json", "photo-backup.db", "photo-backup.db-wal", "photo-backup.db-shm")
	})

	t.Run("full removes all user-scoped files", func(t *testing.T) {
		dir := setupConfigDir(t, "session.json", "config.json", "tdrive.db", "imp_config.json", "photo-backup.db", "photo-backup.db-wal", "photo-backup.db-shm")
		cacheDir := setupThumbnailCache(t)
		cacheRoot := filepath.Dir(cacheDir)
		nativeCache, err := os.UserCacheDir()
		if err != nil {
			t.Fatal(err)
		}
		stagingRoots := []string{filepath.Join(cacheRoot, "photo-backup-stage"), filepath.Join(cacheRoot, "photo-backup-desktop"), filepath.Join(nativeCache, "TDrivePhotoBackup")}
		for _, root := range stagingRoots {
			if err := os.MkdirAll(root, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(root, "original.jpg"), []byte("private staged photo"), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		for _, name := range []string{"tdrive-upload-crash", "tdrive-enc-crash", ".tdrive-upload-part-crash", "tdrive-mountdav-put-crash"} {
			if err := os.WriteFile(filepath.Join(cacheRoot, name), []byte("plaintext"), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		if err := os.WriteFile(filepath.Join(cacheRoot, "keep.cache"), []byte("unrelated"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := ClearUserData(LogoutFull); err != nil {
			t.Fatalf("ClearUserData: %v", err)
		}
		assertGone(t, dir, "session.json", "config.json", "tdrive.db", "imp_config.json", "photo-backup.db", "photo-backup.db-wal", "photo-backup.db-shm")
		if _, err := os.Stat(cacheDir); !os.IsNotExist(err) {
			t.Fatalf("thumbnail cache still exists (err=%v)", err)
		}
		assertGone(t, cacheRoot, "tdrive-upload-crash", "tdrive-enc-crash", ".tdrive-upload-part-crash", "tdrive-mountdav-put-crash")
		assertPresent(t, cacheRoot, "keep.cache")
		for _, root := range stagingRoots {
			if _, err := os.Stat(root); !os.IsNotExist(err) {
				t.Errorf("native staging survives full logout: %s, %v", root, err)
			}
		}
	})

	t.Run("idempotent on missing files", func(t *testing.T) {
		setupConfigDir(t)
		if err := ClearUserData(LogoutFull); err != nil {
			t.Fatalf("first call: %v", err)
		}
		if err := ClearUserData(LogoutFull); err != nil {
			t.Fatalf("second call: %v", err)
		}
	})

	t.Run("rejects unknown mode", func(t *testing.T) {
		setupConfigDir(t)
		if err := ClearUserData("nuke"); err == nil {
			t.Fatal("expected error for unknown mode")
		}
	})

	t.Run("preserves unrelated files", func(t *testing.T) {
		dir := setupConfigDir(t, "session.json", "user_notes.txt")
		if err := ClearUserData(LogoutFull); err != nil {
			t.Fatalf("ClearUserData: %v", err)
		}
		assertPresent(t, dir, "user_notes.txt")
	})
}

func setupThumbnailCache(t *testing.T) string {
	t.Helper()
	base, err := os.UserCacheDir()
	if err != nil {
		t.Fatalf("UserCacheDir: %v", err)
	}
	dir := filepath.Join(base, "TDrive", "thumbnails")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "cached.bin"), []byte("ciphertext"), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

// setupConfigDir points os.UserConfigDir at a fresh temp directory and
// seeds the given files inside its TDrive subfolder. Returns the TDrive
// dir itself. The override mirrors the platform conventions os.UserConfigDir
// itself follows so the production code stays untouched.
func setupConfigDir(t *testing.T, files ...string) string {
	t.Helper()
	root := t.TempDir()

	// Linux + freebsd consult XDG_CONFIG_HOME; darwin synthesises
	// $HOME/Library/Application Support; windows uses %AppData%. Setting
	// all three lets the same test pass on every supported platform.
	t.Setenv("XDG_CONFIG_HOME", root)
	t.Setenv("XDG_CACHE_HOME", root)
	t.Setenv("HOME", root)
	t.Setenv("AppData", root)
	t.Setenv("LOCALAPPDATA", root)

	base, err := os.UserConfigDir()
	if err != nil {
		t.Fatalf("UserConfigDir: %v", err)
	}
	tdriveDir := filepath.Join(base, "TDrive")
	if err := os.MkdirAll(tdriveDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	for _, name := range files {
		if err := os.WriteFile(filepath.Join(tdriveDir, name), []byte("x"), 0o644); err != nil {
			t.Fatalf("seed %s: %v", name, err)
		}
	}
	return tdriveDir
}

func assertGone(t *testing.T, dir string, names ...string) {
	t.Helper()
	for _, name := range names {
		if _, err := os.Stat(filepath.Join(dir, name)); !os.IsNotExist(err) {
			t.Errorf("%s still exists (err=%v)", name, err)
		}
	}
}

func assertPresent(t *testing.T, dir string, names ...string) {
	t.Helper()
	for _, name := range names {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Errorf("%s missing: %v", name, err)
		}
	}
}
