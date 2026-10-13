//go:build !android

package processlock

func legacyOwnerRunning(info Info) bool {
	return processRunning(info.PID)
}
