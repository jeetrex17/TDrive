package app

import (
	"errors"
	"testing"

	fileservice "TDrive/backend/services/file"
)

func TestRetryImportedFilesForDrivePreservesDriveAndActiveUpload(t *testing.T) {
	a, _, fake := setupEncryptionAppWithPolicyRefresh(t, nil)
	a.ctx = t.Context()
	a.engine.SetActiveChannelID(testEncryptionChannelID)
	active, finish := a.transfers.begin(t.Context(), uploadTransfer)
	t.Cleanup(finish)
	sources := []fileservice.ImportRetryFile{{Path: "one.txt"}, {Path: "two.txt"}}

	wrongDrive := a.RetryImportedFilesForDrive(sources, false, testEncryptionChannelID+1)
	if wrongDrive.Result.OK || len(wrongDrive.Failures) != 2 || errors.Is(wrongDrive.Result.Err(), errUploadBusy) {
		t.Fatalf("retry in a different drive = %+v, want drive refusal for both inputs", wrongDrive)
	}
	busy := a.RetryImportedFilesForDrive(sources, false, testEncryptionChannelID)
	if !errors.Is(busy.Result.Err(), errUploadBusy) || len(busy.Failures) != 2 {
		t.Fatalf("retry during active upload = %+v, want busy refusal for both inputs", busy)
	}
	for index, failure := range busy.Failures {
		if failure.Index != index || failure.Code != "not_sent" || !failure.Retryable {
			t.Fatalf("busy failure = %+v, want original index %d and safe retry", failure, index)
		}
	}
	if active.Err() != nil || len(fake.SentFiles()) != 0 {
		t.Fatal("a refused folder retry interrupted or sent files in the active upload")
	}
}
