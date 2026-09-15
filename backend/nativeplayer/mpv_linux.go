//go:build linux

package nativeplayer

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

const mpvProbeTimeout = 3 * time.Second

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
			if st, err := os.Stat(candidate); err != nil || st.IsDir() {
				continue
			}
			// The bundled runtime is built on one distro and pins its own
			// LD_LIBRARY_PATH, so it can fail to load on another. Prefer the
			// system mpv over dying later with an opaque IPC-socket error.
			if err := probeLinuxMPV(candidate); err != nil {
				linuxNativeLogf("bundled mpv unusable, falling back: path=%s err=%v", candidate, err)
				continue
			}
			return candidate, nil
		}
	}

	if path, err := exec.LookPath("mpv"); err == nil {
		return path, nil
	}
	return "", fmt.Errorf("native player: mpv executable not found")
}

// probeLinuxMPV reports whether an mpv binary can actually execute here.
func probeLinuxMPV(path string) error {
	ctx, cancel := context.WithTimeout(context.Background(), mpvProbeTimeout)
	defer cancel()

	output := &mpvOutput{}
	cmd := exec.CommandContext(ctx, path, "--no-config", "--version")
	cmd.Stdout = output
	cmd.Stderr = output
	if err := cmd.Run(); err != nil {
		if tail := output.Tail(); tail != "" {
			return fmt.Errorf("%w: %s", err, tail)
		}
		return err
	}
	return nil
}
