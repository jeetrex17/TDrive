package main

import (
	"fmt"
	goruntime "runtime"
	"strings"

	"TDrive/backend/updater"

	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// updateRepo is where desktop releases are published.
const updateRepo = updater.DefaultRepo

// updatesOpenEvent asks the frontend to open the Updates panel (native menu).
const updatesOpenEvent = "updates:open"

// updateStateEvent carries every updater.State transition to the frontend.
const updateStateEvent = "update_state"

// AppVersionInfo describes the running build for the About/Updates panel.
type AppVersionInfo struct {
	Version  string `json:"version"`
	OS       string `json:"os"`
	Arch     string `json:"arch"`
	DevBuild bool   `json:"dev_build"`
}

// initUpdater builds the updater once the Wails context exists so state
// changes can be forwarded as runtime events. It never touches the network
// on its own; the frontend owns the schedule.
func (a *App) initUpdater() {
	a.updates = updater.New(updater.Options{
		CurrentVersion: a.version,
		Source:         updater.NewGitHubSource(updateRepo, "TDrive/"+a.version, nil),
		UserAgent:      "TDrive/" + a.version,
		OnChange: func(state updater.State) {
			if a.ctx != nil {
				runtime.EventsEmit(a.ctx, updateStateEvent, state)
			}
		},
	})
}

// finishUpdateCleanup removes the previous version once this build has come
// up healthy. Mount initialization is part of that health check because a
// build that cannot mount drives must keep the rollback copy recoverable.
func (a *App) finishUpdateCleanup(mountInitErr error) {
	if a.updates == nil {
		return
	}
	scheduleUpdateCleanup(mountInitErr, a.updates.CleanupAfterRestart)
}

func scheduleUpdateCleanup(mountInitErr error, cleanup func() error) bool {
	if mountInitErr != nil || cleanup == nil {
		return false
	}
	go func() {
		if err := cleanup(); err != nil {
			fmt.Printf("Warning: update cleanup failed: %v\n", err)
		}
	}()
	return true
}

// requestUpdatesPanel is the native "Check for Updates…" menu action.
func (a *App) requestUpdatesPanel() {
	if a.ctx == nil {
		return
	}
	runtime.EventsEmit(a.ctx, updatesOpenEvent)
}

// AppVersion returns the build identity shown in the Updates panel.
func (a *App) AppVersion() AppVersionInfo {
	_, err := updater.ParseVersion(a.version)
	return AppVersionInfo{
		Version:  strings.TrimPrefix(a.version, "v"),
		OS:       goruntime.GOOS,
		Arch:     goruntime.GOARCH,
		DevBuild: err != nil,
	}
}

// GetUpdateState returns the current updater snapshot for hydration.
func (a *App) GetUpdateState() updater.State {
	if a.updates == nil {
		return updater.State{Phase: updater.PhaseDisabled, CurrentVersion: a.version}
	}
	return a.updates.State()
}

// CheckForUpdate contacts GitHub and returns the resulting state. Failures
// are reported inside the state rather than as an error so the panel has a
// single source of truth.
func (a *App) CheckForUpdate() updater.State {
	if a.updates == nil {
		return a.GetUpdateState()
	}
	return a.updates.Check(a.appContext())
}

// DownloadUpdate starts fetching the available release in the background.
func (a *App) DownloadUpdate() error {
	if a.updates == nil {
		return updater.ErrDisabled
	}
	return a.updates.StartDownload()
}

// CancelUpdateDownload aborts the in-flight download.
func (a *App) CancelUpdateDownload() {
	if a.updates != nil {
		a.updates.CancelDownload()
	}
}

// InstallUpdateAndRestart swaps the verified payload into place, launches the
// new version and quits. Native players are closed first so their sidecar
// binaries are not in use while the bundle is replaced; the regular shutdown
// path ejects any mounted drive and releases the backend lock, which the new
// instance waits for before it starts.
func (a *App) InstallUpdateAndRestart() error {
	if a.updates == nil {
		return updater.ErrDisabled
	}
	a.closeAllNativeMedia()
	if err := a.updates.Install(); err != nil {
		return err
	}
	if err := a.updates.Relaunch(); err != nil {
		// The swap already succeeded; reopening TDrive by hand still lands on
		// the new version, so this is a warning, not a failure.
		fmt.Printf("Warning: relaunch after update failed: %v\n", err)
	}
	runtime.Quit(a.ctx)
	return nil
}

// OpenUpdatePage opens the newest release's GitHub page in the browser, or
// the releases index when no newer release is known. The URL never comes
// from the frontend.
func (a *App) OpenUpdatePage() {
	if a.ctx == nil {
		return
	}
	url := ""
	if a.updates != nil {
		url = a.updates.ReleasePageURL()
	}
	if url == "" {
		url = "https://github.com/" + updateRepo + "/releases/latest"
	}
	runtime.BrowserOpenURL(a.ctx, url)
}
