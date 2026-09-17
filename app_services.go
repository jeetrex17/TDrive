package main

import (
	"context"

	"TDrive/backend/core"
	fileservice "TDrive/backend/services/file"
	readservice "TDrive/backend/services/read"

	"github.com/wailsapp/wails/v3/pkg/application"
)

// The frontend API is split across several Wails services, one per domain, and
// the generator gives each of them its own TypeScript module. That split is the
// point: the device, drive, encryption, media and update domains can each be
// changed without opening a file any of the others use, which one 105-method
// App could never offer.
//
// App is the root service and keeps what is genuinely process-wide -- the
// backend lock, the engine, and the startup and shutdown order -- along with
// the domains not lifted out yet: files and transfers, the mount, the gallery
// and the login session. Every other service is a method holder handed its
// dependencies in initServices.
//
// Domain services deliberately have no ServiceStartup of their own. Startup
// order here is load-bearing: the updater only discards the previous version's
// rollback copy once mounting has proved this build healthy, and independent
// per-service startups would either never reach that check or reach it after a
// broken build had already thrown the rollback away. One owner of the sequence
// means one place to read it. See App.ServiceStartup.

// serviceHost is the sliver of the root service a domain service may see: the
// engine its calls run against, the drive those calls are scoped to, the
// lifecycle context that cancels them at shutdown, and the one event bus back
// to the webview.
//
// Services take this instead of *App on purpose. App still owns the mount, the
// gallery and the in-flight transfers, and a service handed that whole struct
// would sooner or later reach into all of it -- which is how App grew to 105
// methods to begin with. With only these four calls in reach, a domain can be
// moved, tested or replaced without dragging the root's state behind it.
type serviceHost interface {
	// coreEngine is the running engine, or nil until ServiceStartup has built
	// one. Callers must check it: the webview can dispatch a bound call while
	// startup is still in flight, and after a failed startup it stays nil for
	// the life of the process.
	coreEngine() *core.Engine
	// activeChannelID is the drive that channel-scoped calls run against, or 0
	// when there is no engine yet.
	activeChannelID() int64
	// appContext is the lifecycle context, cancelled just before shutdown.
	appContext() context.Context
	// emit publishes on the single named event bus back to the webview.
	emit(name string, args ...any)
}

// engineFileService and engineReadService reach the engine's sub-services
// without going through App, so a bound method can move between services
// without dragging an accessor along with it. Both report the same
// "backend not ready" error the frontend already knows how to present, which
// is why every caller can treat a missing engine and a missing sub-service the
// same way.
func engineFileService(engine *core.Engine) (*fileservice.Service, error) {
	if engine == nil {
		return nil, errBackendUnavailable
	}
	if svc := engine.FileService(); svc != nil {
		return svc, nil
	}
	return nil, errBackendUnavailable
}

func engineReadService(engine *core.Engine) (*readservice.Service, error) {
	if engine == nil {
		return nil, errBackendUnavailable
	}
	if svc := engine.ReadService(); svc != nil {
		return svc, nil
	}
	return nil, errBackendUnavailable
}

// coreEngine satisfies serviceHost. It is a second name for the engine field
// because a method cannot share a name with a field it returns.
func (a *App) coreEngine() *core.Engine {
	if a == nil {
		return nil
	}
	return a.engine
}

// activeChannelID satisfies serviceHost. ActiveChannelID stays exported and
// bound for the frontend; this is the same value under the name services use.
func (a *App) activeChannelID() int64 {
	return a.ActiveChannelID()
}

// initServices constructs the domain services and wires the couplings between
// them, so every edge in the service graph is visible in one place instead of
// being discovered a field at a time. NewApp calls it, and so does any test
// that builds an App by hand, which keeps the two graphs identical.
func (a *App) initServices(version string) {
	a.device = &DeviceService{}
	// App is passed twice: once as the host, once as the mount gate a key
	// change has to hold. Both become MountService's job later.
	a.encryption = newEncryptionService(a, a)
	a.drives = newDriveService(a)
	// Same two roles again: the media domain holds the gate only while it
	// publishes an original-image capability.
	a.media = newMediaService(a, a)
	// The updater closes native players before it replaces the bundle they run
	// from, which is the one edge between two domain services.
	a.updates = newUpdateService(a, a, a.media, version)
}

// quit satisfies appShell. It tolerates a missing application handle so a
// service can be driven in a test without a running webview.
func (a *App) quit() {
	if a == nil || a.wails == nil {
		return
	}
	a.wails.Quit()
}

// openURL satisfies appShell. Only the backend ever supplies the URL; nothing
// the frontend passes reaches here.
func (a *App) openURL(url string) {
	if a == nil || a.wails == nil {
		return
	}
	_ = a.wails.Browser.OpenURL(url)
}

// services lists every service to register with Wails, in the order the
// lifecycle requires. App is first and must stay first: Wails starts services
// in slice order and shuts them down in reverse, so the service that creates
// the engine comes up before anything that could use it and is torn down last,
// once every domain has stopped touching it.
func (a *App) services() []application.Service {
	return []application.Service{
		application.NewService(a),
		application.NewService(a.device),
		application.NewService(a.encryption),
		application.NewService(a.drives),
		application.NewService(a.media),
		application.NewService(a.updates),
	}
}
