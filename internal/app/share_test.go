package app

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"TDrive/backend/datadir"
	"TDrive/backend/projection"
	"TDrive/backend/tgclient"
)

type uploadPreparationPeerResolver func(context.Context, int64) (tgclient.InputPeer, error)

func (resolve uploadPreparationPeerResolver) ResolvePeer(ctx context.Context, channelID int64) (tgclient.InputPeer, error) {
	return resolve(ctx, channelID)
}

func TestUploadKeepsDestinationWhenDriveChangesDuringPreparation(t *testing.T) {
	app, db, fake := setupEncryptionAppWithPolicyRefresh(t, nil)
	app.ctx = t.Context()
	app.engine.SetActiveChannelID(testEncryptionChannelID)
	const otherChannelID int64 = 525252
	if err := projection.MigratePersonalChannel(db, otherChannelID); err != nil {
		t.Fatal(err)
	}
	fake.SeedChannel(tgclient.InputPeer{ChannelID: otherChannelID, AccessHash: 8}, "Other drive")
	if err := projection.QueuePartCleanup(db, testEncryptionChannelID, []int64{9000}); err != nil {
		t.Fatal(err)
	}
	svc := app.fileService()
	peers := svc.Peers
	switched := false
	svc.Peers = uploadPreparationPeerResolver(func(ctx context.Context, channelID int64) (tgclient.InputPeer, error) {
		// The orphan sweep performs network preparation before the upload starts.
		// Switching here exercises the same destination ownership as slow staging.
		if !switched {
			switched = true
			app.engine.SetActiveChannelID(otherChannelID)
		}
		return peers.ResolvePeer(ctx, channelID)
	})
	source := filepath.Join(t.TempDir(), "destination.txt")
	if err := os.WriteFile(source, []byte("keep the selected destination"), 0o600); err != nil {
		t.Fatal(err)
	}
	result := app.UploadToDriveFS([]string{source}, []string{""}, false)
	if !result.Result.OK {
		t.Fatalf("upload failed: %+v", result.Result.Error)
	}
	if !switched || app.ActiveChannelID() != otherChannelID {
		t.Fatal("preparation did not switch the selected drive")
	}
	var destination int64
	if err := db.QueryRow(`SELECT channel_id FROM files WHERE name = ?`, "destination.txt").Scan(&destination); err != nil {
		t.Fatal(err)
	}
	if destination != testEncryptionChannelID {
		t.Fatalf("uploaded to channel %d, want original channel %d", destination, testEncryptionChannelID)
	}
}

func TestMobileUploadSourceStagingKeepsOnlyOwnedCopies(t *testing.T) {
	datadir.Set(t.TempDir())
	t.Cleanup(func() { datadir.Set("") })
	cache := t.TempDir()
	name := "large-video.mp4"
	content := []byte("source bytes")

	for _, move := range []bool{false, true} {
		t.Run(map[bool]string{false: "copy", true: "picker move"}[move], func(t *testing.T) {
			pickerDir := filepath.Join(cache, map[bool]string{false: "other", true: "wails-picker"}[move])
			if err := os.MkdirAll(pickerDir, 0o700); err != nil {
				t.Fatal(err)
			}
			source := filepath.Join(pickerDir, name)
			if err := os.WriteFile(source, content, 0o600); err != nil {
				t.Fatal(err)
			}
			if !move {
				// A caller-owned source may grant read access without write access.
				if err := os.Chmod(source, 0o400); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() {
					if err := os.Chmod(source, 0o600); err != nil {
						t.Errorf("restore source permissions: %v", err)
					}
				})
			}
			before, err := os.Lstat(source)
			if err != nil {
				t.Fatal(err)
			}
			staged, err := stageMobileUploadSource(t.Context(), source, before, move)
			if err != nil {
				t.Fatal(err)
			}
			if filepath.Base(staged) != name {
				t.Fatalf("staged name = %q", staged)
			}
			if got, err := os.ReadFile(staged); err != nil || string(got) != string(content) {
				t.Fatalf("staged content = %q, %v", got, err)
			}
			_, sourceErr := os.Stat(source)
			if move && !os.IsNotExist(sourceErr) || !move && sourceErr != nil {
				t.Fatalf("source after staging: %v", sourceErr)
			}
			if err := removeUploadSourceCopy(source); err != nil {
				t.Fatal(err)
			}
			if err := removeUploadSourceCopy(staged); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(staged); !os.IsNotExist(err) {
				t.Fatalf("staged copy remains: %v", err)
			}
		})
	}
}

func TestMobileUploadSourceStagingRejectsCanceledCopy(t *testing.T) {
	datadir.Set(t.TempDir())
	t.Cleanup(func() { datadir.Set("") })
	source := filepath.Join(t.TempDir(), "large-video.mp4")
	if err := os.WriteFile(source, []byte("source bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	before, err := os.Lstat(source)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := stageMobileUploadSource(ctx, source, before, false); err == nil {
		t.Fatal("canceled stage succeeded")
	}
	root, err := durableUploadSourceRoot()
	if err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 0 {
		t.Fatalf("orphan stage directories = %d, %v", len(entries), err)
	}
}

func TestUniqueDownloadPathNumbersDuplicates(t *testing.T) {
	dir := t.TempDir()
	if got, want := uniqueDownloadPath(dir, "plan.pdf"), filepath.Join(dir, "plan.pdf"); got != want {
		t.Fatalf("free name = %q, want %q", got, want)
	}
	for _, name := range []string{"plan.pdf", "plan (2).pdf"} {
		if err := os.WriteFile(filepath.Join(dir, name), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if got, want := uniqueDownloadPath(dir, "plan.pdf"), filepath.Join(dir, "plan (3).pdf"); got != want {
		t.Fatalf("numbered name = %q, want %q", got, want)
	}
	// A path in the name never escapes the downloads folder.
	if got, want := uniqueDownloadPath(dir, "../escape.txt"), filepath.Join(dir, "escape.txt"); got != want {
		t.Fatalf("basename = %q, want %q", got, want)
	}
}

func TestUnderAcceptsOnlyFilesBelowARoot(t *testing.T) {
	root := filepath.Join(string(filepath.Separator), "data", "TDrive")
	for _, tc := range []struct {
		name string
		path string
		want bool
	}{
		{"a file in the root", filepath.Join(root, "plan.pdf"), true},
		{"a file further down", filepath.Join(root, "Downloads", "plan.pdf"), true},
		{"the root itself", root, false},
		{"the parent", filepath.Dir(root), false},
		{"a sibling with a shared prefix", root + "-other", false},
		{"an escape", filepath.Join(root, "..", "plan.pdf"), false},
		{"no root at all", "plan.pdf", false},
	} {
		if got := under(root, filepath.Clean(tc.path)); got != tc.want {
			t.Errorf("%s: under(%q) = %v, want %v", tc.name, tc.path, got, tc.want)
		}
	}
	// A platform with no user-visible folder reports "", which must never
	// turn into a root that accepts everything.
	if under("", filepath.Join(root, "plan.pdf")) {
		t.Error(`under("", ...) accepted a path`)
	}
}

func TestDownloadsDirFallsBackToTheDataDirectory(t *testing.T) {
	root := t.TempDir()
	datadir.Set(root)
	t.Cleanup(func() { datadir.Set("") })
	base, err := datadir.Dir()
	if err != nil {
		t.Fatal(err)
	}

	// visibleStorageDir is empty everywhere but iOS, so everywhere but iOS
	// downloads stay inside the data directory.
	dir, err := downloadsDir()
	if err != nil {
		t.Fatalf("downloadsDir: %v", err)
	}
	if want := filepath.Join(base, "Downloads"); dir != want {
		t.Fatalf("downloadsDir = %q, want %q", dir, want)
	}
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("downloadsDir did not create it: %v", err)
	}
}

func TestShareFileOnlySharesSandboxFiles(t *testing.T) {
	root := t.TempDir()
	datadir.Set(root)
	t.Cleanup(func() { datadir.Set("") })
	base, err := datadir.Dir()
	if err != nil {
		t.Fatal(err)
	}
	app := &App{}

	outside := filepath.Join(t.TempDir(), "outside.txt")
	if err := os.WriteFile(outside, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if res := app.ShareFile(outside); res.OK || res.Error.Code != OperationCodePermissionDenied {
		t.Fatalf("outside path: got %+v, want permission_denied", res)
	}
	if res := app.ShareFile(filepath.Join(base, "Downloads", "missing.txt")); res.OK || res.Error.Code != OperationCodeNotFound {
		t.Fatalf("missing file: got %+v, want not_found", res)
	}
	inside := filepath.Join(base, "Downloads", "plan.pdf")
	if err := os.MkdirAll(filepath.Dir(inside), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(inside, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	// Desktop has no share sheet, so a valid path still fails, but only at
	// the native step: the code is the generic failure, not a denial.
	if res := app.ShareFile(inside); res.OK || res.Error.Code != OperationCodeFailed {
		t.Fatalf("desktop share: got %+v, want operation_failed", res)
	}
}
