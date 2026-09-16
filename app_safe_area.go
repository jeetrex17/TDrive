package main

import (
	"encoding/json"

	"github.com/wailsapp/wails/v3/pkg/application"
)

// SafeAreaInsets is how much of each screen edge the OS reserves for its own
// chrome: the status bar and the gesture handle on a phone. Zero everywhere
// else. Android reports device pixels and iOS reports points, so the caller
// scales by the device pixel ratio; see ui/mobile/safe-area.ts.
type SafeAreaInsets struct {
	Top    float64 `json:"top"`
	Bottom float64 `json:"bottom"`
	Left   float64 `json:"left"`
	Right  float64 `json:"right"`
}

// SafeAreaInsets reports those reserved edges. The frontend asks for them
// because CSS cannot see all of them: Android's WebView fills
// env(safe-area-inset-top) but leaves the bottom gesture area at zero.
func (a *App) SafeAreaInsets() SafeAreaInsets {
	var insets SafeAreaInsets
	raw := application.Mobile.SafeAreaJSON()
	if raw == "" {
		return insets
	}
	if err := json.Unmarshal([]byte(raw), &insets); err != nil {
		return SafeAreaInsets{}
	}
	return insets
}
