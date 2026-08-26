//go:build darwin || linux

package nativeplayer

import (
	"errors"
	"os/exec"
	"syscall"
)

func decoderCrashSignal(err error) (syscall.Signal, bool) {
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		return 0, false
	}
	status, ok := exitErr.Sys().(syscall.WaitStatus)
	if !ok || !status.Signaled() {
		return 0, false
	}
	sig := status.Signal()
	switch sig {
	case syscall.SIGABRT, syscall.SIGBUS, syscall.SIGFPE, syscall.SIGILL, syscall.SIGSEGV, syscall.SIGTRAP:
		return sig, true
	default:
		return 0, false
	}
}
