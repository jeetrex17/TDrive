//go:build windows

package updater

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/windows"
)

const mediaDirName = "media"

type windowsInstaller struct {
	executable func() (string, error)
}

func newPlatformInstaller() Installer {
	return windowsInstaller{executable: os.Executable}
}

func (w windowsInstaller) Target() (Target, error) {
	exe, err := w.executable()
	if err != nil {
		return Target{}, err
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	return Target{Path: exe, Kind: "exe"}, nil
}

// Install swaps the executable and the bundled mpv runtime. Windows lets a
// running .exe be renamed but not deleted, which is exactly what swapPaths
// relies on; the media folder must not be in use, so callers close native
// players first.
func (w windowsInstaller) Install(payload string, target Target) error {
	dir := filepath.Dir(target.Path)
	staging := stagingPath(dir)
	if err := os.RemoveAll(staging); err != nil {
		return fmt.Errorf("clear staging: %w", err)
	}
	if err := extractZip(payload, staging); err != nil {
		return fmt.Errorf("unpack update: %w", err)
	}
	defer os.RemoveAll(staging)

	newExe, err := findSingleExecutable(staging)
	if err != nil {
		return err
	}

	currentMedia := filepath.Join(dir, mediaDirName)
	newMedia := filepath.Join(staging, mediaDirName)
	mediaSwapped := false
	if info, err := os.Stat(newMedia); err == nil && info.IsDir() {
		if _, err := os.Stat(currentMedia); err == nil {
			if err := swapPaths(currentMedia, newMedia); err != nil {
				return fmt.Errorf("media runtime: %w", err)
			}
			mediaSwapped = true
		} else if err := os.Rename(newMedia, currentMedia); err != nil {
			return fmt.Errorf("media runtime: %w", err)
		}
	}

	if err := swapPaths(target.Path, newExe); err != nil {
		if mediaSwapped {
			// Put the old runtime back so the still-installed old exe keeps
			// a matching mpv next to it.
			_ = os.Rename(currentMedia, newMedia)
			_ = os.Rename(currentMedia+previousSuffix, currentMedia)
		}
		return err
	}
	return nil
}

// findSingleExecutable returns the one top-level .exe in dir.
func findSingleExecutable(dir string) (string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", err
	}
	var exes []string
	for _, entry := range entries {
		if !entry.IsDir() && strings.EqualFold(filepath.Ext(entry.Name()), ".exe") {
			exes = append(exes, filepath.Join(dir, entry.Name()))
		}
	}
	if len(exes) != 1 {
		return "", errNoAppInPayload
	}
	return exes[0], nil
}

func (w windowsInstaller) Relaunch(target Target, waitPID int) error {
	cmd := exec.Command(target.Path, WaitForPIDFlag, strconv.Itoa(waitPID))
	cmd.Dir = filepath.Dir(target.Path)
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: windows.CREATE_NEW_PROCESS_GROUP | windows.DETACHED_PROCESS,
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start new version: %w", err)
	}
	return cmd.Process.Release()
}

// Cleanup retries briefly: Windows can keep the old image mapped for a moment
// after the previous process exits.
func (w windowsInstaller) Cleanup(target Target) error {
	dir := filepath.Dir(target.Path)
	var err error
	for attempt := 0; attempt < 5; attempt++ {
		err = removeAllIgnoringMissing(target.Path + previousSuffix)
		if mediaErr := removeAllIgnoringMissing(filepath.Join(dir, mediaDirName+previousSuffix)); err == nil {
			err = mediaErr
		}
		if stagingErr := removeStaging(dir); err == nil {
			err = stagingErr
		}
		if err == nil {
			return nil
		}
		time.Sleep(300 * time.Millisecond)
	}
	return err
}

func removeAllIgnoringMissing(path string) error {
	err := os.RemoveAll(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}
