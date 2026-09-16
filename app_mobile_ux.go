package main

import (
	"encoding/json"

	"github.com/wailsapp/wails/v3/pkg/application"
)

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

// SetImmersive hides the system bars for a full-screen surface, or gives them
// back. The video player is the only caller: everywhere else the bars belong on
// screen, and hiding them would just make the app harder to leave.
//
// It is not only about looking tidy. An app targeting SDK 35 is laid out edge
// to edge on Android 15 whether it asks or not, and the system paints a
// translucent scrim behind three-button navigation so the buttons stay legible
// over whatever is beneath them. That scrim lands on top of the picture as a
// grey band down one edge, and the API that used to switch it off --
// setNavigationBarContrastEnforced -- does nothing at this target. The bar
// itself can still be hidden, which removes the scrim with it.
//
// iOS has no navigation bar to hide, so there the payload only takes the status
// bar, which is what a full-screen player wants anyway.
func (a *App) SetImmersive(on bool) {
	payload, err := json.Marshal(map[string]any{"hidden": on, "bars": "system"})
	if err != nil {
		return
	}
	application.Mobile.SetStatusBar(string(payload))
}
