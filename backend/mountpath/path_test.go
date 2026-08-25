package mountpath

import (
	"errors"
	"strings"
	"testing"
)

func TestParseAbsoluteNormalizesBeforeApplyingLimits(t *testing.T) {
	t.Parallel()

	decomposed := strings.Repeat("e\u0301", 81)
	normalized, components, err := ParseAbsolute("/Docs/"+decomposed, Options{})
	if err != nil {
		t.Fatalf("ParseAbsolute() error = %v", err)
	}
	composed := strings.Repeat("\u00e9", 81)
	if normalized != "/Docs/"+composed {
		t.Fatalf("ParseAbsolute() path = %q, want NFC path", normalized)
	}
	if len(components) != 2 || components[0] != "Docs" || components[1] != composed {
		t.Fatalf("ParseAbsolute() components = %#v", components)
	}
}

func TestParseAbsoluteRejectsUnsafeStructureAndBounds(t *testing.T) {
	t.Parallel()

	overlongPath := "/" + strings.Repeat("a/", MaxPathBytes/2)
	tests := []string{
		"",
		"relative",
		string([]byte{'/', 0xff}),
		"/nul\x00name",
		`/back\slash`,
		"/double//slash",
		"/dot/./name",
		"/dotdot/../name",
		"/" + strings.Repeat("a", MaxComponentBytes+1),
		overlongPath,
	}
	for _, value := range tests {
		value := value
		t.Run(value, func(t *testing.T) {
			t.Parallel()
			if _, _, err := ParseAbsolute(value, Options{}); !errors.Is(err, ErrInvalid) {
				t.Fatalf("ParseAbsolute(%q) error = %v, want ErrInvalid", value, err)
			}
		})
	}
}

func TestParseAbsoluteOptionsPreserveWebDAVCleaningSemantics(t *testing.T) {
	t.Parallel()

	options := Options{AllowEmptyRoot: true, TrimTrailingSlash: true}
	for input, want := range map[string]string{
		"":       "/",
		"/":      "/",
		"/Docs/": "/Docs",
	} {
		got, _, err := ParseAbsolute(input, options)
		if err != nil {
			t.Fatalf("ParseAbsolute(%q) error = %v", input, err)
		}
		if got != want {
			t.Fatalf("ParseAbsolute(%q) = %q, want %q", input, got, want)
		}
	}
	for _, path := range []string{"//", "/Docs//"} {
		if _, _, err := ParseAbsolute(path, options); !errors.Is(err, ErrInvalid) {
			t.Errorf("ParseAbsolute(%q) error = %v, want ErrInvalid", path, err)
		}
	}
}

func TestComponentPolicyMakesWindowsReservedCheckConfigurable(t *testing.T) {
	t.Parallel()

	structural, _, err := ParseAbsolute("/Legacy/CON.txt", Options{})
	if err != nil || structural != "/Legacy/CON.txt" {
		t.Fatalf("structural ParseAbsolute() = %q, %v", structural, err)
	}

	portable := Options{PortableComponents: true}
	if _, _, err := ParseAbsolute("/Docs/CON.txt", portable); err != nil {
		t.Fatalf("portable ParseAbsolute() without reserved-name check error = %v", err)
	}
	portable.RejectWindowsReserved = true
	for _, path := range []string{"/Docs/CON.txt", "/Docs/com\u00b9.log", "/Docs/LPT9"} {
		if _, _, err := ParseAbsolute(path, portable); !errors.Is(err, ErrInvalid) {
			t.Errorf("ParseAbsolute(%q) error = %v, want ErrInvalid", path, err)
		}
	}
	for _, path := range []string{
		"/Docs/bad?.txt",
		"/Docs/trailing.",
		"/Docs/report\u202efdp.exe",
	} {
		if _, _, err := ParseAbsolute(path, portable); !errors.Is(err, ErrInvalid) {
			t.Errorf("ParseAbsolute(%q) error = %v, want ErrInvalid", path, err)
		}
	}
	if _, _, err := ParseAbsolute("/Docs/Persian-می\u200cروم.txt", portable); err != nil {
		t.Fatalf("ParseAbsolute() join-control error = %v", err)
	}
}

func TestIsWindowsReservedComponentMatchesDeviceAliases(t *testing.T) {
	t.Parallel()

	for _, name := range []string{"CON", "nul.txt", "CLOCK$", "COM1.log", "lpt9", "COM\u00b2"} {
		if !IsWindowsReservedComponent(name) {
			t.Errorf("IsWindowsReservedComponent(%q) = false", name)
		}
	}
	for _, name := range []string{"COM0", "LPT10", "console.txt", "report.txt"} {
		if IsWindowsReservedComponent(name) {
			t.Errorf("IsWindowsReservedComponent(%q) = true", name)
		}
	}
}
