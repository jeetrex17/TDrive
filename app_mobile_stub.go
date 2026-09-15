//go:build !ios && !android

package main

import "github.com/wailsapp/wails/v3/pkg/application"

// registerMobileLifecycle is a no-op on desktop, where the OS does not suspend
// the app the way a phone does.
func registerMobileLifecycle(_ *App, _ *application.App) {}
