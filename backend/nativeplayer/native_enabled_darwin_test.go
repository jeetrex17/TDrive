//go:build darwin

package nativeplayer

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDarwinNativePlayerRequiresExplicitOptIn(t *testing.T) {
	t.Setenv(darwinNativePlayerFlag, "")

	if darwinNativePlayerEnabled() {
		t.Fatalf("darwinNativePlayerEnabled() = true, want false by default")
	}
}

func TestDarwinNativePlayerCanBeEnabled(t *testing.T) {
	t.Setenv(darwinNativePlayerFlag, "1")

	if !darwinNativePlayerEnabled() {
		t.Fatalf("darwinNativePlayerEnabled() = false, want true when %s=1", darwinNativePlayerFlag)
	}
}

func TestFindPreflightMPVRequiresSystemLookupOptIn(t *testing.T) {
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
	t.Setenv(systemMPVLookupFlag, "")

	if path, err := findPreflightMPV(); err == nil {
		t.Fatalf("findPreflightMPV() = %q, want system PATH rejected without opt-in", path)
	}

	t.Setenv(systemMPVLookupFlag, "1")
	path, err := findPreflightMPV()
	if err != nil {
		t.Fatalf("findPreflightMPV() with opt-in: %v", err)
	}
	if path != fakeMPV {
		t.Fatalf("findPreflightMPV() = %q, want %q", path, fakeMPV)
	}
}
