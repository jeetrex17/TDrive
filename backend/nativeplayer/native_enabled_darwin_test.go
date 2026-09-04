//go:build darwin

package nativeplayer

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDarwinNativePlayerIsOnByDefault(t *testing.T) {
	t.Setenv(darwinNativePlayerFlag, "")

	if !darwinNativePlayerEnabled() {
		t.Fatalf("darwinNativePlayerEnabled() = false, want true by default")
	}
}

func TestDarwinNativePlayerCanBeDisabled(t *testing.T) {
	t.Setenv(darwinNativePlayerFlag, "0")

	if darwinNativePlayerEnabled() {
		t.Fatalf("darwinNativePlayerEnabled() = true, want false when %s=0", darwinNativePlayerFlag)
	}
}

func TestFindPreflightMPVFallsBackToSystemPath(t *testing.T) {
	pathDir := filepath.Join(t.TempDir(), "bin")
	if err := os.Mkdir(pathDir, 0o700); err != nil {
		t.Fatalf("create fake PATH dir: %v", err)
	}
	fakeMPV := filepath.Join(pathDir, "mpv")
	if err := os.WriteFile(fakeMPV, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatalf("write fake mpv: %v", err)
	}
	t.Setenv("PATH", filepath.Dir(fakeMPV))
	t.Setenv("TDRIVE_MPV_BIN", "")

	path, err := findPreflightMPV()
	if err != nil {
		t.Fatalf("findPreflightMPV(): %v", err)
	}
	if path != fakeMPV {
		t.Fatalf("findPreflightMPV() = %q, want %q", path, fakeMPV)
	}
}
