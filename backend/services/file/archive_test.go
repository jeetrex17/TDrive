package file

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestIsArchive(t *testing.T) {
	for in, want := range map[string]bool{
		"x.zip":     true,
		"X.ZIP":     true,
		"x.tar":     true,
		"x.tar.gz":  true,
		"x.tgz":     true,
		"x.TGZ":     true,
		"x.txt":     false,
		"x.tar.bz2": false,
		"x.gz":      false,
		"noext":     false,
	} {
		if got := IsArchive(in); got != want {
			t.Errorf("IsArchive(%q) = %v, want %v", in, got, want)
		}
	}
}

// buildZip writes a zip with the given regular files, directory entries, and
// (deliberately unsafe) entries, returning its path.
func buildZip(t *testing.T, regular, unsafe map[string]string, dirs []string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "a.zip")
	f, err := os.Create(p)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	zw := zip.NewWriter(f)
	for _, d := range dirs {
		if _, err := zw.Create(strings.TrimSuffix(d, "/") + "/"); err != nil {
			t.Fatal(err)
		}
	}
	add := func(name, content string) {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := io.WriteString(w, content); err != nil {
			t.Fatal(err)
		}
	}
	for n, c := range regular {
		add(n, c)
	}
	for n, c := range unsafe {
		add(n, c)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return p
}

func buildTarGz(t *testing.T, regular map[string]string, dirs []string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "a.tar.gz")
	f, err := os.Create(p)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	gw := gzip.NewWriter(f)
	tw := tar.NewWriter(gw)
	for _, d := range dirs {
		if err := tw.WriteHeader(&tar.Header{Name: strings.TrimSuffix(d, "/") + "/", Typeflag: tar.TypeDir, Mode: 0o755}); err != nil {
			t.Fatal(err)
		}
	}
	for n, c := range regular {
		if err := tw.WriteHeader(&tar.Header{Name: n, Typeflag: tar.TypeReg, Mode: 0o644, Size: int64(len(c))}); err != nil {
			t.Fatal(err)
		}
		if _, err := io.WriteString(tw, c); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gw.Close(); err != nil {
		t.Fatal(err)
	}
	return p
}

func findEntry(entries []ArchiveEntry, rel string) (ArchiveEntry, bool) {
	for _, e := range entries {
		if e.RelPath == rel {
			return e, true
		}
	}
	return ArchiveEntry{}, false
}

func TestScanArchive(t *testing.T) {
	regular := map[string]string{"top.txt": "hello", "a/b/c.txt": "deep!"}
	unsafe := map[string]string{"../evil.txt": "x", "/abs.txt": "y"}

	for _, tc := range []struct {
		name string
		path string
	}{
		{"zip", buildZip(t, regular, unsafe, []string{"a/b/"})},
		{"targz", buildTarGz(t, regular, []string{"a/b/"})},
	} {
		t.Run(tc.name, func(t *testing.T) {
			entries, err := ScanArchive(tc.path)
			if err != nil {
				t.Fatal(err)
			}
			for _, e := range entries {
				if strings.Contains(e.RelPath, "..") || strings.HasPrefix(e.RelPath, "/") {
					t.Fatalf("unsafe entry leaked: %q", e.RelPath)
				}
			}
			if e, ok := findEntry(entries, "top.txt"); !ok || e.IsDir || e.Size != 5 {
				t.Errorf("top.txt: got %+v ok=%v, want regular size 5", e, ok)
			}
			if e, ok := findEntry(entries, "a/b/c.txt"); !ok || e.IsDir || e.Size != 5 {
				t.Errorf("a/b/c.txt: got %+v ok=%v, want regular size 5", e, ok)
			}
			if e, ok := findEntry(entries, "a/b"); !ok || !e.IsDir {
				t.Errorf("a/b: got %+v ok=%v, want directory", e, ok)
			}
		})
	}
}

func TestStreamArchiveFiles(t *testing.T) {
	regular := map[string]string{"top.txt": "hello", "a/b/c.txt": "deep!"}

	for _, tc := range []struct {
		name string
		path string
	}{
		{"zip", buildZip(t, regular, map[string]string{"../evil.txt": "x"}, []string{"a/b/"})},
		{"targz", buildTarGz(t, regular, []string{"a/b/"})},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := map[string]string{}
			err := StreamArchiveFiles(tc.path, func(e ArchiveEntry, r io.Reader) error {
				b, err := io.ReadAll(r)
				if err != nil {
					return err
				}
				got[e.RelPath] = string(b)
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != len(regular) {
				t.Fatalf("streamed %d files, want %d: %v", len(got), len(regular), got)
			}
			for n, want := range regular {
				if got[n] != want {
					t.Errorf("entry %q = %q, want %q", n, got[n], want)
				}
			}
			if _, leaked := got["../evil.txt"]; leaked {
				t.Error("zip-slip entry was streamed")
			}
		})
	}
}

func TestTarHardlinksAreSkipped(t *testing.T) {
	p := filepath.Join(t.TempDir(), "links.tar.gz")
	f, err := os.Create(p)
	if err != nil {
		t.Fatal(err)
	}
	gw := gzip.NewWriter(f)
	tw := tar.NewWriter(gw)
	if err := tw.WriteHeader(&tar.Header{Name: "real.txt", Typeflag: tar.TypeReg, Mode: 0o644, Size: 4}); err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(tw, "real"); err != nil {
		t.Fatal(err)
	}
	if err := tw.WriteHeader(&tar.Header{Name: "linked.txt", Typeflag: tar.TypeLink, Linkname: "real.txt", Mode: 0o644}); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	entries, err := ScanArchive(p)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := findEntry(entries, "linked.txt"); ok {
		t.Fatal("hardlink should not be returned by ScanArchive")
	}

	streamed := map[string]bool{}
	err = StreamArchiveFiles(p, func(e ArchiveEntry, r io.Reader) error {
		streamed[e.RelPath] = true
		_, err := io.Copy(io.Discard, r)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if streamed["linked.txt"] {
		t.Fatal("hardlink should not be streamed")
	}
	if !streamed["real.txt"] {
		t.Fatal("regular file should be streamed")
	}
}
