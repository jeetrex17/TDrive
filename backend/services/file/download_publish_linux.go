//go:build linux

package file

import "golang.org/x/sys/unix"

func publishDirectoryNoReplace(stagingPath, finalPath string) error {
	return unix.Renameat2(unix.AT_FDCWD, stagingPath, unix.AT_FDCWD, finalPath, unix.RENAME_NOREPLACE)
}
