//go:build windows

package file

import (
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows"
)

func publishDirectoryNoReplace(stagingPath, finalPath string) error {
	from, err := windows.UTF16PtrFromString(windowsExtendedPath(stagingPath))
	if err != nil {
		return err
	}
	to, err := windows.UTF16PtrFromString(windowsExtendedPath(finalPath))
	if err != nil {
		return err
	}
	return windows.MoveFile(from, to)
}

func windowsExtendedPath(path string) string {
	if strings.HasPrefix(path, `\\?\`) {
		return path
	}
	if absolute, err := filepath.Abs(path); err == nil {
		path = absolute
	}
	path = filepath.Clean(path)
	if strings.HasPrefix(path, `\\`) {
		return `\\?\UNC\` + strings.TrimPrefix(path, `\\`)
	}
	return `\\?\` + path
}
