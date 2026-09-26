//go:build !ios && !android

package main

import (
	"errors"

	"github.com/wailsapp/wails/v3/pkg/application"
)

// registerMobileLifecycle is a no-op on desktop, where the OS does not suspend
// the app the way a phone does.
func registerMobileLifecycle(_ *App, _ *application.App) {}

// mobileKeepAwake is a no-op on desktop, which does not suspend transfers.
func mobileKeepAwake(bool) {}

// shareFileNative has no desktop counterpart: downloads there go through the
// save dialog and never need a share sheet.
func shareFileNative(string) error {
	return errors.New("sharing is only available on iOS and Android")
}
