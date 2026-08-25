package mountsafe

import (
	"errors"
	"strings"
	"testing"
)

func TestMessageRedactsCapabilityDetails(t *testing.T) {
	secret := "HTTP://LOCALHOST:49152/TDRIVE-0123456789abcdef/"
	got := Message("attach failed for " + secret)
	if ContainsSensitive(got) || strings.Contains(strings.ToLower(got), "tdrive-") {
		t.Fatalf("Message() leaked capability: %q", got)
	}
	if got != fallbackMessage {
		t.Fatalf("Message() = %q, want fallback", got)
	}
}

func TestMessageNormalizesSafeText(t *testing.T) {
	got := Message("  could not\nattach\tTDrive  ")
	if got != "could not attach TDrive" {
		t.Fatalf("Message() = %q", got)
	}
}

func TestSanitizeErrorPreservesSafeErrorsAndRedactsCapabilities(t *testing.T) {
	safe := errors.New("drive is already mounted")
	if got := SanitizeError(safe); !errors.Is(got, safe) {
		t.Fatalf("SanitizeError(safe) = %v, want original error", got)
	}

	unsafe := errors.New("attach failed for http://127.0.0.1:49152/tdrive-secret/")
	got := SanitizeError(unsafe)
	if got == nil || got.Error() != fallbackMessage || errors.Is(got, unsafe) {
		t.Fatalf("SanitizeError(unsafe) = %v", got)
	}
}
