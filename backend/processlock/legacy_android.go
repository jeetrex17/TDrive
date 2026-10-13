//go:build android

package processlock

import "os"

// Legacy Android ownership is disproved only by readable process identity.
func legacyOwnerRunning(info Info, path string) bool {
	return processRunning(info.PID) && legacyAndroidOwnerMatches("/proc", path, info.PID, os.Getpid())
}
