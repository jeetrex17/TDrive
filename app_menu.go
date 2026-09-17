package main

import (
	goruntime "runtime"

	"github.com/wailsapp/wails/v3/pkg/application"
)

const repoURL = "https://github.com/" + updateRepo

// buildAppMenu returns the native menu bar. Only macOS gets one: the system
// menu bar always exists there, so "Check for Updates…" has a conventional
// home. Windows and Linux would gain a menu strip the app otherwise never
// shows, so they rely on the in-app account menu instead.
//
// The AppMenu role reproduces the About/Hide/Quit items Wails installs when
// no menu is configured, reading the About panel text from
// application.Options.Name/Description, so the updater entry lives under
// Help instead of trying to extend that fixed role.
func buildAppMenu(app *App, wailsApp *application.App) *application.Menu {
	if goruntime.GOOS != "darwin" {
		return nil
	}
	menu := wailsApp.NewMenu()
	menu.AddRole(application.AppMenu)
	// EditMenu reproduces the defaults Wails installs when no menu is
	// configured; dropping it would break Cmd+C/V in the webview.
	menu.AddRole(application.EditMenu)
	menu.AddRole(application.WindowMenu)

	help := menu.AddSubmenu("Help")
	help.Add("Check for Updates…").OnClick(func(*application.Context) {
		app.updates.requestPanel()
	})
	help.AddSeparator()
	help.Add("TDrive on GitHub").OnClick(func(*application.Context) {
		if app.wails != nil {
			_ = app.wails.Browser.OpenURL(repoURL)
		}
	})
	return menu
}
