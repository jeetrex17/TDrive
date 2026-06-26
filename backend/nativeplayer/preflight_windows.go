//go:build windows

package nativeplayer

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"syscall"
	"time"
)

const preflightTimeout = 6 * time.Second

// PreflightDecode can decode one frame out-of-process before the sidecar player
// is attached to the app window. On Windows the actual player is already an
// out-of-process mpv.exe sidecar, so decoder crashes do not take down TDrive.
// Keep this check opt-in only: for long remote Telegram files it can block or
// fail before the real player has a chance to open the stream.
func PreflightDecode(ctx context.Context, url string) error {
	if !windowsNativePlayerEnabled() {
		return nil
	}
	if os.Getenv("TDRIVE_ENABLE_MPV_PREFLIGHT") != "1" {
		return nil
	}
	mpvPath, err := findWindowsMPV()
	if err != nil {
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
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
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
