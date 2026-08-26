package file

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

const expectedMaxImportItems = 10_000

var ignoredImportDirectories = []string{
	".git",
	".hg",
	".svn",
	"node_modules",
	"__pycache__",
	".pytest_cache",
	".mypy_cache",
	".ruff_cache",
	".tox",
	".nox",
	".venv",
	".cache",
}

var preservedImportDirectories = []string{
	".github",
	"build",
	"dist",
	"target",
	"vendor",
}

var ignoredImportFiles = []string{
	"compiled.pyc",
	"optimized.pyo",
	"project.tsbuildinfo",
}

func makeImportPolicyTree(t *testing.T) string {
	t.Helper()

	root := filepath.Join(t.TempDir(), "Project")
	for _, name := range ignoredImportDirectories {
		mkfile(t, filepath.Join(root, name, "nested", "ignored.txt"), "ignored")
	}
	for _, name := range ignoredImportFiles {
		mkfile(t, filepath.Join(root, name), "ignored")
	}
	for _, name := range preservedImportDirectories {
		mkfile(t, filepath.Join(root, name, "kept.txt"), "kept")
	}
	mkfile(t, filepath.Join(root, "cache.json"), "kept")
	mkfile(t, filepath.Join(root, "source.go"), "kept")
	return root
}

func makeImportPolicyArchive(t *testing.T) string {
	t.Helper()

	entries := make(map[string]string, len(ignoredImportDirectories)+len(ignoredImportFiles)+len(preservedImportDirectories)+2)
	for _, name := range ignoredImportDirectories {
		entries[name+"/nested/ignored.txt"] = "ignored"
	}
	for _, name := range ignoredImportFiles {
		entries[name] = "ignored"
	}
	for _, name := range preservedImportDirectories {
		entries[name+"/kept.txt"] = "kept"
	}
	entries["cache.json"] = "kept"
	entries["source.go"] = "kept"
	return buildZip(t, entries, nil, nil)
}

func assertImportPolicyPlan(t *testing.T, plan ImportPlan) {
	t.Helper()

	wantFiles := len(preservedImportDirectories) + 2
	if plan.Files != wantFiles {
		t.Errorf("plan.Files = %d, want %d preserved files", plan.Files, wantFiles)
	}
	wantFolders := len(preservedImportDirectories) + 1
	if plan.Folders != wantFolders {
		t.Errorf("plan.Folders = %d, want %d preserved folders", plan.Folders, wantFolders)
	}
	wantIgnored := len(ignoredImportDirectories) + len(ignoredImportFiles)
	if plan.Ignored != wantIgnored {
		t.Errorf("plan.Ignored = %d, want %d ignored roots", plan.Ignored, wantIgnored)
	}
}

func TestPlanImportUsesConservativeIgnorePolicyForFolders(t *testing.T) {
	svc, _, _, _ := newTestService(t)
	assertImportPolicyPlan(t, svc.PlanImport([]string{makeImportPolicyTree(t)}, false, false))
}

func TestPlanImportUsesConservativeIgnorePolicyForArchives(t *testing.T) {
	svc, _, _, _ := newTestService(t)
	assertImportPolicyPlan(t, svc.PlanImport([]string{makeImportPolicyArchive(t)}, false, true))
}

func TestPlanImportIgnorePolicyIsCaseInsensitive(t *testing.T) {
	svc, _, _, _ := newTestService(t)
	root := filepath.Join(t.TempDir(), "Project")
	mkfile(t, filepath.Join(root, ".GIT", "ignored.txt"), "ignored")
	mkfile(t, filepath.Join(root, "BYTECODE.PYC"), "ignored")
	mkfile(t, filepath.Join(root, "cache.JSON"), "kept")

	plan := svc.PlanImport([]string{root}, false, false)
	if plan.Files != 1 || plan.Folders != 1 || plan.Ignored != 2 {
		t.Fatalf("case-insensitive policy plan = %+v, want only cache.JSON preserved", plan)
	}
}

func TestImportIgnorePolicyDoesNotTrimLegitimateNames(t *testing.T) {
	if isIgnoredImportName(" .git", true) {
		t.Fatal("leading-space directory was mistaken for .git")
	}
	if isIgnoredImportName("node_modules ", true) {
		t.Fatal("trailing-space directory was mistaken for node_modules")
	}
}

func TestRunImportUsesThePlannedIgnorePolicy(t *testing.T) {
	tests := []struct {
		name    string
		path    func(*testing.T) string
		extract bool
	}{
		{name: "folder", path: makeImportPolicyTree},
		{name: "archive", path: makeImportPolicyArchive, extract: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			svc, db, fakeTG, _ := newTestService(t)
			svc.CreateFolder = contextTestFolderCreator(db)
			selectedPath := test.path(t)
			plan := svc.PlanImport([]string{selectedPath}, false, test.extract)

			if err := svc.RunImport(context.Background(), personalChannelID, []string{selectedPath}, "", false, test.extract); err != nil {
				t.Fatalf("RunImport: %v", err)
			}
			if got := len(fakeTG.SentFiles()); got != plan.Files {
				t.Fatalf("sent files = %d, want planned %d", got, plan.Files)
			}
			if got := countLiveFiles(t, db, personalChannelID); got != plan.Files {
				t.Fatalf("projected files = %d, want planned %d", got, plan.Files)
			}
		})
	}
}

func TestPlanImportDoesNotIgnoreAnExplicitSelection(t *testing.T) {
	svc, db, fakeTG, _ := newTestService(t)
	svc.CreateFolder = contextTestFolderCreator(db)
	topFile := filepath.Join(t.TempDir(), "manual.pyc")
	mkfile(t, topFile, "chosen explicitly")

	filePlan := svc.PlanImport([]string{topFile}, false, false)
	if filePlan.Files != 1 || filePlan.Ignored != 0 {
		t.Fatalf("explicit file plan = %+v, want one importable file", filePlan)
	}

	topFolder := filepath.Join(t.TempDir(), "__pycache__")
	mkfile(t, filepath.Join(topFolder, "keep.txt"), "chosen explicitly")
	mkfile(t, filepath.Join(topFolder, ".git", "ignored.txt"), "ignored descendant")
	folderPlan := svc.PlanImport([]string{topFolder}, false, false)
	if folderPlan.Files != 1 || folderPlan.Folders != 1 || folderPlan.Ignored != 1 {
		t.Fatalf("explicit folder plan = %+v, want root and keep.txt only", folderPlan)
	}

	if err := svc.RunImport(context.Background(), personalChannelID, []string{topFile, topFolder}, "", false, false); err != nil {
		t.Fatalf("RunImport explicit selections: %v", err)
	}
	if got := len(fakeTG.SentFiles()); got != 2 {
		t.Fatalf("explicitly selected files sent = %d, want 2", got)
	}
}

func TestPlanImportReportsRemoteObjectLimit(t *testing.T) {
	svc, _, _, _ := newTestService(t)
	root := filepath.Join(t.TempDir(), "LargeProject")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	// The selected root itself is one remote folder, so maxImportItems files put
	// the plan exactly one object beyond the safe admission limit.
	for i := 0; i < expectedMaxImportItems; i++ {
		name := filepath.Join(root, fmt.Sprintf("file-%05d.txt", i))
		if err := os.WriteFile(name, nil, 0o644); err != nil {
			t.Fatalf("create item %d: %v", i, err)
		}
	}

	plan := svc.PlanImport([]string{root}, false, false)
	if plan.MaxItems != expectedMaxImportItems {
		t.Fatalf("plan.MaxItems = %d, want %d", plan.MaxItems, expectedMaxImportItems)
	}
	if !plan.LimitExceeded {
		t.Fatalf("plan.LimitExceeded = false for %d remote objects", plan.Files+plan.Folders)
	}
	if got := plan.Files + plan.Folders; got <= plan.MaxItems {
		t.Fatalf("planned remote objects = %d, want more than limit %d", got, plan.MaxItems)
	}

	createCalled := false
	svc.CreateFolder = func(context.Context, int64, string, string) (string, error) {
		createCalled = true
		return "", errors.New("unexpected remote mutation")
	}
	if err := svc.RunImport(context.Background(), personalChannelID, []string{root}, "", false, false); err == nil {
		t.Fatal("RunImport accepted a selection over the remote-object limit")
	}
	if createCalled {
		t.Fatal("RunImport created a remote folder before enforcing the item limit")
	}
}

func TestArchiveImportScanBoundsIgnoredMetadataToo(t *testing.T) {
	archive := buildZip(t, map[string]string{
		"node_modules/a.js": "a",
		"node_modules/b.js": "b",
		"node_modules/c.js": "c",
		"node_modules/d.js": "d",
	}, nil, nil)

	entries, _, err := scanArchiveForImportLimit(context.Background(), archive, 3)
	if !errors.Is(err, errArchiveScanLimit) {
		t.Fatalf("scan error = %v, want archive metadata limit", err)
	}
	if len(entries) != 0 {
		t.Fatalf("retained ignored entries = %d, want 0", len(entries))
	}
}
