//go:build linux

package updater

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
)

type linuxInstaller struct {
	appImage func() string
}

func newPlatformInstaller() Installer {
	return linuxInstaller{appImage: func() string { return os.Getenv("APPIMAGE") }}
}

// Target relies on the APPIMAGE variable the AppImage runtime exports: the
// process itself runs from a temporary squashfs mount, so os.Executable would
// point at something that disappears on exit.
func (l linuxInstaller) Target() (Target, error) {
	path := l.appImage()
	if path == "" {
		return Target{}, &NotInstallableError{Reason: "TDrive isn't running as an AppImage, so it can't replace itself. Download the new version manually."}
	}
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		return Target{}, &NotInstallableError{Reason: "TDrive can't find its own AppImage file. Download the new version manually."}
	}
	return Target{Path: path, Kind: "appimage"}, nil
}

func (l linuxInstaller) Install(payload string, target Target) error {
	dir := filepath.Dir(target.Path)
	_ = removeStaging(dir)
	staging := stagingPath(dir)
	if err := copyFile(payload, staging, 0o755); err != nil {
		return fmt.Errorf("stage update: %w", err)
	}
	if err := swapPaths(target.Path, staging); err != nil {
		_ = os.Remove(staging)
		return err
	}
	return nil
}

func (l linuxInstaller) Relaunch(target Target, waitPID int) error {
	cmd := exec.Command(target.Path, WaitForPIDFlag, strconv.Itoa(waitPID))
	cmd.Dir = filepath.Dir(target.Path)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start new version: %w", err)
	}
	return cmd.Process.Release()
}

func (l linuxInstaller) Cleanup(target Target) error {
	err := os.Remove(target.Path + previousSuffix)
	if errors.Is(err, os.ErrNotExist) {
		err = nil
	}
	if stagingErr := removeStaging(filepath.Dir(target.Path)); stagingErr != nil && err == nil {
		err = stagingErr
	}
	return err
}
