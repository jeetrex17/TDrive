package updater

import (
	"os"
	"path/filepath"
	"testing"
)

func touch(t *testing.T, dir, name string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func TestPruneCacheDropsOldAndPartialPayloads(t *testing.T) {
	dir := t.TempDir()
	touch(t, dir, "TDrive-v1.6.0-macos-arm64.zip")
	touch(t, dir, "TDrive-v1.7.0-macos-arm64.zip")
	touch(t, dir, "TDrive-v1.8.0-macos-arm64.zip")
	touch(t, dir, "TDrive-v1.8.0-macos-arm64.zip.part")
	touch(t, dir, "unrelated.txt")

	pruneCache(dir, mustVersion(t, "1.7.0"))

	if exists(filepath.Join(dir, "TDrive-v1.6.0-macos-arm64.zip")) || exists(filepath.Join(dir, "TDrive-v1.7.0-macos-arm64.zip")) {
		t.Fatalf("payloads at or below the running version must be removed")
	}
	if exists(filepath.Join(dir, "TDrive-v1.8.0-macos-arm64.zip.part")) {
		t.Fatalf("partial downloads must be removed")
	}
	if !exists(filepath.Join(dir, "TDrive-v1.8.0-macos-arm64.zip")) || !exists(filepath.Join(dir, "unrelated.txt")) {
		t.Fatalf("newer payloads and foreign files must survive")
	}
}

func TestPruneCacheExceptKeepsOnlyTheNamedPayload(t *testing.T) {
	dir := t.TempDir()
	touch(t, dir, "TDrive-v1.7.0-macos-arm64.zip")
	touch(t, dir, "TDrive-v1.8.0-macos-arm64.zip")
	pruneCacheExcept(dir, "TDrive-v1.8.0-macos-arm64.zip")
	if exists(filepath.Join(dir, "TDrive-v1.7.0-macos-arm64.zip")) {
		t.Fatalf("superseded payload must be removed")
	}
	if !exists(filepath.Join(dir, "TDrive-v1.8.0-macos-arm64.zip")) {
		t.Fatalf("kept payload must survive")
	}
}
