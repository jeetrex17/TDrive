package daemon

import (
	"strings"
	"testing"
)

func TestPipeSecurityDescriptorForSID(t *testing.T) {
	t.Parallel()

	const sid = "S-1-5-21-1111111111-2222222222-3333333333-1001"

	descriptor, err := pipeSecurityDescriptorForSID(sid)
	if err != nil {
		t.Fatalf("pipeSecurityDescriptorForSID() error = %v", err)
	}
	if want := "D:P(A;;GA;;;" + sid + ")"; descriptor != want {
		t.Fatalf("pipeSecurityDescriptorForSID() = %q, want %q", descriptor, want)
	}
}

func TestWindowsPipePathForSID(t *testing.T) {
	t.Parallel()

	const sid = "S-1-5-21-1111111111-2222222222-3333333333-1001"

	path, err := windowsPipePathForSID(sid)
	if err != nil {
		t.Fatalf("windowsPipePathForSID() error = %v", err)
	}
	if want := `\\.\pipe\TDrive-daemon-` + sid; path != want {
		t.Fatalf("windowsPipePathForSID() = %q, want %q", path, want)
	}
}

func TestWindowsPipePathForSIDRejectsInvalidSID(t *testing.T) {
	t.Parallel()

	if path, err := windowsPipePathForSID("S-1-5-21)(A;;GA;;;WD"); err == nil {
		t.Fatalf("windowsPipePathForSID() = %q, want error", path)
	}
}

func TestPipeSecurityDescriptorForSIDRejectsInvalidSID(t *testing.T) {
	t.Parallel()

	tooManySubAuthorities := "S-1-5-" + strings.Repeat("1-", 15) + "1"
	testCases := []struct {
		name string
		sid  string
	}{
		{name: "empty", sid: ""},
		{name: "lowercase prefix", sid: "s-1-5-21"},
		{name: "unsupported revision", sid: "S-2-5-21"},
		{name: "missing sub-authority", sid: "S-1-5"},
		{name: "empty component", sid: "S-1-5--21"},
		{name: "non-decimal", sid: "S-1-5-abc"},
		{name: "sddl injection", sid: "S-1-5-21)(A;;GA;;;WD"},
		{name: "authority overflow", sid: "S-1-281474976710656-21"},
		{name: "sub-authority overflow", sid: "S-1-5-4294967296"},
		{name: "too many sub-authorities", sid: tooManySubAuthorities},
	}

	for _, testCase := range testCases {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			if descriptor, err := pipeSecurityDescriptorForSID(testCase.sid); err == nil {
				t.Fatalf("pipeSecurityDescriptorForSID(%q) = %q, want error", testCase.sid, descriptor)
			}
		})
	}
}
