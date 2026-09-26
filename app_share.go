package main

import (
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"TDrive/backend/datadir"

	"github.com/wailsapp/wails/v3/pkg/application"
)

// Phones have no save dialog. A download has to land somewhere first and be
// made reachable afterwards, and the two platforms disagree about where:
// iOS has no shared storage at all, so the file goes in the one folder the
// Files app is allowed to list, while Android has a real public Downloads
// folder the host moves it into. Desktop keeps its dialogs and never comes
// through here.

// downloadsDir is the folder phone downloads are written to: the user-visible
// one where the platform has it, and the app's own data directory otherwise.
func downloadsDir() (string, error) {
	base := visibleStorageDir()
	if base == "" {
		dataDir, err := datadir.Dir()
		if err != nil {
			return "", err
		}
		base = dataDir
	}
	dir := filepath.Join(base, "Downloads")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	return dir, nil
}

// uniqueDownloadPath returns dir/name, or "name (2).ext", "name (3).ext", ...
// when the name is taken, the way Finder and Files number duplicates.
func uniqueDownloadPath(dir, name string) string {
	name = filepath.Base(name)
	ext := filepath.Ext(name)
	stem := strings.TrimSuffix(name, ext)
	candidate := filepath.Join(dir, name)
	for n := 2; ; n++ {
		if _, err := os.Lstat(candidate); err != nil {
			return candidate
		}
		candidate = filepath.Join(dir, fmt.Sprintf("%s (%d)%s", stem, n, ext))
	}
}

// chooseDownloadPath is the save-location callback for single-file downloads:
// the sandbox on phones, the platform save dialog everywhere else.
func (a *App) chooseDownloadPath(defaultName string) (string, error) {
	if application.System.IsMobile() {
		dir, err := downloadsDir()
		if err != nil {
			return "", err
		}
		return uniqueDownloadPath(dir, defaultName), nil
	}
	return a.wails.Dialog.SaveFileWithOptions(&application.SaveFileDialogOptions{
		Filename: defaultName,
		Title:    "Save File As...",
	}).PromptForSingleSelection()
}

// chooseDownloadDir is the destination-parent callback for folder downloads.
func (a *App) chooseDownloadDir(defaultName string) (string, error) {
	if application.System.IsMobile() {
		return downloadsDir()
	}
	return a.wails.Dialog.OpenFile().
		CanChooseFiles(false).
		CanChooseDirectories(true).
		SetTitle(fmt.Sprintf("Choose where to save %q", defaultName)).
		PromptForSingleSelection()
}

// under reports whether path is a file somewhere below root. The root itself
// does not count: a directory cannot be shared, and neither can its parent.
func under(root, path string) bool {
	if root == "" {
		return false
	}
	rel, err := filepath.Rel(root, path)
	if err != nil || rel == "." || rel == ".." {
		return false
	}
	return !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// ShareFile opens the platform share sheet for a file TDrive wrote itself.
// Anything outside the folders it owns is refused so the webview cannot hand
// arbitrary files to other apps. Desktop reports unsupported.
func (a *App) ShareFile(path string) OperationResult {
	base, err := datadir.Dir()
	if err != nil {
		return operationFailure(err)
	}
	clean := filepath.Clean(path)
	if !under(base, clean) && !under(visibleStorageDir(), clean) {
		return operationFailure(fmt.Errorf("%w: only files TDrive saved itself can be shared", fs.ErrPermission))
	}
	if _, err := os.Stat(clean); err != nil {
		return operationFailure(err)
	}
	if err := shareFileNative(clean); err != nil {
		return operationFailure(err)
	}
	return operationSuccess()
}

// fileURL renders a sandbox path as the file URL both share sheets accept.
//
//lint:ignore U1000 called only from app_mobile.go, behind //go:build ios || android.
func fileURL(path string) string {
	return (&url.URL{Scheme: "file", Path: path}).String()
}
