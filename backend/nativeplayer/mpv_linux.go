//go:build linux && !android

package nativeplayer

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const mpvProbeTimeout = 3 * time.Second

// linuxMPVVersions remembers what each probed binary reported, so a run pays
// one process spawn per binary rather than one per video opened.
var linuxMPVVersions sync.Map // path -> mpvVersion

// findLinuxMPV picks the mpv to run and reports its version: an explicit
// override, else the bundled runtime when it can execute here, else the
// system mpv.
func findLinuxMPV() (string, mpvVersion, error) {
	if override := os.Getenv("TDRIVE_MPV_BIN"); override != "" {
		if st, err := os.Stat(override); err == nil && !st.IsDir() {
			version, err := linuxMPVVersion(override)
			return override, version, err
		}
		return "", mpvVersion{}, fmt.Errorf("TDRIVE_MPV_BIN is not executable")
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
			version, err := linuxMPVVersion(candidate)
			if err != nil {
				linuxNativeLogf("bundled mpv unusable, falling back: path=%s err=%v", candidate, err)
				continue
			}
			return candidate, version, nil
		}
	}

	if path, err := exec.LookPath("mpv"); err == nil {
		version, err := linuxMPVVersion(path)
		if err != nil {
			return "", mpvVersion{}, fmt.Errorf("native player: system mpv cannot run: %w", err)
		}
		return path, version, nil
	}
	return "", mpvVersion{}, fmt.Errorf("native player: mpv executable not found")
}

// linuxMPVVersion probes path once and remembers the answer.
func linuxMPVVersion(path string) (mpvVersion, error) {
	if cached, ok := linuxMPVVersions.Load(path); ok {
		return cached.(mpvVersion), nil
	}
	version, err := probeLinuxMPV(path)
	if err != nil {
		return mpvVersion{}, err
	}
	linuxMPVVersions.Store(path, version)
	return version, nil
}

// probeLinuxMPV reports whether an mpv binary can actually execute here and
// which release it is.
func probeLinuxMPV(path string) (mpvVersion, error) {
	ctx, cancel := context.WithTimeout(context.Background(), mpvProbeTimeout)
	defer cancel()

	var output bytes.Buffer
	cmd := exec.CommandContext(ctx, path, "--no-config", "--version")
	cmd.Stdout = &output
	cmd.Stderr = &output
	if err := cmd.Run(); err != nil {
		if tail := strings.Join(strings.Fields(output.String()), " "); tail != "" {
			return mpvVersion{}, fmt.Errorf("%w: %s", err, tail)
		}
		return mpvVersion{}, err
	}
	version := parseMPVVersion(output.String())
	if !version.known() {
		linuxNativeLogf("mpv version unknown, using only the options every release accepts: path=%s", path)
	}
	return version, nil
}

// linuxMPVArgs builds the mpv command line for one playback. Options that
// arrived in later releases are passed only to a binary new enough to know
// them: mpv refuses to start on an unknown option, which is how the 0.34
// runtime shipped in AppImages failed every open on a fresh Fedora.
func linuxMPVArgs(version mpvVersion, ipcPath string, windowID uintptr) []string {
	args := []string{
		"--no-config",
		// Terminal messages stay on so a startup failure is reported by mpv
		// itself; the output is captured rather than printed.
		"--terminal=yes",
		"--msg-level=all=error",
		"--ytdl=no",
		"--cache=yes",
		"--demuxer-readahead-secs=20",
		"--demuxer-max-bytes=67108864",
		"--demuxer-max-back-bytes=33554432",
		// After an underrun, wait for three seconds of buffer before resuming
		// instead of one, so playback does not flap on links just under the
		// bitrate.
		"--cache-pause-wait=3",
		"--keepaspect=yes",
		"--force-window=immediate",
		"--input-terminal=no",
		"--idle=yes",
		"--keep-open=yes",
		"--input-ipc-server=" + ipcPath,
	}
	// auto-safe hardware decoding exists since 0.34. Older builds decode in
	// software rather than gamble on a decoder that can take the player down.
	if version.atLeast(0, 34) {
		args = append(args, "--hwdec=auto-safe")
	}
	if windowID != 0 {
		args = append(args,
			"--keepaspect-window=no",
			"--osc=no",
			"--osd-bar=no",
			"--osd-level=0",
			"--cursor-autohide=no",
			"--no-input-default-bindings",
			"--input-vo-keyboard=no",
			fmt.Sprintf("--wid=%d", windowID),
		)
		// --auto-window-resize exists since 0.36. With --wid, older builds
		// never resized the embedded window in the first place.
		if version.atLeast(0, 36) {
			args = append(args, "--auto-window-resize=no")
		}
		return args
	}
	// Wayland does not provide the cross-process child-window embedding
	// primitive used on X11. Keep playback reliable in an honest standalone
	// mpv window and leave its native controls enabled.
	return append(args,
		"--title=TDrive Video",
		"--osc=yes",
		"--osd-bar=yes",
		"--osd-level=1",
		"--input-default-bindings=yes",
		"--input-vo-keyboard=yes",
	)
}
