package app

import "testing"

func TestRestoreFromTrashRejectsMissingOrSwitchedDrive(t *testing.T) {
	app := &App{}
	for _, tc := range []struct {
		channelID int64
		want      string
	}{
		{0, "invalid channel id"},
		{-1, "invalid channel id"},
		{maxFrontendSafeInteger + 1, "invalid channel id"},
		{42, "active drive changed; switch back before restoring"},
	} {
		result := app.RestoreFromTrash(tc.channelID, "f:101")
		if result.OK || result.Error == nil || result.Error.Message != tc.want {
			t.Errorf("channel %d: result = %+v, want refusal %q", tc.channelID, result, tc.want)
		}
	}
}
