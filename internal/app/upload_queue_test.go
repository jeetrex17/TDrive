package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"TDrive/backend/projection"
	"TDrive/backend/tgclient"
)

func TestQueuedImportRejectsChangedDriveBeforeWriting(t *testing.T) {
	for _, expected := range []int64{0, testEncryptionChannelID + 1} {
		t.Run(fmt.Sprintf("expected_%d", expected), func(t *testing.T) {
			a, db, fake := setupEncryptionAppWithPolicyRefresh(t, nil)
			a.ctx = t.Context()
			a.engine.SetActiveChannelID(testEncryptionChannelID)
			result := a.ImportPathsForDrive([]string{t.TempDir()}, "", false, false, expected)
			if result.OK {
				t.Fatal("queued import accepted a changed destination")
			}
			var count int
			if err := db.QueryRow(`SELECT (SELECT COUNT(*) FROM files) + (SELECT COUNT(*) FROM folders)`).Scan(&count); err != nil {
				t.Fatal(err)
			}
			if count != 0 {
				t.Fatalf("refused import created %d files or folders", count)
			}
			history, err := fake.GetHistory(t.Context(), tgclient.InputPeer{ChannelID: testEncryptionChannelID}, 0, 0, 10)
			if err != nil || len(history) != 0 {
				t.Fatalf("refused import remote messages = %d, error = %v", len(history), err)
			}
		})
	}
}

func TestQueuedImportDoesNotCancelExistingUpload(t *testing.T) {
	a, _, _ := setupEncryptionAppWithPolicyRefresh(t, nil)
	a.ctx = t.Context()
	a.engine.SetActiveChannelID(testEncryptionChannelID)
	active, finish := a.transfers.begin(t.Context(), uploadTransfer)
	t.Cleanup(finish)
	result := a.ImportPathsForDrive([]string{t.TempDir()}, "", false, false, testEncryptionChannelID)
	if !errors.Is(result.Err(), errUploadBusy) {
		t.Fatalf("queued import error = %v, want upload busy", result.Err())
	}
	if active.Err() != nil {
		t.Fatal("refused queued import canceled the existing upload")
	}
}

func TestQueuedFolderRejectsChangedDriveBeforeWriting(t *testing.T) {
	for _, expected := range []int64{0, testEncryptionChannelID + 1} {
		t.Run(fmt.Sprintf("expected_%d", expected), func(t *testing.T) {
			a, db, fake := setupEncryptionAppWithPolicyRefresh(t, nil)
			a.ctx = t.Context()
			a.engine.SetActiveChannelID(testEncryptionChannelID)
			if _, err := a.CreateFolderForDrive("queued", "", expected); err == nil {
				t.Fatal("queued folder accepted a changed destination")
			}
			var count int
			if err := db.QueryRow(`SELECT COUNT(*) FROM folders`).Scan(&count); err != nil {
				t.Fatal(err)
			}
			if count != 0 {
				t.Fatalf("refused request created %d folders", count)
			}
			history, err := fake.GetHistory(t.Context(), tgclient.InputPeer{ChannelID: testEncryptionChannelID}, 0, 0, 10)
			if err != nil || len(history) != 0 {
				t.Fatalf("refused folder remote messages = %d, error = %v", len(history), err)
			}
		})
	}
}

func TestQueuedImportKeepsDestinationDuringPreparation(t *testing.T) {
	a, db, fake := setupEncryptionAppWithPolicyRefresh(t, nil)
	a.ctx = t.Context()
	a.engine.SetActiveChannelID(testEncryptionChannelID)
	const otherChannelID int64 = 525252
	if err := projection.MigratePersonalChannel(db, otherChannelID); err != nil {
		t.Fatal(err)
	}
	fake.SeedChannel(tgclient.InputPeer{ChannelID: otherChannelID, AccessHash: 8}, "Other drive")
	if err := projection.QueuePartCleanup(db, testEncryptionChannelID, []int64{9000}); err != nil {
		t.Fatal(err)
	}
	svc := a.fileService()
	peers := svc.Peers
	switched := false
	svc.Peers = uploadPreparationPeerResolver(func(ctx context.Context, channelID int64) (tgclient.InputPeer, error) {
		if !switched {
			switched = true
			a.engine.SetActiveChannelID(otherChannelID)
		}
		return peers.ResolvePeer(ctx, channelID)
	})
	root := filepath.Join(t.TempDir(), "Photos")
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "destination.txt"), []byte("keep the queued destination"), 0o600); err != nil {
		t.Fatal(err)
	}
	result := a.ImportPathsForDrive([]string{root}, "", false, false, testEncryptionChannelID)
	if !result.OK {
		t.Fatalf("queued import failed: %+v", result.Error)
	}
	if !switched || a.ActiveChannelID() != otherChannelID {
		t.Fatal("preparation did not switch the selected drive")
	}
	var destination int64
	var folderName string
	if err := db.QueryRow(`SELECT f.channel_id, d.name FROM files f JOIN folders d ON d.channel_id = f.channel_id AND d.id = f.parent_id WHERE f.name = ?`, "destination.txt").Scan(&destination, &folderName); err != nil {
		t.Fatal(err)
	}
	if destination != testEncryptionChannelID || folderName != "Photos" {
		t.Fatalf("imported destination = (%d, %q), want (%d, Photos)", destination, folderName, testEncryptionChannelID)
	}
	if a.transfers.keepAwake {
		t.Fatal("completed queued import retained the upload lease")
	}
}

func TestReleaseUploadSourcesOnlyRemovesOwnedPickerCopies(t *testing.T) {
	app := &App{}
	dir := t.TempDir()
	owned := filepath.Join(dir, "picker-copy.txt")
	caller := filepath.Join(dir, "caller-owned.txt")
	consumed := filepath.Join(dir, "durable-source.txt")
	for _, path := range []string{owned, caller, consumed} {
		if err := os.WriteFile(path, []byte("source bytes"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	app.transfers.rememberPickerSources([]string{owned, consumed})
	if !app.transfers.takePickerSource(consumed) {
		t.Fatal("did not transfer picker ownership to the resumable source")
	}
	result := app.ReleaseUploadSources([]string{caller, consumed, owned})
	if !result.OK {
		t.Fatalf("release picker sources: %+v", result.Error)
	}
	if _, err := os.Stat(owned); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("owned picker copy remains: %v", err)
	}
	for _, path := range []string{caller, consumed} {
		if got, err := os.ReadFile(path); err != nil || string(got) != "source bytes" {
			t.Fatalf("non-picker source changed: bytes = %q, error = %v", got, err)
		}
	}
	if app.transfers.takePickerSource(owned) {
		t.Fatal("released picker copy retained ownership")
	}
	if result := app.ReleaseUploadSources([]string{owned}); !result.OK {
		t.Fatalf("repeated release failed: %+v", result.Error)
	}
}

func TestReleaseUploadSourcesDoesNotFollowOwnedSymlink(t *testing.T) {
	app := &App{}
	dir := t.TempDir()
	target := filepath.Join(dir, "caller-owned.txt")
	owned := filepath.Join(dir, "picker-copy.txt")
	if err := os.WriteFile(target, []byte("keep original"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, owned); err != nil {
		t.Fatal(err)
	}
	app.transfers.rememberPickerSources([]string{owned})
	if result := app.ReleaseUploadSources([]string{owned}); !result.OK {
		t.Fatalf("release picker symlink: %+v", result.Error)
	}
	if got, err := os.ReadFile(target); err != nil || string(got) != "keep original" {
		t.Fatalf("release touched symlink target: bytes = %q, error = %v", got, err)
	}
}

func TestFinishedUploadPrunesPickerOwnershipAndKeepsRetrySources(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(fmt.Sprintf("failed_%t", fail), func(t *testing.T) {
			a, _, _ := setupEncryptionAppWithPolicyRefresh(t, nil)
			a.ctx = t.Context()
			a.engine.SetActiveChannelID(testEncryptionChannelID)
			source := filepath.Join(t.TempDir(), "source.txt")
			if err := os.WriteFile(source, []byte("keep for retry"), 0o600); err != nil {
				t.Fatal(err)
			}
			a.transfers.rememberPickerSources([]string{source})
			if fail {
				a.fileService().ActorID = func(context.Context) (int64, error) {
					return 0, errors.New("actor is unavailable")
				}
			}
			result := a.RetryUploadToDriveFS([]string{source}, []string{""}, false, testEncryptionChannelID)
			if result.Result.OK == fail {
				t.Fatalf("finished upload success = %t, wanted %t", result.Result.OK, !fail)
			}
			if fail && (len(result.Failures) != 1 || !result.Failures[0].Retryable) {
				t.Fatalf("preflight failure = %+v, want safe retry", result.Failures)
			}
			if a.transfers.takePickerSource(source) {
				t.Fatal("finished upload retained queued picker ownership")
			}
			if got, err := os.ReadFile(source); err != nil || string(got) != "keep for retry" {
				t.Fatalf("finished upload lost source: bytes = %q, error = %v", got, err)
			}
		})
	}
}
