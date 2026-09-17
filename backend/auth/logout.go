package auth

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"TDrive/backend/datadir"
)

type LogoutMode string

const (
	LogoutSoft LogoutMode = "soft"
	LogoutFull LogoutMode = "full"
)

// ClearUserData removes the on-disk files for the chosen logout mode.
//
// Soft only drops the gotd session token, so the same user can log back in
// without re-downloading their projection. Full also drops the personal
// channel id, the SQLite cache, and the Telegram API credentials, leaving
// the install indistinguishable from a fresh one.
//
// Idempotent: missing files are not an error.
func ClearUserData(mode LogoutMode) error {
	dir, err := tdriveConfigDir()
	if err != nil {
		return err
	}

	var files []string
	switch mode {
	case LogoutSoft:
		files = []string{"session.json"}
	case LogoutFull:
		// The backup ledger contains source identities and local paths. Its
		// connection is closed by the app before full logout removes all three
		// SQLite files, including any outstanding WAL and shared-memory sidecar.
		files = []string{"session.json", "config.json", "tdrive.db", "imp_config.json", "photo-backup.db", "photo-backup.db-wal", "photo-backup.db-shm"}
	default:
		return fmt.Errorf("auth: unknown logout mode %q", mode)
	}

	for _, name := range files {
		if err := os.Remove(filepath.Join(dir, name)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("auth: remove %s: %w", name, err)
		}
	}
	if mode == LogoutFull {
		cacheDir, err := datadir.CacheDir()
		if err != nil {
			return fmt.Errorf("auth: locate cache dir: %w", err)
		}
		// Only the disposable thumbnail subtree is account-scoped here. Keep
		// unrelated application caches intact and make repeated logout safe.
		if err := os.RemoveAll(filepath.Join(cacheDir, "thumbnails")); err != nil {
			return fmt.Errorf("auth: clear thumbnail cache: %w", err)
		}
		if err := datadir.CleanupCacheTemps(); err != nil {
			return fmt.Errorf("auth: clear temporary cache files: %w", err)
		}
		// Native backup adapters stage originals in these dedicated directories.
		// The app drains backup work before calling us, so no worker can recreate
		// them after logout. Never remove user-selected source folders here.
		if err := os.RemoveAll(filepath.Join(cacheDir, "photo-backup-stage")); err != nil {
			return fmt.Errorf("auth: clear photo backup staging: %w", err)
		}
		if err := os.RemoveAll(filepath.Join(cacheDir, "photo-backup-desktop")); err != nil {
			return fmt.Errorf("auth: clear desktop photo staging: %w", err)
		}
		nativeCache, err := os.UserCacheDir()
		if err != nil {
			return fmt.Errorf("auth: locate native cache: %w", err)
		}
		if err := os.RemoveAll(filepath.Join(nativeCache, "TDrivePhotoBackup")); err != nil {
			return fmt.Errorf("auth: clear Photos library staging: %w", err)
		}
	}
	return nil
}

func tdriveConfigDir() (string, error) {
	dir, err := datadir.Dir()
	if err != nil {
		return "", fmt.Errorf("auth: locate config dir: %w", err)
	}
	return dir, nil
}
