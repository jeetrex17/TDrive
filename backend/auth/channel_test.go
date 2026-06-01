package auth

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGetTDriveChannelReturnsCorruptConfigError(t *testing.T) {
	root := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", root)
	t.Setenv("HOME", root)
	t.Setenv("AppData", root)

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
