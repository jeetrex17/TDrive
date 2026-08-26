package main

import (
	"embed"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/mac"
)

//go:embed all:frontend/dist
var assets embed.FS

func main() {
	// Create an instance of the app structure
	app := NewApp()

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
		// macOS: explicit standard titlebar so the green zoom button is
		// fully active. Without this, the default Wails behavior leaves
		// the button visually dim until first manual resize.
		Mac: &mac.Options{
			TitleBar:             mac.TitleBarDefault(),
			WebviewIsTransparent: true,
		},
	})
	if err != nil {
		println("Error:", err.Error())
	}
}
