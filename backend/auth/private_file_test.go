package auth

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestWritePrivateFileReplacesContents(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "credentials.json")
	if err := os.WriteFile(path, []byte("old credentials"), 0o600); err != nil {
		t.Fatalf("write old file: %v", err)
	}

	if err := writePrivateFile(path, []byte("new credentials")); err != nil {
		t.Fatalf("writePrivateFile: %v", err)
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read replacement: %v", err)
	}
	if got, want := string(contents), "new credentials"; got != want {
		t.Fatalf("replacement = %q, want %q", got, want)
	}
}

func TestWritePrivateFileUsesRestrictiveModes(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows os.FileMode does not represent ACL permissions")
	}

	dir := filepath.Join(t.TempDir(), "private")
	path := filepath.Join(dir, "credentials.json")
	if err := writePrivateFile(path, []byte("secret")); err != nil {
		t.Fatalf("writePrivateFile: %v", err)
	}

	fileInfo, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat private file: %v", err)
	}
	if got := fileInfo.Mode().Perm(); got != privateFileMode {
		t.Fatalf("file mode = %04o, want %04o", got, privateFileMode)
	}
	dirInfo, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("stat private directory: %v", err)
	}
	if got := dirInfo.Mode().Perm(); got != privateDirMode {
		t.Fatalf("directory mode = %04o, want %04o", got, privateDirMode)
	}
}

func TestWritePrivateFileRenameFailurePreservesOldFileAndCleansTemp(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "credentials.json")
	const oldContents = "working credentials"
	if err := os.WriteFile(path, []byte(oldContents), 0o600); err != nil {
		t.Fatalf("write old file: %v", err)
	}

	injectedErr := errors.New("injected rename failure")
	originalRename := privateFileRename
	privateFileRename = func(_, _ string) error { return injectedErr }
	t.Cleanup(func() { privateFileRename = originalRename })

	err := writePrivateFile(path, []byte("replacement credentials"))
	if !errors.Is(err, injectedErr) {
		t.Fatalf("writePrivateFile error = %v, want injected rename failure", err)
	}
	contents, readErr := os.ReadFile(path)
	if readErr != nil {
		t.Fatalf("read original after failure: %v", readErr)
	}
	if got := string(contents); got != oldContents {
		t.Fatalf("original after failure = %q, want %q", got, oldContents)
	}

	entries, readDirErr := os.ReadDir(dir)
	if readDirErr != nil {
		t.Fatalf("read directory: %v", readDirErr)
	}
	if len(entries) != 1 || entries[0].Name() != filepath.Base(path) {
		t.Fatalf("directory entries after failure = %v, want only %q", entryNames(entries), filepath.Base(path))
	}
}

func entryNames(entries []os.DirEntry) []string {
	names := make([]string, len(entries))
	for i, entry := range entries {
		names[i] = entry.Name()
	}
	return names
}
