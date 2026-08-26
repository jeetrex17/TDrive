package updater

import (
	"strconv"
	"strings"
	"time"
)

// WaitForPIDFlag is passed to the freshly installed binary so it can wait for
// the instance that spawned it to exit. Without the handshake the new process
// would race the old one for the single-backend lock and quit immediately.
const WaitForPIDFlag = "--wait-for-pid"

// WaitPIDFromArgs extracts the pid from "--wait-for-pid N" or
// "--wait-for-pid=N". The flag is consumed by main before Wails starts.
func WaitPIDFromArgs(args []string) (int, bool) {
	for i, arg := range args {
		var value string
		switch {
		case arg == WaitForPIDFlag && i+1 < len(args):
			value = args[i+1]
		case strings.HasPrefix(arg, WaitForPIDFlag+"="):
			value = strings.TrimPrefix(arg, WaitForPIDFlag+"=")
		default:
			continue
		}
		pid, err := strconv.Atoi(value)
		if err != nil || pid <= 0 {
			return 0, false
		}
		return pid, true
	}
	return 0, false
}

// WaitForExit polls alive(pid) until it reports false or timeout elapses.
// It returns true when the process is gone.
func WaitForExit(pid int, timeout time.Duration, alive func(int) bool) bool {
	deadline := time.Now().Add(timeout)
	for alive(pid) {
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(100 * time.Millisecond)
	}
	return true
}
