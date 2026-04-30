package auth

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
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
		files = []string{"session.json", "config.json", "tdrive.db", "imp_config.json"}
	default:
		return fmt.Errorf("auth: unknown logout mode %q", mode)
	}

	for _, name := range files {
		if err := os.Remove(filepath.Join(dir, name)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("auth: remove %s: %w", name, err)
		}
	}
	return nil
}

func tdriveConfigDir() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("auth: locate config dir: %w", err)
	}
	return filepath.Join(base, "TDrive"), nil
}
