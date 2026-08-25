//go:build windows

package main

import (
	"os/exec"
	"testing"

	"golang.org/x/sys/windows"
)

func TestDetachDaemonCommandDetachesAndHidesConsole(t *testing.T) {
	t.Parallel()

	cmd := exec.Command("tdrive.exe")
	detachDaemonCommand(cmd)
	attr := cmd.SysProcAttr
	if attr == nil {
		t.Fatal("daemonSysProcAttr() returned nil")
	}
	if !attr.HideWindow {
		t.Error("HideWindow = false, want true")
	}

	wantFlags := uint32(windows.CREATE_NEW_PROCESS_GROUP | windows.CREATE_NO_WINDOW)
	if attr.CreationFlags != wantFlags {
		t.Errorf("CreationFlags = %#x, want %#x", attr.CreationFlags, wantFlags)
	}
}
