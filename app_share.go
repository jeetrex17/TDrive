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

// Phones have no save dialog: downloads land in the app sandbox and leave
// through the OS share sheet (Files, AirDrop, Drive, mail, ...). Desktop keeps
// its dialogs and never calls into the share path.

// downloadsDir is the sandbox folder phone downloads are written to.
func downloadsDir() (string, error) {
	base, err := datadir.Dir()
	if err != nil {
		return "", err
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

// ShareFile opens the platform share sheet for a file TDrive wrote into its
// own data directory. Anything outside it is refused so the webview cannot
// hand arbitrary files to other apps. Desktop reports unsupported.
func (a *App) ShareFile(path string) OperationResult {
	base, err := datadir.Dir()
	if err != nil {
		return operationFailure(err)
	}
	clean := filepath.Clean(path)
	rel, err := filepath.Rel(base, clean)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return operationFailure(fmt.Errorf("%w: only files inside TDrive's data directory can be shared", fs.ErrPermission))
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
func fileURL(path string) string {
	return (&url.URL{Scheme: "file", Path: path}).String()
}
