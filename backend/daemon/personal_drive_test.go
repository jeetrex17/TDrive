package daemon

import (
	"reflect"
	"testing"

	personaldriveservice "TDrive/backend/services/personaldrive"
)

func TestPersonalDriveCandidatesFromServiceUseStringIDs(t *testing.T) {
	got := personalDriveCandidatesFromService([]personaldriveservice.Candidate{{
		ID: 9_007_199_254_740_993, Title: "TDrive", CreatedAt: 100,
		HasActivity: true, Recommended: true,
	}})
	want := []PersonalDriveCandidate{{
		ID: "9007199254740993", Title: "TDrive", CreatedAt: 100,
		HasActivity: true, Recommended: true,
	}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("candidates = %#v, want %#v", got, want)
	}
	if empty := personalDriveCandidatesFromService(nil); empty == nil || len(empty) != 0 {
		t.Fatalf("nil candidates = %#v, want empty non-nil slice", empty)
	}
}

func TestParsePersonalDriveIDRejectsNonCanonicalValues(t *testing.T) {
	for _, value := range []string{"", "0", "-1", "+8200", "08200", "not-an-id"} {
		if _, err := parsePersonalDriveID(value); err == nil {
			t.Fatalf("parsePersonalDriveID(%q) unexpectedly succeeded", value)
		}
	}
	if got, err := parsePersonalDriveID("8200"); err != nil || got != 8200 {
		t.Fatalf("parsePersonalDriveID = %d, %v", got, err)
	}
}
