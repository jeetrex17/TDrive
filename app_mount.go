package main

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"TDrive/backend/mountcontroller"
	"TDrive/backend/mountpolicy"
)

var errAppMountLifecycleTerminal = errors.New("TDrive is shutting down")

// mountLifecycleGate is a zero-value, context-aware binary gate. Unlike a
// sync.Mutex, a shutdown or vault transition can abandon a queued acquisition
// when its deadline expires instead of freezing the UI indefinitely.
type mountLifecycleGate struct {
	once  sync.Once
	token chan struct{}
}

func (gate *mountLifecycleGate) lock(ctx context.Context) error {
	if ctx == nil {
		return fmt.Errorf("mount lifecycle: context is required")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	gate.once.Do(func() { gate.token = make(chan struct{}, 1) })
	select {
	case gate.token <- struct{}{}:
		if err := ctx.Err(); err != nil {
			<-gate.token
			return err
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (gate *mountLifecycleGate) tryLock() bool {
	gate.once.Do(func() { gate.token = make(chan struct{}, 1) })
	select {
	case gate.token <- struct{}{}:
		return true
	default:
		return false
	}
}

func (gate *mountLifecycleGate) unlock() {
	<-gate.token
}

type appMountController interface {
	Start(context.Context, mountcontroller.Drive, mountcontroller.StartOptions) (mountcontroller.Status, error)
	Status() mountcontroller.Status
	Open(context.Context) error
	Stop(context.Context) (mountcontroller.Status, error)
	Close(context.Context) error
}

// MountView is the capability-free mount state exposed to the webview. The
// private WebDAV endpoint never crosses the Go/Wails boundary.
type MountView struct {
	Phase           string         `json:"phase"`
	Mounted         bool           `json:"mounted"`
	Mode            string         `json:"mode,omitempty"`
	WriteState      string         `json:"write_state,omitempty"`
	AcceptingWrites bool           `json:"accepting_writes,omitempty"`
	ActiveWrites    int            `json:"active_writes,omitempty"`
	Label           string         `json:"label,omitempty"`
	Location        string         `json:"location,omitempty"`
	Error           string         `json:"error,omitempty"`
	Drive           MountDriveView `json:"drive,omitempty"`
	WindowsDrive    string         `json:"windows_drive,omitempty"`
}

type MountDriveView struct {
	ID    int64  `json:"id,omitempty"`
	Title string `json:"title,omitempty"`
	Kind  string `json:"kind,omitempty"`
}

// MountDrive attaches the active drive without changing it. The controller
// pins this immutable drive record until the user disconnects it.
func (a *App) MountDrive() (MountView, error) {
	return a.mountDrive(mountcontroller.ModeAuto)
}

// MountDriveReadOnly is the explicit safety fallback for clients that do not
// want write access even when the active personal drive is eligible.
func (a *App) MountDriveReadOnly() (MountView, error) {
	return a.mountDrive(mountcontroller.ModeReadOnly)
}

func (a *App) mountDrive(mode mountcontroller.Mode) (MountView, error) {
	release, err := a.acquireMountLifecycle(a.appContext())
	if err != nil {
		return MountView{}, fmt.Errorf("mount: wait for encryption transition: %w", err)
	}
	defer release()

	controller, err := a.requireMountController()
	if err != nil {
		return MountView{}, err
	}
	drive, err := a.resolveActiveMountDrive()
	if err != nil {
		return MountView{}, err
	}
	status, err := controller.Start(a.appContext(), drive, mountcontroller.StartOptions{Mode: mode})
	return mountView(status), err
}

func (a *App) MountStatus() MountView {
	if a == nil || a.mountController == nil {
		return MountView{Phase: "idle"}
	}
	return mountView(a.mountController.Status())
}

func (a *App) OpenMountedDrive() error {
	controller, err := a.requireMountController()
	if err != nil {
		return err
	}
	return controller.Open(a.appContext())
}

func (a *App) UnmountDrive() (MountView, error) {
	release, err := a.acquireMountLifecycle(a.appContext())
	if err != nil {
		return MountView{}, fmt.Errorf("mount: wait for encryption transition: %w", err)
	}
	defer release()

	controller, err := a.requireMountController()
	if err != nil {
		return MountView{}, err
	}
	status, err := controller.Stop(a.appContext())
	return mountView(status), err
}

func (a *App) acquireMountLifecycle(ctx context.Context) (func(), error) {
	if a == nil {
		return nil, fmt.Errorf("mount: backend is not ready")
	}
	if err := a.mountLifecycle.lock(ctx); err != nil {
		return nil, err
	}
	if a.mountLifecycleTerminal {
		a.mountLifecycle.unlock()
		return nil, errAppMountLifecycleTerminal
	}
	return a.mountLifecycle.unlock, nil
}

// shutdownMountController makes the lifecycle terminal before closing the
// controller. It intentionally bypasses acquireMountLifecycle's terminal
// rejection so a Wails shutdown triggered after Logout is idempotent and
// cannot recursively close or deadlock the controller.
func (a *App) shutdownMountController(ctx context.Context) error {
	if a == nil {
		return nil
	}
	if err := a.mountLifecycle.lock(ctx); err != nil {
		return err
	}
	defer a.mountLifecycle.unlock()
	if a.mountLifecycleTerminal {
		return nil
	}
	a.mountLifecycleTerminal = true
	return a.closeMountControllerLocked(ctx)
}

// closeMountControllerLocked requires mountLifecycle to be held by the caller.
func (a *App) closeMountControllerLocked(ctx context.Context) error {
	if a == nil || a.mountController == nil {
		return nil
	}
	return a.mountController.Close(ctx)
}

func (a *App) requireMountController() (appMountController, error) {
	if a == nil || a.mountController == nil {
		return nil, fmt.Errorf("mount: backend is not ready")
	}
	return a.mountController, nil
}

func (a *App) resolveActiveMountDrive() (mountcontroller.Drive, error) {
	if a != nil && a.mountDriveResolver != nil {
		return a.mountDriveResolver()
	}
	if a == nil || a.engine == nil || a.engine.ChannelService() == nil {
		return mountcontroller.Drive{}, fmt.Errorf("mount: backend is not ready")
	}
	activeID := a.engine.ActiveChannelID()
	if activeID <= 0 {
		return mountcontroller.Drive{}, fmt.Errorf("mount: no active drive")
	}
	channels, err := a.engine.ChannelService().ListChannels()
	if err != nil {
		return mountcontroller.Drive{}, fmt.Errorf("mount: list drives: %w", err)
	}
	for _, channel := range channels {
		if channel.ChannelID == activeID {
			encrypted, unlocked, err := a.mountDriveEncryptionStatus(channel.ChannelID, channel.Kind)
			if err != nil {
				return mountcontroller.Drive{}, err
			}
			return mountcontroller.Drive{
				ID:                 channel.ChannelID,
				Title:              channel.Title,
				Kind:               channel.Kind,
				Encrypted:          encrypted,
				EncryptionUnlocked: unlocked,
			}, nil
		}
	}
	return mountcontroller.Drive{}, fmt.Errorf("mount: active drive is unavailable")
}

func (a *App) mountDriveEncryptionStatus(channelID int64, kind string) (bool, bool, error) {
	if kind != mountcontroller.DriveKindPersonal {
		return false, false, nil
	}
	if a == nil || a.engine == nil {
		return false, false, fmt.Errorf("mount: encryption eligibility is unavailable")
	}
	reads := a.engine.ReadService()
	if reads == nil || reads.DB == nil {
		return false, false, fmt.Errorf("mount: encryption eligibility is unavailable")
	}
	policy, err := mountpolicy.ResolvePersonal(
		a.appContext(),
		reads.DB,
		channelID,
		a.refreshMountEncryptionPolicy,
		func() (bool, error) {
			status, err := a.engine.EncryptionService().StatusContext(a.appContext())
			return status.PasswordRemembered, err
		},
	)
	if err != nil {
		return false, false, err
	}
	return policy.Encrypted, policy.Unlocked, nil
}

func (a *App) refreshMountEncryptionPolicy(ctx context.Context, channelID int64) error {
	if a != nil && a.mountEncryptionPolicyRefresh != nil {
		return a.mountEncryptionPolicyRefresh(ctx, channelID)
	}
	if a == nil || a.engine == nil {
		return mountpolicy.ErrEncryptionPolicyUnavailable
	}
	if err := a.engine.EnsureEncryptionPolicy(ctx, channelID); err != nil {
		fmt.Printf("mount: refresh personal-drive encryption policy: %v\n", err)
		return err
	}
	return nil
}

func (a *App) appContext() context.Context {
	if a != nil && a.ctx != nil {
		return a.ctx
	}
	return context.Background()
}

func mountView(status mountcontroller.Status) MountView {
	return MountView{
		Phase:           mountViewPhase(status.Phase),
		Mounted:         status.Mounted,
		Mode:            string(status.Mode),
		WriteState:      string(status.WriteState),
		AcceptingWrites: status.AcceptingWrites,
		ActiveWrites:    status.ActiveWrites,
		Label:           status.Label,
		Location:        status.Location,
		Error:           status.Error,
		Drive: MountDriveView{
			ID:    status.DriveID,
			Title: status.DriveTitle,
			Kind:  status.DriveKind,
		},
		WindowsDrive: status.WindowsDrive,
	}
}

func mountViewPhase(phase mountcontroller.Phase) string {
	switch phase {
	case mountcontroller.PhasePreparing, mountcontroller.PhaseAttaching:
		return "mounting"
	case mountcontroller.PhaseMounted:
		return "mounted"
	case mountcontroller.PhaseDetaching:
		return "disconnecting"
	case mountcontroller.PhaseDraining:
		return "disconnecting"
	case mountcontroller.PhaseFailed:
		return "error"
	default:
		return "idle"
	}
}
