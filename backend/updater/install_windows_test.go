//go:build windows

package updater

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWindowsInstallSwapsExecutableAndMedia(t *testing.T) {
	work := t.TempDir()
	payload := writeZip(t, map[string]string{
		"TDrive-windows-amd64.exe": "new exe",
		"media/mpv.exe":            "new mpv",
		"media/libmpv-2.dll":       "new dll",
	})
	dir := filepath.Join(work, "app")
	exe := filepath.Join(dir, "My TDrive.exe") // user-renamed executable
	if err := os.MkdirAll(filepath.Join(dir, mediaDirName), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(exe, []byte("old exe"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, mediaDirName, "mpv.exe"), []byte("old mpv"), 0o755); err != nil {
		t.Fatal(err)
	}
	target := Target{Path: exe, Kind: "exe"}

	if err := (windowsInstaller{}).Install(payload, target); err != nil {
		t.Fatalf("Install: %v", err)
	}
	if got, _ := os.ReadFile(exe); string(got) != "new exe" {
		t.Fatalf("exe = %q", got)
	}
	if got, _ := os.ReadFile(filepath.Join(dir, mediaDirName, "libmpv-2.dll")); string(got) != "new dll" {
		t.Fatalf("media not replaced: %q", got)
	}
	if got, _ := os.ReadFile(exe + previousSuffix); string(got) != "old exe" {
		t.Fatalf("previous exe = %q", got)
	}
	if got, _ := os.ReadFile(filepath.Join(dir, mediaDirName+previousSuffix, "mpv.exe")); string(got) != "old mpv" {
		t.Fatalf("previous media = %q", got)
	}
	if err := (windowsInstaller{}).Cleanup(target); err != nil {
		t.Fatalf("Cleanup: %v", err)
	}
	if exists(exe+previousSuffix) || exists(filepath.Join(dir, mediaDirName+previousSuffix)) {
		t.Fatalf("Cleanup left previous copies behind")
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 2 {
		t.Fatalf("unexpected entries after install: %v", entries)
	}
}

func TestWindowsInstallRejectsAmbiguousPayload(t *testing.T) {
	work := t.TempDir()
	payload := writeZip(t, map[string]string{"a.exe": "a", "b.exe": "b"})
	exe := filepath.Join(work, "TDrive.exe")
	if err := os.WriteFile(exe, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := (windowsInstaller{}).Install(payload, Target{Path: exe, Kind: "exe"}); err == nil {
		t.Fatalf("expected error for two executables")
	}
	if got, _ := os.ReadFile(exe); string(got) != "old" {
		t.Fatalf("target changed after failed install")
	}
	if entries, _ := os.ReadDir(work); len(entries) != 1 {
		t.Fatalf("staging leftovers: %v", entries)
	}
}
