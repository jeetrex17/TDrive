//go:build windows

package main

import (
	"os/exec"
	"syscall"

	"golang.org/x/sys/windows"
)

func detachDaemonCommand(cmd *exec.Cmd) {
	cmd.SysProcAttr = daemonSysProcAttr()
}

func daemonSysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{
		// Keep the daemon outside the invoking console's control group and avoid
		// flashing a second console window during automatic startup.
		CreationFlags: windows.CREATE_NEW_PROCESS_GROUP | windows.CREATE_NO_WINDOW,
		HideWindow:    true,
	}
}
