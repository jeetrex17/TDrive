//go:build !android

package processlock

func legacyOwnerRunning(info Info, _ string) bool {
	return processRunning(info.PID)
}
