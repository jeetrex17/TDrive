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
	if os.Getenv("TDRIVE_SKIP_MPV_PREFLIGHT") == "1" {
		linuxNativeLogf("preflight skipped: TDRIVE_SKIP_MPV_PREFLIGHT=1")
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
			linuxNativeLogf("preflight failed after %s: timeout", time.Since(started).Round(time.Millisecond))
			return fmt.Errorf("%w: timed out", ErrDecoderUnsafe)
		}
		if len(output) > 0 {
			linuxNativeLogf("preflight failed after %s: %v: %s", time.Since(started).Round(time.Millisecond), err, string(output))
			return fmt.Errorf("%w: %v: %s", ErrDecoderUnsafe, err, string(output))
		}
		linuxNativeLogf("preflight failed after %s: %v", time.Since(started).Round(time.Millisecond), err)
		return fmt.Errorf("%w: %v", ErrDecoderUnsafe, err)
	}
	linuxNativeLogf("preflight ok after %s", time.Since(started).Round(time.Millisecond))
	return nil
}
