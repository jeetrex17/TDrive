package daemon

import (
	"reflect"
	"testing"

	personaldriveservice "TDrive/backend/services/personaldrive"
)

func TestPersonalDriveSetupFromServiceUsesStringIDs(t *testing.T) {
	got := personalDriveSetupFromService(personaldriveservice.State{
		Status:    personaldriveservice.StatusSelectionRequired,
		ChannelID: 9_007_199_254_740_993,
		Candidates: []personaldriveservice.Candidate{{
			ID: 9_007_199_254_740_993, Title: "TDrive", CreatedAt: 100,
			HasActivity: true, Recommended: true,
		}},
	})
	want := PersonalDriveSetup{
		Status:          personaldriveservice.StatusSelectionRequired,
		ActiveChannelID: "9007199254740993",
		Candidates: []PersonalDriveCandidate{{
			ID: "9007199254740993", Title: "TDrive", CreatedAt: 100,
			HasActivity: true, Recommended: true,
		}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("setup = %#v, want %#v", got, want)
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
