package auth

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func useTemporaryConfigDir(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", root)
	t.Setenv("HOME", root)
	t.Setenv("AppData", root)
	return root
}

func writeRawConfig(t *testing.T, contents string) {
	t.Helper()
	base, err := os.UserConfigDir()
	if err != nil {
		t.Fatalf("UserConfigDir: %v", err)
	}
	dir := filepath.Join(base, "TDrive")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(contents), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
}

func TestLoadConfigMissingFileMeansUnconfigured(t *testing.T) {
	useTemporaryConfigDir(t)

	got, err := LoadConfig()
	if err != nil || got != 0 {
		t.Fatalf("LoadConfig = %d, %v; want 0, nil", got, err)
	}
}

func TestLoadConfigRoundTrip(t *testing.T) {
	useTemporaryConfigDir(t)
	const channelID int64 = 8200

	if err := SaveConfig(channelID); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}
	got, err := LoadConfig()
	if err != nil || got != channelID {
		t.Fatalf("LoadConfig = %d, %v; want %d, nil", got, err, channelID)
	}
}

func TestLoadConfigUnparseableFileIsErrConfigInvalid(t *testing.T) {
	for name, contents := range map[string]string{
		"garbage":      `{bad json`,
		"json comment": "{\n  // \"channel_id\": 8200\n}\n",
	} {
		t.Run(name, func(t *testing.T) {
			useTemporaryConfigDir(t)
			writeRawConfig(t, contents)

			got, err := LoadConfig()
			if !errors.Is(err, ErrConfigInvalid) {
				t.Fatalf("LoadConfig error = %v, want ErrConfigInvalid", err)
			}
			if got != 0 {
				t.Fatalf("LoadConfig id = %d, want 0", got)
			}
		})
	}
}
