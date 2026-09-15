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
