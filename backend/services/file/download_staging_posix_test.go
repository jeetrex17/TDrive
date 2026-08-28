//go:build !windows

package file

import (
	"os"
	"testing"
)

func TestCreatePrivateFolderDownloadStagingRejectsWritableNamespace(t *testing.T) {
	parent := t.TempDir()
	if err := os.Chmod(parent, 0o777); err != nil {
		t.Fatalf("make destination writable: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(parent, 0o700) })

	staging, err := createPrivateFolderDownloadStaging(parent)
	if err == nil {
		if staging != nil {
			_ = staging.Close()
			_ = os.RemoveAll(staging.Path())
		}
		t.Fatal("shared writable destination accepted")
	}
}

func TestCreatePrivateFolderDownloadStagingUsesOwnerOnlyMode(t *testing.T) {
	parent := t.TempDir()
	staging, err := createPrivateFolderDownloadStaging(parent)
	if err != nil {
		t.Fatalf("create staging: %v", err)
	}
	defer func() {
		_ = staging.Close()
		_ = os.RemoveAll(staging.Path())
	}()

	info, err := os.Stat(staging.Path())
	if err != nil {
		t.Fatalf("stat staging: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o700 {
		t.Fatalf("staging mode = %#o, want 0700", got)
	}
}
