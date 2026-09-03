package file

import (
	"fmt"
	"os"
	"path/filepath"
)

// createFolderDownloadStaging creates a private sibling staging directory
// beneath the chosen destination parent and returns the resolved parent with
// the staging path. The whole tree is assembled there and published with one
// rename, so a failed or canceled download never leaves a partial folder at
// the final path. MkdirTemp gives the directory a random name and mode 0700,
// which is all the isolation a single-user desktop app needs.
func createFolderDownloadStaging(parentPath string) (resolvedParent, stagingPath string, err error) {
	resolvedParent, err = filepath.Abs(parentPath)
	if err != nil {
		return "", "", fmt.Errorf("resolve download destination: %w", err)
	}
	resolvedParent, err = filepath.EvalSymlinks(resolvedParent)
	if err != nil {
		return "", "", fmt.Errorf("resolve download destination: %w", err)
	}
	stagingPath, err = os.MkdirTemp(resolvedParent, ".tdrive-folder-download-*")
	if err != nil {
		return "", "", err
	}
	return resolvedParent, stagingPath, nil
}

// publishFolderDownload moves the completed staging tree to finalPath without
// replacing anything already there. The destination was checked before the
// download started; checking again here narrows the window in which a folder
// created mid-download could be merged into or clobbered. A conflict surfaces
// as os.ErrExist so callers can report "already exists" rather than a disk
// error.
func publishFolderDownload(stagingPath, finalPath string) error {
	if _, err := os.Lstat(finalPath); err == nil {
		return &os.LinkError{Op: "rename", Old: stagingPath, New: finalPath, Err: os.ErrExist}
	} else if !os.IsNotExist(err) {
		return err
	}
	return os.Rename(stagingPath, finalPath)
}
