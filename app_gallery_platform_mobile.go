//go:build ios || android

package main

import (
	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
)

// Native application events are not automatically custom JavaScript events.
// Forward their structured payloads explicitly through the app event boundary.
func registerGalleryResourceEvents(a *App, app *application.App) {
	for _, binding := range []struct {
		kind events.ApplicationEventType
		name string
	}{
		{events.Android.NetworkChanged, "android:NetworkChanged"},
		{events.Android.BatteryChanged, "android:BatteryChanged"},
		{events.IOS.NetworkChanged, "ios:NetworkChanged"},
		{events.IOS.BatteryChanged, "ios:BatteryChanged"},
	} {
		app.Event.OnApplicationEvent(binding.kind, func(event *application.ApplicationEvent) {
			if event.Context() != nil {
				a.emit(binding.name, event.Context().Data())
			}
		})
	}
	pressure := func(*application.ApplicationEvent) {
		a.emit("gallery_memory_pressure")
		a.revokeGalleryImages()
	}
	app.Event.OnApplicationEvent(events.Android.ApplicationLowMemory, pressure)
	app.Event.OnApplicationEvent(events.IOS.ApplicationDidReceiveMemoryWarning, pressure)
}
