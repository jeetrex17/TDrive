package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"

	"TDrive/backend/datadir"
	"TDrive/backend/mountcontroller"
)

const mountDriveLetterFilename = "mount-drive-letter.json"

// MountDriveLetterView is the drive letter the next Windows mount uses.
// Available is false on other platforms, which mount by volume name.
type MountDriveLetterView struct {
	Available bool   `json:"available"`
	Letter    string `json:"letter"`
}

type mountDriveLetterPreference struct {
	WindowsDrive string `json:"windows_drive"`
}

func (a *App) GetMountDriveLetter() (MountDriveLetterView, error) {
	letter, err := readMountDriveLetter()
	if err != nil {
		return MountDriveLetterView{}, err
	}
	return mountDriveLetterView(letter), nil
}

// SetMountDriveLetter saves the letter for the next mount; a mounted drive
// keeps its current letter until it is ejected.
func (a *App) SetMountDriveLetter(letter string) (MountDriveLetterView, error) {
	normalized, err := mountcontroller.NormalizeWindowsDrive(letter)
	if err != nil {
		return MountDriveLetterView{}, err
	}
	dir, err := datadir.Dir()
	if err != nil {
		return MountDriveLetterView{}, err
	}
	file, err := os.CreateTemp(dir, ".mount-drive-letter-*")
	if err != nil {
		return MountDriveLetterView{}, fmt.Errorf("create mount drive letter: %w", err)
	}
	defer os.Remove(file.Name())
	writeErr := json.NewEncoder(file).Encode(mountDriveLetterPreference{WindowsDrive: normalized})
	if err := errors.Join(writeErr, file.Close()); err != nil {
		return MountDriveLetterView{}, fmt.Errorf("write mount drive letter: %w", err)
	}
	if err := os.Rename(file.Name(), filepath.Join(dir, mountDriveLetterFilename)); err != nil {
		return MountDriveLetterView{}, fmt.Errorf("replace mount drive letter: %w", err)
	}
	return mountDriveLetterView(normalized), nil
}

// readMountDriveLetter returns the saved letter, or the default when none is
// saved.
func readMountDriveLetter() (string, error) {
	dir, err := datadir.Dir()
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(filepath.Join(dir, mountDriveLetterFilename))
	if errors.Is(err, os.ErrNotExist) {
		return mountcontroller.NormalizeWindowsDrive("")
	}
	if err != nil {
		return "", fmt.Errorf("read mount drive letter: %w", err)
	}
	var preference mountDriveLetterPreference
	if err := json.Unmarshal(data, &preference); err != nil {
		return "", fmt.Errorf("decode mount drive letter: %w", err)
	}
	return mountcontroller.NormalizeWindowsDrive(preference.WindowsDrive)
}

// mountStartOptions falls back to the default letter when the saved one cannot
// be read, so a damaged preference file never blocks mounting.
func mountStartOptions() mountcontroller.StartOptions {
	letter, err := readMountDriveLetter()
	if err != nil {
		slog.Warn("mount: using the default drive letter", "error", err)
		letter = ""
	}
	return mountcontroller.StartOptions{Mode: mountcontroller.ModeAuto, WindowsDrive: letter}
}

func mountDriveLetterView(letter string) MountDriveLetterView {
	return MountDriveLetterView{Available: runtime.GOOS == "windows", Letter: letter}
}
