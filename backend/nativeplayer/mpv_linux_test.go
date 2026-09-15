//go:build linux

package nativeplayer

import (
	"os"
	"path/filepath"
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

func TestProbeLinuxMPVRejectsBinaryThatCannotRun(t *testing.T) {
	dir := t.TempDir()

	working := filepath.Join(dir, "good")
	writeScript(t, working, "exit 0")
	if err := probeLinuxMPV(working); err != nil {
		t.Fatalf("probeLinuxMPV(working) = %v, want nil", err)
	}

	broken := filepath.Join(dir, "bad")
	writeScript(t, broken, "echo 'error while loading shared libraries: libavcodec.so.61' >&2; exit 127")
	err := probeLinuxMPV(broken)
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
	writeScript(t, systemMPV, "exit 0")
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
	t.Cleanup(func() { _ = os.RemoveAll(filepath.Dir(bundled)) })

	got, err := findLinuxMPV()
	if err != nil {
		t.Fatalf("findLinuxMPV() = %v, want the system mpv", err)
	}
	if got != systemMPV {
		t.Fatalf("findLinuxMPV() = %q, want %q", got, systemMPV)
	}
}
