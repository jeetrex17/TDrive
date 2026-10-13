package app

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestFailedUploadRetryRejectsChangedDrive(t *testing.T) {
	a, db, _ := setupEncryptionAppWithPolicyRefresh(t, nil)
	a.ctx = t.Context()
	a.engine.SetActiveChannelID(testEncryptionChannelID)
	result := a.RetryUploadToDriveFS([]string{"unused.txt"}, []string{""}, false, testEncryptionChannelID+1)
	if result.Result.OK {
		t.Fatal("retry accepted a different destination")
	}
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM files`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("retry created %d files", count)
	}
}

func TestFailedUploadRetryDoesNotCancelExistingUpload(t *testing.T) {
	a, _, _ := setupEncryptionAppWithPolicyRefresh(t, nil)
	a.ctx = t.Context()
	a.engine.SetActiveChannelID(testEncryptionChannelID)
	ctx, finish := a.transfers.begin(a.ctx, uploadTransfer)
	t.Cleanup(finish)
	result := a.RetryUploadToDriveFS([]string{"unused.txt"}, []string{""}, false, testEncryptionChannelID)
	if !errors.Is(result.Result.Err(), errUploadBusy) {
		t.Fatalf("retry error = %v, want upload busy", result.Result.Err())
	}
	if ctx.Err() != nil {
		t.Fatal("retry canceled the existing upload")
	}
}

func TestUploadResultTracksFailedSelectionIndexes(t *testing.T) {
	a, db, _ := setupEncryptionAppWithPolicyRefresh(t, nil)
	a.ctx = t.Context()
	a.engine.SetActiveChannelID(testEncryptionChannelID)
	dir := t.TempDir()
	source := filepath.Join(dir, "success.txt")
	if err := os.WriteFile(source, []byte("accepted bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	result := a.UploadToDriveFS([]string{source, filepath.Join(dir, "missing.txt")}, []string{"", ""}, false)
	if result.Result.OK || len(result.Files) != 1 || len(result.Failures) != 1 {
		t.Fatalf("partial upload = %+v", result)
	}
	failure := result.Failures[0]
	if failure.Index != 1 || failure.Name != "missing.txt" {
		t.Fatalf("failure = %+v, want original index 1", failure)
	}
	if !failure.Retryable {
		t.Fatal("missing source before send should be safe after restoring it")
	}
	missing := filepath.Join(dir, "missing.txt")
	if err := os.WriteFile(missing, []byte("restored source"), 0o600); err != nil {
		t.Fatal(err)
	}
	retried := a.RetryUploadToDriveFS([]string{missing}, []string{""}, false, testEncryptionChannelID)
	if !retried.Result.OK || len(retried.Files) != 1 || len(retried.Failures) != 0 {
		t.Fatalf("retry = %+v", retried)
	}
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM files`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("file count = %d, want successful original and retried failure only", count)
	}
}
