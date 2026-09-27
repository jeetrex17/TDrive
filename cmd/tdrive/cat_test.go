package main

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestCatTempPathHasPrivateDirectoryAndCleanup(t *testing.T) {
	t.Parallel()
	path, cleanup, err := catTempPath()
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Dir(path)
	defer func() { _ = cleanup() }()
	if runtime.GOOS != "windows" {
		info, err := os.Stat(dir)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm()&0o077 != 0 {
			t.Fatalf("cat temp dir is accessible to others: %o", info.Mode().Perm())
		}
	}
	if err := os.WriteFile(path, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := cleanup(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("cat temp dir still exists: %v", err)
	}
}
