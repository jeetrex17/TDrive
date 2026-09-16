package main

import (
	"os"
	"path/filepath"
	"testing"

	"TDrive/backend/datadir"
)

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
