//go:build windows

package nativeplayer

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

const windowsPreflightHelperEnv = "TDRIVE_WINDOWS_PREFLIGHT_TEST_HELPER"
const windowsPreflightMarkerEnv = "TDRIVE_WINDOWS_PREFLIGHT_TEST_MARKER"

func TestMain(m *testing.M) {
	if os.Getenv(windowsPreflightHelperEnv) == "1" {
		marker := os.Getenv(windowsPreflightMarkerEnv)
		if marker == "" {
			os.Exit(96)
		}
		if err := os.WriteFile(marker, []byte("launched"), 0o600); err != nil {
			os.Exit(97)
		}
		time.Sleep(100 * time.Millisecond)
		os.Exit(98)
	}
	os.Exit(m.Run())
}

func TestWindowsPreflightRemainsNoOpWhenLegacyOptInIsSet(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable: %v", err)
	}
	marker := filepath.Join(t.TempDir(), "preflight-launched")
	t.Setenv(windowsNativePlayerFlag, "1")
	t.Setenv("TDRIVE_ENABLE_MPV_PREFLIGHT", "1")
	t.Setenv("TDRIVE_SKIP_MPV_PREFLIGHT", "")
	t.Setenv("TDRIVE_MPV_BIN", executable)
	t.Setenv(windowsPreflightHelperEnv, "1")
	t.Setenv(windowsPreflightMarkerEnv, marker)

	if err := PreflightDecode(context.Background(), "http://127.0.0.1/media/opaque-token"); err != nil {
		t.Fatalf("PreflightDecode returned an error: %v", err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("Windows preflight launched a subprocess; marker stat error = %v", err)
	}
}
