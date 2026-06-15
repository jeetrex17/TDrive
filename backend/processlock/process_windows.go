//go:build windows

package processlock

import "os"

func processRunning(pid int) bool {
	if pid <= 0 {
		return false
	}
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	_ = p.Release()
	// Windows cannot reliably probe liveness through os.FindProcess alone.
	// Prefer preserving the lock over accidentally allowing a second backend.
	return true
}
