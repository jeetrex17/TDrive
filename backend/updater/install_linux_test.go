//go:build linux

package updater

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestLinuxTargetRequiresAppImage(t *testing.T) {
	var notInstallable *NotInstallableError
	if _, err := (linuxInstaller{appImage: func() string { return "" }}).Target(); !errors.As(err, &notInstallable) {
		t.Fatalf("Target without APPIMAGE = %v, want NotInstallableError", err)
	}
	if _, err := (linuxInstaller{appImage: func() string { return filepath.Join(t.TempDir(), "missing") }}).Target(); !errors.As(err, &notInstallable) {
		t.Fatalf("Target with a missing file = %v, want NotInstallableError", err)
	}
	path := filepath.Join(t.TempDir(), "TDrive.AppImage")
	if err := os.WriteFile(path, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	target, err := (linuxInstaller{appImage: func() string { return path }}).Target()
	if err != nil || target.Path != path || target.Kind != "appimage" {
		t.Fatalf("Target = %+v, %v", target, err)
	}
}

func TestLinuxInstallReplacesAppImage(t *testing.T) {
	dir := t.TempDir()
	current := filepath.Join(dir, "TDrive.AppImage")
	if err := os.WriteFile(current, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	payload := filepath.Join(t.TempDir(), "TDrive-v9.9.9-linux-amd64.AppImage")
	if err := os.WriteFile(payload, []byte("new"), 0o644); err != nil {
		t.Fatal(err)
	}
	target := Target{Path: current, Kind: "appimage"}
	if err := (linuxInstaller{}).Install(payload, target); err != nil {
		t.Fatalf("Install: %v", err)
	}
	got, err := os.ReadFile(current)
	if err != nil || string(got) != "new" {
		t.Fatalf("installed = %q, %v", got, err)
	}
	info, _ := os.Stat(current)
	if info.Mode().Perm()&0o111 == 0 {
		t.Fatalf("AppImage must stay executable")
	}
	if got, _ := os.ReadFile(current + previousSuffix); string(got) != "old" {
		t.Fatalf("previous = %q", got)
	}
	if err := (linuxInstaller{}).Cleanup(target); err != nil {
		t.Fatalf("Cleanup: %v", err)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 1 {
		t.Fatalf("leftovers after cleanup: %v", entries)
	}
}
