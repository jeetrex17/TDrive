//go:build darwin

package nativeplayer

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

const preflightTimeout = 6 * time.Second

// PreflightDecode decodes a tiny prefix in a separate process before libmpv is
// loaded in-process. A bad system decoder can segfault; doing the first probe
// out-of-process keeps TDrive alive and turns that class of failure into a
// normal playback error.
func PreflightDecode(ctx context.Context, url string) error {
	if os.Getenv("TDRIVE_SKIP_MPV_PREFLIGHT") == "1" {
		return nil
	}
	mpvPath, err := findPreflightMPV()
	if err != nil {
		// Future bundled libmpv builds may not ship a separate mpv executable.
		// In that case the app still attempts playback; packaging owns safety.
		return nil
	}

	runCtx, cancel := context.WithTimeout(ctx, preflightTimeout)
	defer cancel()
	cmd := exec.CommandContext(runCtx, mpvPath,
		"--no-config",
		"--really-quiet",
		"--terminal=no",
		"--force-window=no",
		"--vo=null",
		"--ao=null",
		"--frames=1",
		"--demuxer-readahead-secs=0.5",
		"--demuxer-max-bytes=2097152",
		"--",
		url,
	)
	if output, err := cmd.CombinedOutput(); err != nil {
		if runCtx.Err() != nil {
			return fmt.Errorf("%w: timed out", ErrDecoderUnsafe)
		}
		if len(output) > 0 {
			return fmt.Errorf("%w: %v: %s", ErrDecoderUnsafe, err, string(output))
		}
		return fmt.Errorf("%w: %v", ErrDecoderUnsafe, err)
	}
	return nil
}

func findPreflightMPV() (string, error) {
	if override := os.Getenv("TDRIVE_MPV_BIN"); override != "" {
		st, err := os.Stat(override)
		if err != nil || st.IsDir() {
			return "", fmt.Errorf("TDRIVE_MPV_BIN is not executable")
		}
		return override, nil
	}
	if bundled, err := bundledMPVPath(); err == nil {
		return bundled, nil
	}
	return exec.LookPath("mpv")
}

func bundledMPVPath() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	candidates := []string{
		filepath.Join(filepath.Dir(exe), "mpv"),
		filepath.Join(filepath.Dir(exe), "..", "Resources", "media", "mpv"),
	}
	for _, candidate := range candidates {
		st, err := os.Stat(candidate)
		if err == nil && !st.IsDir() {
			return candidate, nil
		}
	}
	return "", os.ErrNotExist
}
