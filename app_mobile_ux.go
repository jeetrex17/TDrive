package main

import "github.com/wailsapp/wails/v3/pkg/application"

// The mobile UX primitives iOS and Android both implement identically, exposed
// as one binding each so the frontend never branches on platform to reach them.
// application.Mobile dispatches to the IOS or Android manager and to a no-op
// stub on desktop, so every call here is safe to make from shared code.

// hapticKinds is the vocabulary both platform managers understand. Unknown
// values are dropped rather than passed through: Android's switch falls back to
// a medium click for anything it does not recognise, so a typo would silently
// buzz the wrong weight instead of doing nothing.
var hapticKinds = map[string]bool{
	"impact-light":  true,
	"impact-medium": true,
	"impact-heavy":  true,
	"selection":     true,
	"success":       true,
	"warning":       true,
	"error":         true,
}

// Haptic plays a semantic feedback pattern: a light impact for a long press or
// an armed pull, "selection" for a changed sort or filter, and success/warning/
// error only at a real outcome. The OS maps these to its own generator, so a
// user who has turned system haptics off feels nothing, as they asked.
func (a *App) Haptic(kind string) {
	if !hapticKinds[kind] {
		return
	}
	application.Mobile.Haptic(kind)
}

// SetScreenProtect asks the OS to keep the app's contents out of screenshots
// and the app switcher while the vault is locked or privacy is raised.
//
// The two platforms do NOT deliver the same thing. Android sets FLAG_SECURE,
// which really does blank screenshots, screen recording and the switcher
// thumbnail. iOS cannot block any of that: the same call only starts
// *detection*, reported back as a "common:screenCapture" event. Hiding the iOS
// switcher preview needs a native overlay on resign-active that this host does
// not have yet, so on iOS treat this as telemetry, not protection.
func (a *App) SetScreenProtect(enabled bool) {
	application.Mobile.SetScreenProtect(enabled)
}

// SetKeyboardWatch starts or stops "common:keyboard" {visible,height} events.
// The frontend prefers visualViewport where it exists and falls back to these,
// which is the only source Android's WebView reports reliably.
func (a *App) SetKeyboardWatch(enabled bool) {
	application.Mobile.SetKeyboardWatch(enabled)
}
