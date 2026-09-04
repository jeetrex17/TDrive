package file

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPublishFolderDownloadMovesCompletedTree(t *testing.T) {
	parent := t.TempDir()
	staging := filepath.Join(parent, ".tdrive-folder-download-test")
	final := filepath.Join(parent, "Project")
	if err := os.Mkdir(staging, 0o700); err != nil {
		t.Fatalf("mkdir staging: %v", err)
	}
	if err := os.WriteFile(filepath.Join(staging, "file.txt"), []byte("done"), 0o600); err != nil {
		t.Fatalf("write staging file: %v", err)
	}

	if err := publishFolderDownload(staging, final); err != nil {
		t.Fatalf("publishFolderDownload: %v", err)
	}
	if _, err := os.Stat(staging); !os.IsNotExist(err) {
		t.Fatalf("staging still exists after publish: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(final, "file.txt"))
	if err != nil || string(got) != "done" {
		t.Fatalf("published file = %q, err=%v", got, err)
	}
}

func TestPublishFolderDownloadPreservesExistingDestination(t *testing.T) {
	parent := t.TempDir()
	staging := filepath.Join(parent, ".tdrive-folder-download-test")
	final := filepath.Join(parent, "Project")
	if err := os.Mkdir(staging, 0o700); err != nil {
		t.Fatalf("mkdir staging: %v", err)
	}
	if err := os.WriteFile(filepath.Join(staging, "new.txt"), []byte("new"), 0o600); err != nil {
		t.Fatalf("write staging file: %v", err)
	}
	if err := os.Mkdir(final, 0o755); err != nil {
		t.Fatalf("mkdir final: %v", err)
	}
	marker := filepath.Join(final, "keep.txt")
	if err := os.WriteFile(marker, []byte("keep"), 0o600); err != nil {
		t.Fatalf("write destination marker: %v", err)
	}

	err := publishFolderDownload(staging, final)
	if err == nil {
		t.Fatal("publish unexpectedly replaced an existing destination")
	}
	if !os.IsExist(err) {
		t.Fatalf("publish error = %v, want os.IsExist so DownloadFolder reports \"already exists\"", err)
	}
	got, err := os.ReadFile(marker)
	if err != nil || string(got) != "keep" {
		t.Fatalf("existing destination changed: body=%q err=%v", got, err)
	}
	if _, err := os.Stat(filepath.Join(staging, "new.txt")); err != nil {
		t.Fatalf("staging was lost after rejected publish: %v", err)
	}
}
