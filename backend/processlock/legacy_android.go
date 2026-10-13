//go:build android

package processlock

import "syscall"

// The data directory is app-private, so a PID this process may not signal
// belongs to another UID and cannot be a legacy owner. Android 7+ mounts /proc
// with hidepid, so no other identity check is readable.
func legacyOwnerRunning(info Info) bool {
	return info.PID > 0 && syscall.Kill(info.PID, 0) == nil
}
