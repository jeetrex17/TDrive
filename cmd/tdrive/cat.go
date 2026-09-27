package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
)

func catTempPath() (string, func() error, error) {
	dir, err := os.MkdirTemp("", "tdrive-cat-*")
	if err != nil {
		return "", nil, err
	}
	return filepath.Join(dir, "content"), func() error { return os.RemoveAll(dir) }, nil
}

func runCat(args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("usage: tdrive cat <remote-file>")
	}
	tmpPath, cleanup, err := catTempPath()
	if err != nil {
		return err
	}
	defer func() {
		if err := cleanup(); err != nil {
			fmt.Fprintf(os.Stderr, "warning: could not remove temporary download: %v\n", err)
		}
	}()

	c, err := newDaemonClient()
	if err != nil {
		return err
	}
	if _, err := c.DownloadInDrive(cliCurrentOptions().DriveID, args[0], tmpPath, printTransferEvent); err != nil {
		fmt.Fprintln(os.Stderr)
		return err
	}
	fmt.Fprintln(os.Stderr)

	f, err := os.Open(tmpPath)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	// Unix can unlink the verified file while stdout still reads its open handle.
	_ = os.Remove(tmpPath)
	_, err = io.Copy(os.Stdout, f)
	return err
}
