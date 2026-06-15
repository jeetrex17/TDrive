//go:build windows

package main

import "os/exec"

func detachDaemonCommand(cmd *exec.Cmd) {}
