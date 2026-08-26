package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestPersonalDriveCandidateJSONPreservesIDAndOmitsAccessHash(t *testing.T) {
	candidate := PersonalDriveCandidate{
		ID:          "9007199254740993",
		Title:       "TDrive",
		CreatedAt:   123,
		HasActivity: true,
		Recommended: true,
	}

	payload, err := json.Marshal(candidate)
	if err != nil {
		t.Fatalf("marshal candidate: %v", err)
	}
	if !strings.Contains(string(payload), `"id":"9007199254740993"`) {
		t.Fatalf("candidate ID lost precision: %s", payload)
	}
	if strings.Contains(strings.ToLower(string(payload)), "access") {
		t.Fatalf("candidate exposed backend-only access data: %s", payload)
	}
}

func TestSelectPersonalDriveRejectsNonCanonicalIDsAtBoundary(t *testing.T) {
	app := &App{}
	for _, channelID := range []string{"", "0", "-1", "+1", "01", " 1", "1 ", "1.0", "9223372036854775808"} {
		t.Run(channelID, func(t *testing.T) {
			err := app.SelectPersonalDrive(channelID)
			if err == nil || err.Error() != "invalid channel id" {
				t.Fatalf("SelectPersonalDrive(%q) error = %v, want invalid channel id", channelID, err)
			}
		})
	}
}
