package updater

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestSwapPathsReplacesAndKeepsPrevious(t *testing.T) {
	dir := t.TempDir()
	current := filepath.Join(dir, "app")
	fresh := filepath.Join(dir, "fresh")
	if err := os.WriteFile(current, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fresh, []byte("new"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := swapPaths(current, fresh); err != nil {
		t.Fatalf("swapPaths: %v", err)
	}
	if got, _ := os.ReadFile(current); string(got) != "new" {
		t.Fatalf("current = %q, want new", got)
	}
	if got, _ := os.ReadFile(current + previousSuffix); string(got) != "old" {
		t.Fatalf("previous = %q, want old", got)
	}
}

func TestSwapPathsRestoresCurrentWhenFreshIsMissing(t *testing.T) {
	dir := t.TempDir()
	current := filepath.Join(dir, "app")
	if err := os.WriteFile(current, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := swapPaths(current, filepath.Join(dir, "missing")); err == nil {
		t.Fatalf("expected failure for a missing fresh path")
	}
	if got, _ := os.ReadFile(current); string(got) != "old" {
		t.Fatalf("current must be restored, got %q", got)
	}
	if exists(current + previousSuffix) {
		t.Fatalf("previous must not linger after a rollback")
	}
}

func TestRemoveStagingOnlyTouchesStagingEntries(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, stagingPrefix+"123", "nested"), 0o755); err != nil {
		t.Fatal(err)
	}
	touch(t, dir, stagingPrefix+"999")
	touch(t, dir, "keep.txt")
	if err := removeStaging(dir); err != nil {
		t.Fatalf("removeStaging: %v", err)
	}
	if exists(filepath.Join(dir, stagingPrefix+"123")) || exists(filepath.Join(dir, stagingPrefix+"999")) {
		t.Fatalf("staging entries must be removed")
	}
	if !exists(filepath.Join(dir, "keep.txt")) {
		t.Fatalf("unrelated files must survive")
	}
}

func TestCopyFilePreservesContentAndMode(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src")
	dst := filepath.Join(dir, "dst")
	if err := os.WriteFile(src, []byte("payload"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := copyFile(src, dst, 0o755); err != nil {
		t.Fatalf("copyFile: %v", err)
	}
	got, err := os.ReadFile(dst)
	if err != nil || string(got) != "payload" {
		t.Fatalf("dst = %q, %v", got, err)
	}
	info, err := os.Stat(dst)
	if err != nil {
		t.Fatal(err)
	}
	// Windows has no Unix execute bit; os.Chmod can't set it there. The exec
	// bit only matters for the Linux AppImage path this helper serves.
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o100 == 0 {
		t.Fatalf("dst should be executable, mode = %v", info.Mode())
	}
}

func TestDirWritable(t *testing.T) {
	if !dirWritable(t.TempDir()) {
		t.Fatalf("temp dir must be writable")
	}
	if dirWritable(filepath.Join(t.TempDir(), "missing")) {
		t.Fatalf("missing dir must not be writable")
	}
}
