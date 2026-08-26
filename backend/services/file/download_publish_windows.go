//go:build windows

package file

import "golang.org/x/sys/windows"

func publishDirectoryNoReplace(stagingPath, finalPath string) error {
	from, err := windows.UTF16PtrFromString(stagingPath)
	if err != nil {
		return err
	}
	to, err := windows.UTF16PtrFromString(finalPath)
	if err != nil {
		return err
	}
	return windows.MoveFile(from, to)
}
