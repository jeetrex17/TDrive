package mountdav

import (
	"errors"
	"os"
	"testing"
)

func TestCleanWritablePathRejectsUnicodeBidiControls(t *testing.T) {
	t.Parallel()

	for _, control := range []rune{
		'\u061c', // ARABIC LETTER MARK
		'\u200e', // LEFT-TO-RIGHT MARK
		'\u200f', // RIGHT-TO-LEFT MARK
		'\u202a', // LEFT-TO-RIGHT EMBEDDING
		'\u202b', // RIGHT-TO-LEFT EMBEDDING
		'\u202c', // POP DIRECTIONAL FORMATTING
		'\u202d', // LEFT-TO-RIGHT OVERRIDE
		'\u202e', // RIGHT-TO-LEFT OVERRIDE
		'\u2066', // LEFT-TO-RIGHT ISOLATE
		'\u2067', // RIGHT-TO-LEFT ISOLATE
		'\u2068', // FIRST STRONG ISOLATE
		'\u2069', // POP DIRECTIONAL ISOLATE
	} {
		path := "/Docs/report" + string(control) + "fdp.exe"
		if _, err := cleanWritablePath(path); !errors.Is(err, os.ErrInvalid) {
			t.Errorf("cleanWritablePath(%q) error = %v, want os.ErrInvalid", path, err)
		}
	}
}

// TestCleanWritablePathAcceptsMacOSJunkFilesAsLegalComponents: these names are
// legal path components at the cleaning layer -- an earlier version of this
// fix rejected them here instead, which risked Finder treating a rejected
// AppleDouble sidecar write as failing the visible file copy. Callers now
// detect and fake-success them separately (see isMacOSJunkPath and
// TestServePUT/MkdirMoveDelete-FakesMacOSJunkPaths in write_test.go) rather
// than having the path layer reject them outright.
func TestCleanWritablePathAcceptsMacOSJunkFilesAsLegalComponents(t *testing.T) {
	t.Parallel()

	for _, path := range []string{
		"/Docs/.DS_Store",
		"/.DS_Store",
		"/Docs/._report.pdf",
		"/Docs/._",
		"/.Spotlight-V100",
		"/.Trashes",
		"/.fseventsd",
		"/.TemporaryItems",
		"/.apdisk",
	} {
		clean, err := cleanWritablePath(path)
		if err != nil {
			t.Errorf("cleanWritablePath(%q) error = %v, want nil", path, err)
			continue
		}
		if !isMacOSJunkPath(clean) {
			t.Errorf("isMacOSJunkPath(%q) = false, want true", clean)
		}
	}
}

func TestIsMacOSJunkPathIgnoresRootAndLegitimateNames(t *testing.T) {
	t.Parallel()

	for _, path := range []string{"/", "/Docs", "/Docs/photo.png", "/Docs/.gitignore"} {
		if isMacOSJunkPath(path) {
			t.Errorf("isMacOSJunkPath(%q) = true, want false", path)
		}
	}
}

func TestCleanWritablePathPreservesLegitimateDotfiles(t *testing.T) {
	t.Parallel()

	for _, path := range []string{
		"/.gitignore",
		"/Docs/.env",
		"/Docs/.hidden-notes.txt",
	} {
		clean, err := cleanWritablePath(path)
		if err != nil {
			t.Errorf("cleanWritablePath(%q) error = %v", path, err)
			continue
		}
		if clean != path {
			t.Errorf("cleanWritablePath(%q) = %q, want unchanged", path, clean)
		}
	}
}

func TestCleanWritablePathPreservesUnicodeJoinControls(t *testing.T) {
	t.Parallel()

	for _, path := range []string{
		"/Docs/family-\U0001f468\u200d\U0001f469\u200d\U0001f467.txt",
		"/Docs/Persian-می\u200cروم.txt",
	} {
		clean, err := cleanWritablePath(path)
		if err != nil {
			t.Errorf("cleanWritablePath(%q) error = %v", path, err)
			continue
		}
		if clean != path {
			t.Errorf("cleanWritablePath(%q) = %q, want join controls preserved", path, clean)
		}
	}
}
