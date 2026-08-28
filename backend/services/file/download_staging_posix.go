//go:build !windows

package file

import (
	"fmt"
	"os"
	"path/filepath"
)

type posixFolderDownloadStaging struct {
	parentPath string
	path       string
}

func createPrivateFolderDownloadStaging(parentPath string) (folderDownloadStaging, error) {
	absoluteParent, err := filepath.Abs(parentPath)
	if err != nil {
		return nil, fmt.Errorf("resolve download destination: %w", err)
	}
	resolvedParent, err := filepath.EvalSymlinks(absoluteParent)
	if err != nil {
		return nil, fmt.Errorf("resolve download destination: %w", err)
	}
	if err := validatePrivateNamespaceChain(resolvedParent); err != nil {
		return nil, err
	}
	path, err := os.MkdirTemp(resolvedParent, ".tdrive-folder-download-*")
	if err != nil {
		return nil, err
	}
	if err := os.Chmod(path, 0o700); err != nil {
		_ = os.Remove(path)
		return nil, err
	}
	return &posixFolderDownloadStaging{parentPath: resolvedParent, path: path}, nil
}

func validatePrivateNamespaceChain(path string) error {
	for current := path; ; current = filepath.Dir(current) {
		info, err := os.Lstat(current)
		if err != nil {
			return fmt.Errorf("inspect download destination %q: %w", current, err)
		}
		if !info.IsDir() {
			return fmt.Errorf("download destination component %q is not a directory", current)
		}
		writableByOthers := info.Mode().Perm()&0o022 != 0
		if writableByOthers && info.Mode()&os.ModeSticky == 0 {
			return fmt.Errorf(
				"download destination %q is writable by other users; choose a private directory",
				current,
			)
		}
		next := filepath.Dir(current)
		if next == current {
			return nil
		}
	}
}

func (s *posixFolderDownloadStaging) ParentPath() string {
	return s.parentPath
}

func (s *posixFolderDownloadStaging) Path() string {
	return s.path
}

func (s *posixFolderDownloadStaging) PublishNoReplace(finalPath string) error {
	return publishDirectoryNoReplace(s.path, finalPath)
}

func (*posixFolderDownloadStaging) PrepareCleanup() error {
	return nil
}

func (*posixFolderDownloadStaging) Close() error {
	return nil
}
