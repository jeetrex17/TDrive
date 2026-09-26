//go:build ios || android

package main

import (
	"encoding/json"
	"fmt"
	"runtime"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
)

// mobileKeepAwake overrides the idle timer while a transfer runs, so the
// screen does not lock and suspend the app mid-upload.
func mobileKeepAwake(on bool) {
	application.Mobile.SetKeepAwake(on)
}

// shareFileNative hands a sandbox file to the OS share sheet: the activity
// view controller on iOS, the send chooser on Android, where the host turns a
// file URL into a FileProvider content URI (see build/android WailsBridge).
func shareFileNative(path string) error {
	payload, err := json.Marshal(map[string]string{"url": fileURL(path)})
	if err != nil {
		return err
	}
	application.Mobile.Share(string(payload))
	return nil
}

// registerMobileLifecycle ties OS suspend/resume to the engine. Backgrounding a
// phone drops the Telegram update loop and stops serving decrypted media over
// the loopback server; returning to the foreground resumes both and pulls any
// changes the active drive missed while suspended. Photo backup is different:
// Android's data-sync foreground service can keep its process alive and iOS
// grants a short background assertion. Native expiry signals and a conservative
// Go deadline stop the worker; the OS may suspend it sooner. New items wait for
// foreground resume even when the current upload can finish in the background.
func registerMobileLifecycle(a *App, wailsApp *application.App) {
	if a == nil || wailsApp == nil {
		return
	}
	background := func(*application.ApplicationEvent) { a.mobileEnterBackground() }
	foreground := func(*application.ApplicationEvent) { a.mobileEnterForeground() }

	// Only the host platform's events ever fire, so registering both pairs is
	// harmless and keeps this file free of per-OS build tags.
	wailsApp.Event.OnApplicationEvent(events.IOS.ApplicationDidEnterBackground, background)
	wailsApp.Event.OnApplicationEvent(events.Android.ActivityPaused, background)
	wailsApp.Event.OnApplicationEvent(events.IOS.ApplicationWillEnterForeground, foreground)
	wailsApp.Event.OnApplicationEvent(events.Android.ActivityResumed, foreground)
	registerGalleryResourceEvents(a, wailsApp)
}

func (a *App) mobileEnterBackground() {
	if a == nil || a.engine == nil {
		return
	}
	a.engine.PauseLiveSync()
	a.engine.CloseMediaSessions()
	a.revokeGalleryImages()
	if !a.enterPhotoBackupBackground(runtime.GOOS) {
		a.stopPhotoBackup()
	}
}

func (a *App) mobileEnterForeground() {
	if a == nil || a.engine == nil {
		return
	}
	a.leavePhotoBackupBackground()
	if !a.authReady.Load() {
		return
	}
	a.engine.ResumeLiveSync()
	// The durable queue may have been interrupted by process suspension or an
	// OS background deadline. A manual pause remains sticky in startPhotoBackup,
	// so this never overrides the user's choice.
	go func() { _ = a.startPhotoBackup() }()
	channelID := a.engine.ActiveChannelID()
	if channelID <= 0 {
		return
	}
	// SyncChannel can block on the network, so keep it off the event dispatch.
	go func() {
		if err := a.engine.LifecycleService().SyncChannel(a.appContext(), channelID); err != nil {
			fmt.Printf("Warning: mobile foreground sync failed: %v\n", err)
		}
	}()
}
