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
	if !sidecarPreflightEnabled(os.Getenv("TDRIVE_ENABLE_MPV_PREFLIGHT"), os.Getenv("TDRIVE_SKIP_MPV_PREFLIGHT")) {
		return nil
	}
	mpvPath, err := findWindowsMPV()
	if err != nil {
		return nil
	}

	runCtx, cancel := context.WithTimeout(ctx, preflightTimeout)
	defer cancel()
	args, stdin := mpvPreflightInvocation(url)
	cmd := exec.CommandContext(runCtx, mpvPath, args...)
	cmd.Stdin = stdin
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	if err := cmd.Run(); err != nil {
		if runCtx.Err() != nil {
			return nil
		}
		return fmt.Errorf("%w", ErrDecoderUnsafe)
	}
	return nil
}
