//go:build darwin

package file

import "golang.org/x/sys/unix"

func publishDirectoryNoReplace(stagingPath, finalPath string) error {
	return unix.RenamexNp(stagingPath, finalPath, unix.RENAME_EXCL)
}
