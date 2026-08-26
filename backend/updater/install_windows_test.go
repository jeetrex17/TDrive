//go:build windows

package updater

import (
	"archive/zip"
	"bytes"
	"errors"
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

func TestWindowsInstallRemovesNewMediaWhenExecutableSwapFails(t *testing.T) {
	work := t.TempDir()
	payload := writeZip(t, map[string]string{
		"TDrive-windows-amd64.exe": "new exe",
		"media/mpv.exe":            "new mpv",
	})
	dir := filepath.Join(work, "app")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	exe := filepath.Join(dir, "TDrive.exe")
	if err := os.WriteFile(exe, []byte("old exe"), 0o755); err != nil {
		t.Fatal(err)
	}

	swapErr := errors.New("executable swap failed")
	swapCalls := 0
	installer := windowsInstaller{
		swapPaths: func(current, fresh string) error {
			swapCalls++
			if current != exe {
				t.Fatalf("swap current = %q, want %q", current, exe)
			}
			return swapErr
		},
	}
	err := installer.Install(payload, Target{Path: exe, Kind: "exe"})
	if !errors.Is(err, swapErr) {
		t.Fatalf("Install error = %v, want %v", err, swapErr)
	}
	if swapCalls != 1 {
		t.Fatalf("swap calls = %d, want 1", swapCalls)
	}
	if got, readErr := os.ReadFile(exe); readErr != nil || string(got) != "old exe" {
		t.Fatalf("target after failed install = %q, %v", got, readErr)
	}
	if exists(filepath.Join(dir, mediaDirName)) {
		t.Fatalf("new media must be removed when executable installation fails")
	}
	entries, readErr := os.ReadDir(dir)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if len(entries) != 1 || entries[0].Name() != filepath.Base(exe) {
		t.Fatalf("failed install left unexpected entries: %v", entries)
	}
}

func TestWindowsInstallRemovesPartialStagingWhenExtractionFails(t *testing.T) {
	work := t.TempDir()
	var payloadBytes bytes.Buffer
	zipWriter := zip.NewWriter(&payloadBytes)
	for _, entry := range []struct {
		name    string
		content string
	}{
		{name: "partial.txt", content: "extracted before the error"},
		{name: "../escape.txt", content: "rejected entry"},
	} {
		file, err := zipWriter.Create(entry.name)
		if err != nil {
			t.Fatalf("zip create %s: %v", entry.name, err)
		}
		if _, err := file.Write([]byte(entry.content)); err != nil {
			t.Fatalf("zip write %s: %v", entry.name, err)
		}
	}
	if err := zipWriter.Close(); err != nil {
		t.Fatalf("zip close: %v", err)
	}
	payload := filepath.Join(work, "payload.zip")
	if err := os.WriteFile(payload, payloadBytes.Bytes(), 0o644); err != nil {
		t.Fatalf("write payload: %v", err)
	}

	dir := filepath.Join(work, "app")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	exe := filepath.Join(dir, "TDrive.exe")
	if err := os.WriteFile(exe, []byte("old exe"), 0o755); err != nil {
		t.Fatal(err)
	}

	err := (windowsInstaller{}).Install(payload, Target{Path: exe, Kind: "exe"})
	if !errors.Is(err, errZipSlip) {
		t.Fatalf("Install error = %v, want errZipSlip", err)
	}
	if exists(stagingPath(dir)) {
		t.Fatalf("partial staging must be removed after extraction failure")
	}
	if got, readErr := os.ReadFile(exe); readErr != nil || string(got) != "old exe" {
		t.Fatalf("target after failed extraction = %q, %v", got, readErr)
	}
}
