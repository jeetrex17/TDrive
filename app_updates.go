package main

import (
	"fmt"
	goruntime "runtime"
	"strings"

	"TDrive/backend/updater"

	"github.com/wailsapp/wails/v3/pkg/application"
)

// UpdateService owns the desktop release lifecycle: reporting what this build
// is, asking GitHub whether there is a newer one, downloading it, swapping it
// into place and restarting into it.
//
// These methods sit on one type because they all read or drive a single phase
// machine, and because every one of them is meaningless without it: a "dev"
// build and every mobile build leave that machine nil, and each method here
// then degrades to PhaseDisabled instead of pretending an update is possible.
// Grouping them also keeps the one destructive step -- replacing the bundle the
// process is running from -- next to the checks that decide whether there is
// anything worth replacing it with.
//
// The rollback cleanup that runs at startup is deliberately not a method the
// frontend can call; see finishCleanup.
type UpdateService struct {
	host  serviceHost
	shell appShell
	// players is consulted only by InstallUpdateAndRestart; see there for why
	// the updater has to know about media at all.
	players nativeMediaCloser
	// version is the build stamp from main.appVersion ("dev" for local builds).
	version string
	// service drives the check/download/install lifecycle. It is built during
	// startup rather than at construction so its state events have a webview
	// to reach.
	service *updater.Service
}

func newUpdateService(host serviceHost, shell appShell, players nativeMediaCloser, version string) *UpdateService {
	return &UpdateService{host: host, shell: shell, players: players, version: version}
}

// appShell is the part of the running Wails application the updater has to
// drive directly: ending the process once the new build is in place, and
// handing a release page to the user's browser. It is kept out of serviceHost
// because no other domain has any business quitting the app.
type appShell interface {
	quit()
	openURL(url string)
}

// nativeMediaCloser closes every out-of-webview player. Whoever owns those
// players implements it; the updater depends on the capability rather than on
// the owner so that moving the players between services stays a one-line
// rewire in initServices.
type nativeMediaCloser interface {
	closeAllNativeMedia()
}

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
func (s *UpdateService) initUpdater() {
	// Mobile stores own the update path, so leave the updater nil: every entry
	// point then reports PhaseDisabled, exactly like a "dev" desktop build.
	if application.System.IsMobile() {
		return
	}
	s.service = updater.New(updater.Options{
		CurrentVersion: s.version,
		Source:         updater.NewGitHubSource(updateRepo, "TDrive/"+s.version, nil),
		UserAgent:      "TDrive/" + s.version,
		OnChange: func(state updater.State) {
			s.host.emit(updateStateEvent, state)
		},
	})
}

// finishCleanup removes the previous version once this build has come up
// healthy. Mount initialization is part of that health check because a build
// that cannot mount drives must keep the rollback copy recoverable.
//
// It takes the mount result as an argument instead of asking for it because
// that keeps the health check in the hands of the root service, which is the
// only place that knows the whole startup ran in the right order. An updater
// that decided this for itself would eventually run before the mount attempt
// and throw away the rollback of a build the user cannot escape.
func (s *UpdateService) finishCleanup(mountInitErr error) {
	if s.service == nil {
		return
	}
	scheduleUpdateCleanup(mountInitErr, s.service.CleanupAfterRestart)
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

// requestPanel is the native "Check for Updates…" menu action.
func (s *UpdateService) requestPanel() {
	s.host.emit(updatesOpenEvent)
}

// AppVersion returns the build identity shown in the Updates panel.
func (s *UpdateService) AppVersion() AppVersionInfo {
	_, err := updater.ParseVersion(s.version)
	return AppVersionInfo{
		Version:  strings.TrimPrefix(s.version, "v"),
		OS:       goruntime.GOOS,
		Arch:     goruntime.GOARCH,
		DevBuild: err != nil,
	}
}

// GetUpdateState returns the current updater snapshot for hydration.
func (s *UpdateService) GetUpdateState() updater.State {
	if s.service == nil {
		return updater.State{Phase: updater.PhaseDisabled, CurrentVersion: s.version}
	}
	return s.service.State()
}

// CheckForUpdate contacts GitHub and returns the resulting state. Failures
// are reported inside the state rather than as an error so the panel has a
// single source of truth.
func (s *UpdateService) CheckForUpdate() updater.State {
	if s.service == nil {
		return s.GetUpdateState()
	}
	return s.service.Check(s.host.appContext())
}

// DownloadUpdate starts fetching the available release in the background.
func (s *UpdateService) DownloadUpdate() error {
	if s.service == nil {
		return updater.ErrDisabled
	}
	return s.service.StartDownload()
}

// CancelUpdateDownload aborts the in-flight download.
func (s *UpdateService) CancelUpdateDownload() {
	if s.service != nil {
		s.service.CancelDownload()
	}
}

// InstallUpdateAndRestart swaps the verified payload into place, launches the
// new version and quits. Native players are closed first so their sidecar
// binaries are not in use while the bundle is replaced; the regular shutdown
// path ejects any mounted drive and releases the backend lock, which the new
// instance waits for before it starts.
func (s *UpdateService) InstallUpdateAndRestart() error {
	if s.service == nil {
		return updater.ErrDisabled
	}
	s.players.closeAllNativeMedia()
	if err := s.service.Install(); err != nil {
		return err
	}
	if err := s.service.Relaunch(); err != nil {
		// The swap already succeeded; reopening TDrive by hand still lands on
		// the new version, so this is a warning, not a failure.
		fmt.Printf("Warning: relaunch after update failed: %v\n", err)
	}
	s.shell.quit()
	return nil
}

// OpenUpdatePage opens the newest release's GitHub page in the browser, or
// the releases index when no newer release is known. The URL never comes
// from the frontend.
func (s *UpdateService) OpenUpdatePage() {
	url := ""
	if s.service != nil {
		url = s.service.ReleasePageURL()
	}
	if url == "" {
		url = "https://github.com/" + updateRepo + "/releases/latest"
	}
	s.shell.openURL(url)
}
