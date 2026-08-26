//go:build linux

package nativeplayer

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"time"
)

const preflightTimeout = 6 * time.Second

func PreflightDecode(ctx context.Context, url string) error {
	if !linuxNativePlayerEnabled() {
		linuxNativeLogf("preflight skipped: disabled by %s=0", linuxNativePlayerFlag)
		return nil
	}
	if !sidecarPreflightEnabled(os.Getenv("TDRIVE_ENABLE_MPV_PREFLIGHT"), os.Getenv("TDRIVE_SKIP_MPV_PREFLIGHT")) {
		linuxNativeLogf("preflight skipped: not explicitly enabled")
		return nil
	}
	mpvPath, err := findLinuxMPV()
	if err != nil {
		linuxNativeLogf("preflight skipped: %v", err)
		return nil
	}

	runCtx, cancel := context.WithTimeout(ctx, preflightTimeout)
	defer cancel()
	started := time.Now()
	linuxNativeLogf("preflight start: mpv=%s timeout=%s", mpvPath, preflightTimeout)
	args, stdin := mpvPreflightInvocation(url)
	cmd := exec.CommandContext(runCtx, mpvPath, args...)
	cmd.Stdin = stdin
	if err := cmd.Run(); err != nil {
		if runCtx.Err() != nil {
			linuxNativeLogf("preflight inconclusive after %s: timeout", time.Since(started).Round(time.Millisecond))
			return nil
		}
		if sig, crashed := decoderCrashSignal(err); crashed {
			linuxNativeLogf("preflight decoder crash after %s: %s", time.Since(started).Round(time.Millisecond), sig)
			return fmt.Errorf("%w: %s", ErrDecoderUnsafe, sig)
		}
		linuxNativeLogf("preflight inconclusive after %s: ordinary mpv failure", time.Since(started).Round(time.Millisecond))
		return nil
	}
	linuxNativeLogf("preflight ok after %s", time.Since(started).Round(time.Millisecond))
	return nil
}
