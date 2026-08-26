package updater

import (
	"archive/zip"
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func writeZip(t *testing.T, entries map[string]string) string {
	t.Helper()
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	for name, content := range entries {
		f, err := w.Create(name)
		if err != nil {
			t.Fatalf("zip create %s: %v", name, err)
		}
		if _, err := f.Write([]byte(content)); err != nil {
			t.Fatalf("zip write %s: %v", name, err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatalf("zip close: %v", err)
	}
	path := filepath.Join(t.TempDir(), "payload.zip")
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatalf("write zip: %v", err)
	}
	return path
}

func TestExtractZipWritesNestedEntries(t *testing.T) {
	src := writeZip(t, map[string]string{
		"TDrive.exe":      "exe",
		"media/mpv.exe":   "mpv",
		"media/lib/x.dll": "dll",
		"empty/":          "",
	})
	dest := filepath.Join(t.TempDir(), "out")
	if err := extractZip(src, dest); err != nil {
		t.Fatalf("extractZip: %v", err)
	}
	for path, want := range map[string]string{
		"TDrive.exe":      "exe",
		"media/mpv.exe":   "mpv",
		"media/lib/x.dll": "dll",
	} {
		got, err := os.ReadFile(filepath.Join(dest, filepath.FromSlash(path)))
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		if string(got) != want {
			t.Fatalf("%s = %q, want %q", path, got, want)
		}
	}
	if info, err := os.Stat(filepath.Join(dest, "empty")); err != nil || !info.IsDir() {
		t.Fatalf("directory entry not created: %v", err)
	}
}

func TestExtractZipRejectsTraversal(t *testing.T) {
	for _, name := range []string{"../escape.txt", "sub/../../escape.txt", "/abs.txt", "C:/abs.txt"} {
		src := writeZip(t, map[string]string{name: "x"})
		dest := filepath.Join(t.TempDir(), "out")
		err := extractZip(src, dest)
		if !errors.Is(err, errZipSlip) {
			t.Errorf("extractZip(%q) err = %v, want errZipSlip", name, err)
		}
	}
}

func TestSafeJoinAcceptsPlainPaths(t *testing.T) {
	root := t.TempDir()
	got, err := safeJoin(root, "a/b/c.txt")
	if err != nil {
		t.Fatalf("safeJoin: %v", err)
	}
	if got != filepath.Join(root, "a", "b", "c.txt") {
		t.Fatalf("safeJoin = %q", got)
	}
}
