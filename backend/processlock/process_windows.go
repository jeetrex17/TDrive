//go:build windows

package processlock

import (
	"errors"

	"golang.org/x/sys/windows"
)

func processRunning(pid int) bool {
	if pid <= 0 {
		return false
	}
	h, err := windows.OpenProcess(windows.SYNCHRONIZE|windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		// Access denied means a process exists but this user cannot inspect it.
		// Treat it as live; invalid parameter means the PID is stale.
		if errors.Is(err, windows.ERROR_ACCESS_DENIED) {
			return true
		}
		return false
	}
	defer windows.CloseHandle(h)
	status, err := windows.WaitForSingleObject(h, 0)
	if err != nil {
		return true
	}
	return status == uint32(windows.WAIT_TIMEOUT)
}
