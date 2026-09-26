//go:build linux && !android

package nativeplayer

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func writeScript(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll(%s) = %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
		t.Fatalf("WriteFile(%s) = %v", path, err)
	}
}

func TestProbeLinuxMPVReportsVersionAndRejectsBinaryThatCannotRun(t *testing.T) {
	dir := t.TempDir()

	working := filepath.Join(dir, "good")
	writeScript(t, working, "echo 'mpv 0.34.1 Copyright © 2000-2021 mpv/MPlayer/mplayer2 projects'")
	version, err := probeLinuxMPV(working)
	if err != nil {
		t.Fatalf("probeLinuxMPV(working) = %v, want nil", err)
	}
	if version != (mpvVersion{0, 34}) {
		t.Fatalf("probeLinuxMPV(working) version = %v, want 0.34", version)
	}

	broken := filepath.Join(dir, "bad")
	writeScript(t, broken, "echo 'error while loading shared libraries: libavcodec.so.61' >&2; exit 127")
	_, err = probeLinuxMPV(broken)
	if err == nil {
		t.Fatal("probeLinuxMPV(broken) = nil, want an error")
	}
	if got := err.Error(); !strings.Contains(got, "libavcodec.so.61") {
		t.Fatalf("probeLinuxMPV(broken) = %q, want the loader message quoted", got)
	}
}

func TestFindLinuxMPVFallsBackWhenBundledRuntimeIsBroken(t *testing.T) {
	t.Setenv("TDRIVE_MPV_BIN", "")

	dir := t.TempDir()
	systemMPV := filepath.Join(dir, "bin", "mpv")
	writeScript(t, systemMPV, "echo 'mpv 0.40.0 Copyright'")
	t.Setenv("PATH", filepath.Dir(systemMPV))

	exe, err := os.Executable()
	if err != nil {
		t.Skipf("os.Executable() = %v", err)
	}
	bundled := filepath.Join(filepath.Dir(exe), "media", "mpv")
	if _, err := os.Stat(bundled); err == nil {
		t.Skip("a real bundled mpv sits next to the test binary")
	}
	writeScript(t, bundled, "exit 127")
	t.Cleanup(func() {
		_ = os.RemoveAll(filepath.Dir(bundled))
		linuxMPVVersions.Delete(bundled)
		linuxMPVVersions.Delete(systemMPV)
	})

	got, version, err := findLinuxMPV()
	if err != nil {
		t.Fatalf("findLinuxMPV() = %v, want the system mpv", err)
	}
	if got != systemMPV || version != (mpvVersion{0, 40}) {
		t.Fatalf("findLinuxMPV() = %q (%v), want %q (0.40)", got, version, systemMPV)
	}
}

// mpv exits on any option it does not know, so the command line has to fit
// the release it is given: Ubuntu 22.04 ships 0.34, Fedora something newer.
func TestLinuxMPVArgsFitTheRuntime(t *testing.T) {
	const ipc = "/tmp/mpv.sock"
	has := func(args []string, option string) bool {
		return slices.ContainsFunc(args, func(arg string) bool { return strings.HasPrefix(arg, option) })
	}

	old := linuxMPVArgs(mpvVersion{0, 34}, ipc, 42)
	if has(old, "--auto-window-resize") {
		t.Fatalf("0.34 was given --auto-window-resize, which it rejects: %v", old)
	}
	if !has(old, "--hwdec=auto-safe") || !has(old, "--wid=42") || !has(old, "--input-ipc-server="+ipc) {
		t.Fatalf("0.34 embedded args are missing basics: %v", old)
	}

	current := linuxMPVArgs(mpvVersion{0, 41}, ipc, 42)
	if !has(current, "--auto-window-resize=no") {
		t.Fatalf("0.41 embedded args should pin the window size: %v", current)
	}

	unknown := linuxMPVArgs(mpvVersion{}, ipc, 42)
	if has(unknown, "--auto-window-resize") || has(unknown, "--hwdec") {
		t.Fatalf("an unreadable version must get only universally accepted options: %v", unknown)
	}

	standalone := linuxMPVArgs(mpvVersion{0, 41}, ipc, 0)
	if has(standalone, "--wid") || has(standalone, "--auto-window-resize") || !has(standalone, "--osc=yes") {
		t.Fatalf("standalone args are wrong: %v", standalone)
	}
	for _, args := range [][]string{old, current, unknown, standalone} {
		if has(args, "--video-align") {
			t.Fatalf("video alignment is already the default and predates some runtimes: %v", args)
		}
	}
}
