//go:build linux

package nativeplayer

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

func findLinuxMPV() (string, error) {
	if override := os.Getenv("TDRIVE_MPV_BIN"); override != "" {
		if st, err := os.Stat(override); err == nil && !st.IsDir() {
			return override, nil
		}
		return "", fmt.Errorf("TDRIVE_MPV_BIN is not executable")
	}

	if exe, err := os.Executable(); err == nil {
		dir := filepath.Dir(exe)
		for _, candidate := range []string{
			filepath.Join(dir, "media", "mpv"),
			filepath.Join(dir, "mpv"),
			filepath.Join(dir, "..", "lib", "tdrive", "mpv"),
			filepath.Join(dir, "..", "lib", "TDrive", "mpv"),
		} {
			if st, err := os.Stat(candidate); err == nil && !st.IsDir() {
				return candidate, nil
			}
		}
	}

	if !systemMPVLookupEnabled(os.Getenv(systemMPVLookupFlag)) {
		return "", fmt.Errorf("native player: bundled mpv executable not found")
	}
	if path, err := exec.LookPath("mpv"); err == nil {
		return path, nil
	}
	return "", fmt.Errorf("native player: mpv executable not found")
}
