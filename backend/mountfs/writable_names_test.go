package mountfs

import (
	"errors"
	"strings"
	"testing"
)

func TestNormalizeWritableNameAcceptsPortableUnicodeAndReturnsNFC(t *testing.T) {
	t.Parallel()

	normalized, err := NormalizeWritableName("Cafe\u0301.txt")
	if err != nil {
		t.Fatalf("NormalizeWritableName() error = %v", err)
	}
	if normalized != "Caf\u00e9.txt" {
		t.Fatalf("NormalizeWritableName() = %q, want NFC", normalized)
	}
	if err := ValidateWritableName("Cafe\u0301.txt"); err != nil {
		t.Fatalf("ValidateWritableName() error = %v", err)
	}
}

func TestNameKeyUsesOneCaseInsensitiveUnicodeNamespace(t *testing.T) {
	t.Parallel()

	keys := []string{
		NameKey("Caf\u00e9.TXT"),
		NameKey("CAFE\u0301.txt"),
		NameKey("caf\u00e9.txt"),
	}
	for index := 1; index < len(keys); index++ {
		if keys[index] != keys[0] {
			t.Fatalf("NameKey variants = %#v, want one canonical namespace", keys)
		}
	}
}

func TestNormalizeWritableNameRejectsNonPortableWindowsNames(t *testing.T) {
	t.Parallel()

	tests := []string{
		"",
		".",
		"..",
		"CON",
		"nul.txt",
		"COM1.log",
		"LPT\u00b9.txt",
		"bad<name>.txt",
		"bad/name.txt",
		`bad\name.txt`,
		"trailing.",
		"trailing ",
		"control\u0085.txt",
		string([]byte{0xff, 'a'}),
		strings.Repeat("a", maxPortableNameBytes+1),
	}
	for _, name := range tests {
		name := name
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if _, err := NormalizeWritableName(name); !errors.Is(err, ErrInvalidName) {
				t.Fatalf("NormalizeWritableName(%q) error = %v, want ErrInvalidName", name, err)
			}
			if err := ValidateWritableName(name); !errors.Is(err, ErrInvalidName) {
				t.Fatalf("ValidateWritableName(%q) error = %v, want ErrInvalidName", name, err)
			}
		})
	}
}

func TestWritableNameValidationDoesNotChangeLegacyReadAliases(t *testing.T) {
	t.Parallel()

	if _, err := NormalizeWritableName("CON.txt"); !errors.Is(err, ErrInvalidName) {
		t.Fatalf("NormalizeWritableName(CON.txt) error = %v, want ErrInvalidName", err)
	}
	if got := portableName("CON.txt"); got != "_CON.txt" {
		t.Fatalf("legacy portableName(CON.txt) = %q, want existing read alias", got)
	}
}

func FuzzNormalizeWritableNameProperties(f *testing.F) {
	for _, seed := range []string{
		"report.txt",
		"Cafe\u0301.txt",
		"CON",
		"bad<name>.txt",
		"trailing. ",
		string([]byte{0xff, 'a'}),
	} {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, value string) {
		normalized, err := NormalizeWritableName(value)
		if err != nil {
			if !errors.Is(err, ErrInvalidName) {
				t.Fatalf("NormalizeWritableName(%q) error = %v, want ErrInvalidName", value, err)
			}
			return
		}
		if normalized == "" {
			t.Fatal("NormalizeWritableName() returned an empty valid name")
		}
		if err := ValidateWritableName(normalized); err != nil {
			t.Fatalf("normalized value %q is not valid: %v", normalized, err)
		}
		if NameKey(normalized) == "" {
			t.Fatal("NameKey() returned an empty key for a valid name")
		}
	})
}
