package file

import (
	"context"
	"errors"
	"io"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"TDrive/backend/projection"
	"TDrive/backend/tgclient"
)

func TestRunImportWithFolderMergeReusesFoldersAndHandlesFileConflicts(t *testing.T) {
	for _, tc := range []struct {
		policy  string
		uploads int
		skipped int
	}{
		{policy: "skip", uploads: 2, skipped: 1},
		{policy: "keep-both", uploads: 3},
	} {
		t.Run(tc.policy, func(t *testing.T) {
			svc, db, fakeTG, _ := newTestService(t)
			svc.CreateFolder = testFolderCreator(db)
			old := filepath.Join(t.TempDir(), "Collection")
			mkfile(t, filepath.Join(old, "Café", "Report.txt"), "original")
			mkfile(t, filepath.Join(old, "Keep.txt"), "untouched")
			if err := svc.RunImport(t.Context(), personalChannelID, []string{old}, "", false, false); err != nil {
				t.Fatalf("initial import: %v", err)
			}
			collection := folderIDByName(t, db, personalChannelID, "", "Collection")
			existing := folderIDByName(t, db, personalChannelID, collection, "Café")
			_, before, err := projection.ListFolderContents(db, personalChannelID, existing)
			if err != nil || len(before) != 1 {
				t.Fatalf("seeded nested files = %+v, error %v", before, err)
			}
			startedWith := len(fakeTG.SentFiles())
			events := &importTestEventRecorder{}
			svc.Events = events

			root := filepath.Join(t.TempDir(), "collection")
			mkfile(t, filepath.Join(root, "CAFE\u0301", "report.TXT"), "incoming")
			mkfile(t, filepath.Join(root, "CAFE\u0301", "new.txt"), "addition")
			mkfile(t, filepath.Join(root, "Another", "other.txt"), "another branch")
			if err := svc.RunImportWithFolderMerge(t.Context(), personalChannelID, []string{root}, "", false, false, tc.policy); err != nil {
				t.Fatalf("merge import: %v", err)
			}
			if got := childFolderNames(t, db, personalChannelID, ""); !slices.Equal(got, []string{"Collection"}) {
				t.Errorf("root folders = %v, want existing Collection only", got)
			}
			if got := childFolderNames(t, db, personalChannelID, collection); !slices.Equal(got, []string{"Another", "Café"}) {
				t.Errorf("merged subfolders = %v, want Another and existing Café", got)
			}
			another := folderIDByName(t, db, personalChannelID, collection, "Another")
			for file, wantParent := range map[string]string{"Keep.txt": collection, "new.txt": existing, "other.txt": another} {
				if got := fileParentID(t, db, personalChannelID, file); got != wantParent {
					t.Errorf("parent of %s = %q, want %q", file, got, wantParent)
				}
			}
			_, after, err := projection.ListFolderContents(db, personalChannelID, existing)
			if err != nil || len(after) != 3-tc.skipped ||
				!slices.ContainsFunc(after, func(f projection.FileSlim) bool { return f.MsgID == before[0].MsgID && f.Name == "Report.txt" }) {
				t.Errorf("merged files = %+v, error %v, want original retained and %d total", after, err, 3-tc.skipped)
			}
			if uploads := len(fakeTG.SentFiles()) - startedWith; uploads != tc.uploads {
				t.Errorf("new Telegram uploads = %d, want %d", uploads, tc.uploads)
			}
			if payload, ok := events.lastPayload("import_complete"); !ok || payload["uploaded"] != tc.uploads ||
				payload["scheduled"] != tc.uploads || payload["conflictsSkipped"] != tc.skipped || payload["failed"] != 0 {
				t.Errorf("completion = %+v, present %v, want %d uploaded and %d skipped", payload, ok, tc.uploads, tc.skipped)
			}
			if progress, ok := events.lastPayload("import_upload_progress"); !ok || progress["total"] != 3 ||
				progress["conflictsSkipped"] != tc.skipped || progress["progress"] != float64(100) {
				t.Errorf("progress = %+v, present %v, want all 3 files settled including %d skipped", progress, ok, tc.skipped)
			}
		})
	}
}

func TestRunImportWithFolderMergeSkipsSameNameFromEarlierSelectedFolder(t *testing.T) {
	svc, db, fakeTG, _ := newTestService(t)
	svc.CreateFolder = testFolderCreator(db)
	one := filepath.Join(t.TempDir(), "Collection")
	two := filepath.Join(t.TempDir(), "Collection")
	mkfile(t, filepath.Join(one, "report.txt"), "first")
	mkfile(t, filepath.Join(two, "REPORT.TXT"), "second")

	if err := svc.RunImportWithFolderMerge(t.Context(), personalChannelID, []string{one, two}, "", false, false, "skip"); err != nil {
		t.Fatalf("merge import: %v", err)
	}
	if files := fakeTG.SentFiles(); len(files) != 1 || files[0].Name != "report.txt" {
		t.Errorf("Telegram uploads = %+v, want first file only", files)
	}
}

func TestRunImportWithFolderMergeMatchesFolderNamesTrimmedByCreate(t *testing.T) {
	svc, db, _, _ := newTestService(t)
	svc.CreateFolder = testFolderCreator(db)
	photos, err := svc.CreateFolder(t.Context(), personalChannelID, "Photos", "")
	if err != nil {
		t.Fatal(err)
	}
	nested, err := svc.CreateFolder(t.Context(), personalChannelID, "Inside", photos)
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(t.TempDir(), " Photos")
	mkfile(t, filepath.Join(root, " Inside", "addition.txt"), "addition")

	if err := svc.RunImportWithFolderMerge(t.Context(), personalChannelID, []string{root}, "", false, false, "skip"); err != nil {
		t.Fatalf("merge import: %v", err)
	}
	if got := fileParentID(t, db, personalChannelID, "addition.txt"); got != nested {
		t.Errorf("addition parent = %q, want existing Inside %q", got, nested)
	}
}

type importRejectedThenUnconfirmedClient struct{ *batchFailureClient }

func (c importRejectedThenUnconfirmedClient) SendFileWithRandomID(ctx context.Context, peer tgclient.InputPeer, source io.Reader, name, caption string, size int64, progress func(int64, int64), randomID int64) (tgclient.SendFileResult, error) {
	if name == "refused.txt" {
		return tgclient.SendFileResult{}, errors.New("upload refused")
	}
	return c.batchFailureClient.SendFileWithRandomID(ctx, peer, source, name, caption, size, progress, randomID)
}

func TestRunImportWithFolderMergeStopsOnUnconfirmedSendAfterEarlierFailure(t *testing.T) {
	svc, db, fakeTG, _ := newTestService(t)
	svc.CreateFolder = testFolderCreator(db)
	svc.MaxConcurrentUploads = 1
	svc.TG = importRejectedThenUnconfirmedClient{&batchFailureClient{
		Fake: fakeTG, name: "lost.txt", accept: true, err: tgclient.ErrSendOutcomeUnknown,
	}}
	refused := filepath.Join(t.TempDir(), "refused.txt")
	first := filepath.Join(t.TempDir(), "Collection")
	second := filepath.Join(t.TempDir(), "Collection")
	mkfile(t, refused, "refused")
	mkfile(t, filepath.Join(first, "lost.txt"), "Telegram accepted this")
	mkfile(t, filepath.Join(second, "LOST.txt"), "later name conflict")

	err := svc.RunImportWithFolderMerge(t.Context(), personalChannelID, []string{refused, first, second}, "", false, false, "skip")
	if !errors.Is(err, tgclient.ErrSendOutcomeUnknown) {
		t.Errorf("import error = %v, want unconfirmed acceptance", err)
	}
	if files := fakeTG.SentFiles(); len(files) != 1 || files[0].Name != "lost.txt" {
		t.Errorf("Telegram uploads = %+v, want only the unconfirmed file", files)
	}
}

func TestRunImportWithFolderMergeLeavesFileBlockingSubfolderAndReportsIt(t *testing.T) {
	svc, db, fakeTG, _ := newTestService(t)
	svc.CreateFolder = testFolderCreator(db)
	old := filepath.Join(t.TempDir(), "Collection")
	mkfile(t, filepath.Join(old, "Reports"), "existing file")
	if err := svc.RunImport(t.Context(), personalChannelID, []string{old}, "", false, false); err != nil {
		t.Fatalf("initial import: %v", err)
	}
	events := &importTestEventRecorder{}
	svc.Events = events
	root := filepath.Join(t.TempDir(), "Collection")
	mkfile(t, filepath.Join(root, "Reports", "new.txt"), "blocked")
	mkfile(t, filepath.Join(root, "other.txt"), "unblocked")

	if err := svc.RunImportWithFolderMerge(t.Context(), personalChannelID, []string{root}, "", false, false, "skip"); err != nil {
		t.Fatalf("merge import: %v", err)
	}
	collection := folderIDByName(t, db, personalChannelID, "", "Collection")
	if got := childFolderNames(t, db, personalChannelID, collection); len(got) != 0 {
		t.Errorf("created folders = %v, want none", got)
	}
	if files := fakeTG.SentFiles(); len(files) != 2 || files[0].Name != "Reports" || files[1].Name != "other.txt" {
		t.Errorf("Telegram uploads = %+v, want existing Reports and new other.txt", files)
	}
	if payload, ok := events.lastPayload("import_complete"); !ok || payload["errorCount"] != 1 {
		t.Errorf("completion = %+v, present %v, want one reported conflict", payload, ok)
	} else if reasons, _ := payload["errors"].([]string); len(reasons) != 1 || !strings.Contains(reasons[0], "a file named \"Reports\" already exists") {
		t.Errorf("completion errors = %v, want the blocking filename", reasons)
	}
}

func TestRunImportWithFolderMergeRejectsUnknownPolicyBeforeImport(t *testing.T) {
	svc, db, fakeTG, _ := newTestService(t)
	svc.CreateFolder = testFolderCreator(db)
	root := filepath.Join(t.TempDir(), "Collection")
	mkfile(t, filepath.Join(root, "report.txt"), "incoming")

	for _, option := range []string{"", "replace"} {
		if err := svc.RunImportWithFolderMerge(t.Context(), personalChannelID, []string{root}, "", false, false, option); err == nil {
			t.Errorf("merge import accepted conflict policy %q", option)
		}
	}
	if got := childFolderNames(t, db, personalChannelID, ""); len(got) != 0 {
		t.Errorf("created folders = %v, want none", got)
	}
	if files := fakeTG.SentFiles(); len(files) != 0 {
		t.Errorf("Telegram uploads = %+v, want none", files)
	}
}
