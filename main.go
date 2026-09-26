package main

import (
	"embed"
	"log"
	"os"
	"time"

	"TDrive/backend/processlock"
	"TDrive/backend/updater"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
)

//go:embed all:frontend/dist
var assets embed.FS

// appVersion is stamped by the release workflow:
//
//	wails3 task build VERSION=vX.Y.Z
//
// Local builds keep "dev", which disables the updater.
var appVersion = "dev"

// relaunchWait bounds how long a freshly installed build waits for the
// instance that spawned it. Shutdown can take up to a minute when a mounted
// drive has to drain, so this is deliberately generous.
const relaunchWait = 2 * time.Minute

func main() {
	// A build launched by the updater waits for its predecessor to exit so the
	// single-backend lock is free before startup tries to acquire it.
	if pid, ok := updater.WaitPIDFromArgs(os.Args[1:]); ok {
		updater.WaitForExit(pid, relaunchWait, processlock.ProcessRunning)
	}

	app := NewApp(appVersion)

	wailsApp := application.New(application.Options{
		Name: "TDrive",
		// The default About panel reads Name/Description/Icon (there is no
		// v2-style mac.AboutInfo any more), so the version lives here.
		Description: "Version " + appVersion + "\nTelegram-backed desktop drive.",
		Services:    app.services(),
		Assets: application.AssetOptions{
			Handler: application.AssetFileServerFS(assets),
		},
		Mac: application.MacOptions{
			ApplicationShouldTerminateAfterLastWindowClosed: true,
		},
		// Match the window BackgroundColour below so the phone shows the TDrive
		// Vault backdrop, not a white flash, before the WebView paints.
		IOS: application.IOSOptions{
			EnableInlineMediaPlayback: true,
			DisableBounce:             true,
			BackgroundColour:          application.NewRGB(14, 23, 28),
		},
		Android: application.AndroidOptions{
			DisableOverscroll: true,
			BackgroundColour:  application.NewRGB(14, 23, 28),
		},
	})
	app.wails = wailsApp
	// Wire OS suspend/resume on phones; a no-op on desktop.
	registerMobileLifecycle(app, wailsApp)
	// macOS only; nil elsewhere keeps Windows/Linux without a menu bar.
	if menu := buildAppMenu(app, wailsApp); menu != nil {
		wailsApp.Menu.Set(menu)
	}

	window := wailsApp.Window.NewWithOptions(application.WebviewWindowOptions{
		Title:     "TDrive",
		Width:     1024,
		Height:    768,
		MinWidth:  800,
		MinHeight: 600,
		// Match the no-preference TDrive Vault canvas during native window
		// creation. The frontend synchronises this backdrop to the resolved
		// light/dark palette as soon as the runtime is ready.
		BackgroundColour: application.NewRGB(14, 23, 28),
		// Native file drop: dropped folders and files arrive as absolute paths,
		// which is the only way to accept a mixed files+folders selection (the
		// OS open dialogs cannot). Drop zones opt in via data-file-drop-target.
		EnableFileDrop: true,
		URL:            "/",
		Mac: application.MacWindow{
			TitleBar: application.MacTitleBarDefault,
		},
	})

	// Native file drop: hand the dropped absolute paths to the frontend, which
	// resolves the target folder and runs the import flow. The handler itself
	// is always registered; ServiceStartup/SetFileDropEnabled gate whether it
	// forwards drops, since the frontend turns forwarding off for the
	// duration of an internal drag-to-move.
	window.OnWindowEvent(events.Common.WindowFilesDropped, func(e *application.WindowEvent) {
		if !app.fileDropAllowed() {
			return
		}
		ctx := e.Context()
		paths := ctx.DroppedFiles()
		if len(paths) == 0 {
			return
		}
		var x, y int
		if target := ctx.DropTargetDetails(); target != nil {
			x, y = target.X, target.Y
		}
		app.emit("files_dropped", map[string]any{
			"x":     x,
			"y":     y,
			"paths": paths,
		})
	})

	if err := wailsApp.Run(); err != nil {
		log.Fatal(err)
	}
}
