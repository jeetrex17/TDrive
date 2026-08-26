package main

import (
	"embed"
	"os"
	"time"

	"TDrive/backend/processlock"
	"TDrive/backend/updater"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/mac"
)

//go:embed all:frontend/dist
var assets embed.FS

// appVersion is stamped by the release workflow:
//
//	wails build -ldflags "-X main.appVersion=v1.7.0"
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

	// Create an instance of the app structure
	app := NewApp()
	app.version = appVersion

	// Create application with options
	err := wails.Run(&options.App{
		Title:     "TDrive",
		Width:     1024,
		Height:    768,
		MinWidth:  800,
		MinHeight: 600,
		AssetServer: &assetserver.Options{
			Assets: assets,
		},
		// Match the no-preference Tokyo Night canvas during native window
		// creation. The frontend synchronises this backdrop to the resolved
		// light/dark palette as soon as the Wails runtime is ready.
		BackgroundColour: &options.RGBA{R: 26, G: 27, B: 38, A: 255},
		OnStartup:        app.startup,
		OnShutdown:       app.shutdown,
		// Native file drop: dropped folders and files arrive as absolute paths,
		// which is the only way to accept a mixed files+folders selection (the
		// OS open dialogs cannot). Drop zones opt in via the --wails-drop-target
		// CSS property in the frontend.
		DragAndDrop: &options.DragAndDrop{
			EnableFileDrop: true,
		},
		Bind: []interface{}{
			app,
		},
		// macOS only; nil elsewhere keeps Windows/Linux without a menu bar.
		Menu: buildAppMenu(app),
		// macOS: explicit standard titlebar so the green zoom button is
		// fully active. Without this, the default Wails behavior leaves
		// the button visually dim until first manual resize.
		Mac: &mac.Options{
			TitleBar:             mac.TitleBarDefault(),
			WebviewIsTransparent: true,
			About:                macAbout(app.AppVersion().Version),
		},
	})
	if err != nil {
		println("Error:", err.Error())
	}
}
