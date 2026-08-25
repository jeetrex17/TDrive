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
