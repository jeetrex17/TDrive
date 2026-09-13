package auth

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
)

var privateFileRename = os.Rename

func writePrivateFile(path string, data []byte) (retErr error) {
	dirPath := filepath.Dir(path)
	if err := os.MkdirAll(dirPath, privateDirMode); err != nil {
		return fmt.Errorf("create private directory: %w", err)
	}
	if err := os.Chmod(dirPath, privateDirMode); err != nil {
		return fmt.Errorf("secure private directory: %w", err)
	}

	temp, err := os.CreateTemp(dirPath, "."+filepath.Base(path)+"-*.tmp")
	if err != nil {
		return fmt.Errorf("create private temp file: %w", err)
	}
	tempPath := temp.Name()
	tempOpen := true
	removeTemp := true
	defer func() {
		if tempOpen {
			if err := temp.Close(); err != nil {
				retErr = errors.Join(retErr, fmt.Errorf("close private temp file: %w", err))
			}
		}
		if removeTemp {
			if err := os.Remove(tempPath); err != nil && !errors.Is(err, os.ErrNotExist) {
				retErr = errors.Join(retErr, fmt.Errorf("remove private temp file: %w", err))
			}
		}
	}()

	// CreateTemp already uses 0600, but apply and check the required mode before
	// any secret bytes are handed to the operating system.
	if err := temp.Chmod(privateFileMode); err != nil {
		return fmt.Errorf("secure private temp file: %w", err)
	}
	written, err := temp.Write(data)
	if err != nil {
		return fmt.Errorf("write private temp file: %w", err)
	}
	if written != len(data) {
		return fmt.Errorf("write private temp file: %w", io.ErrShortWrite)
	}
	if err := temp.Sync(); err != nil {
		return fmt.Errorf("sync private temp file: %w", err)
	}
	if err := temp.Close(); err != nil {
		tempOpen = false
		return fmt.Errorf("close private temp file: %w", err)
	}
	tempOpen = false

	if err := privateFileRename(tempPath, path); err != nil {
		return fmt.Errorf("replace private file: %w", err)
	}
	removeTemp = false

	if err := syncPrivateFileDirectory(dirPath); err != nil {
		return err
	}
	return nil
}

func syncPrivateFileDirectory(path string) (retErr error) {
	// os.File.Sync has no portable directory equivalent on Windows.
	if runtime.GOOS == "windows" {
		return nil
	}

	dir, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open private file directory: %w", err)
	}
	defer func() {
		if err := dir.Close(); err != nil {
			retErr = errors.Join(retErr, fmt.Errorf("close private file directory: %w", err))
		}
	}()

	if err := dir.Sync(); err != nil {
		return fmt.Errorf("sync private file directory: %w", err)
	}
	return nil
}
