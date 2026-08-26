package main

import (
	goruntime "runtime"

	"github.com/wailsapp/wails/v2/pkg/menu"
	"github.com/wailsapp/wails/v2/pkg/options/mac"
	"github.com/wailsapp/wails/v2/pkg/runtime"
)

const repoURL = "https://github.com/" + updateRepo

// buildAppMenu returns the native menu bar. Only macOS gets one: the system
// menu bar always exists there, so "Check for Updates…" has a conventional
// home. Windows and Linux would gain a menu strip the app otherwise never
// shows, so they rely on the in-app account menu instead.
//
// Wails renders the application menu from a fixed role (About, Hide, Quit)
// that accepts no extra items, so the updater entry lives under Help.
func buildAppMenu(app *App) *menu.Menu {
	if goruntime.GOOS != "darwin" {
		return nil
	}
	help := menu.NewMenu()
	help.AddText("Check for Updates…", nil, func(*menu.CallbackData) {
		app.requestUpdatesPanel()
	})
	help.AddSeparator()
	help.AddText("TDrive on GitHub", nil, func(*menu.CallbackData) {
		if app.ctx != nil {
			runtime.BrowserOpenURL(app.ctx, repoURL)
		}
	})
	// AppMenu/EditMenu/WindowMenu reproduce the defaults Wails installs when
	// no menu is configured; dropping EditMenu would break Cmd+C/V in the
	// webview.
	return menu.NewMenuFromItems(menu.AppMenu(), menu.EditMenu(), menu.WindowMenu(), menu.SubMenu("Help", help))
}

// macAbout feeds the native "About TDrive" panel with the build version.
func macAbout(version string) *mac.AboutInfo {
	return &mac.AboutInfo{
		Title:   "TDrive",
		Message: "Version " + version + "\nTelegram-backed desktop drive.",
	}
}
