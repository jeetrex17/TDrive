package auth

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
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

func TestGetTDriveChannelReturnsSavedConfigWithoutTelegram(t *testing.T) {
	useTemporaryConfigDir(t)
	const channelID int64 = 8200
	if err := SaveConfig(channelID); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}

	got, err := GetTDriveChannel(context.Background(), nil)
	if err != nil {
		t.Fatalf("GetTDriveChannel: %v", err)
	}
	if got != channelID {
		t.Fatalf("channel id = %d, want %d", got, channelID)
	}
}

func TestGetTDriveChannelMissingConfigRequiresExplicitSetup(t *testing.T) {
	useTemporaryConfigDir(t)

	got, err := GetTDriveChannel(context.Background(), nil)
	if got != 0 {
		t.Fatalf("channel id = %d, want 0", got)
	}
	if !errors.Is(err, ErrPersonalDriveSetupRequired) {
		t.Fatalf("error = %v, want ErrPersonalDriveSetupRequired", err)
	}

	base, configErr := os.UserConfigDir()
	if configErr != nil {
		t.Fatalf("UserConfigDir: %v", configErr)
	}
	if _, statErr := os.Stat(filepath.Join(base, "TDrive", "config.json")); !os.IsNotExist(statErr) {
		t.Fatalf("missing-config lookup created config.json (stat err = %v)", statErr)
	}
}

func TestGetTDriveChannelReturnsCorruptConfigError(t *testing.T) {
	useTemporaryConfigDir(t)

	cfgDir, err := os.UserConfigDir()
	if err != nil {
		t.Fatalf("UserConfigDir: %v", err)
	}
	tdriveDir := filepath.Join(cfgDir, "TDrive")
	if err := os.MkdirAll(tdriveDir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tdriveDir, "config.json"), []byte(`{bad json`), 0o600); err != nil {
		t.Fatalf("write corrupt config: %v", err)
	}

	_, err = GetTDriveChannel(context.Background(), nil)
	if err == nil {
		t.Fatal("expected corrupt config error")
	}
	if !strings.Contains(err.Error(), "load TDrive channel config") {
		t.Fatalf("err = %v, want load config context", err)
	}
}
