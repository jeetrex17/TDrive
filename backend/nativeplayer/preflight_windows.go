//go:build windows

package nativeplayer

import "context"

// PreflightDecode is intentionally a no-op on Windows. Playback already runs
// in an isolated mpv.exe sidecar, so a second remote decode only adds startup
// latency and another failure path without improving process isolation.
func PreflightDecode(_ context.Context, _ string) error { return nil }
