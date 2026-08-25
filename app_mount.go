package main

import (
	"context"
	"errors"
	"fmt"

	"TDrive/backend/core"
	"TDrive/backend/mountcontroller"
	"TDrive/backend/mountsafe"
)

var errAppMountLifecycleTerminal = errors.New("TDrive is shutting down")

type appMountController interface {
	Start(context.Context, mountcontroller.Drive, mountcontroller.StartOptions) (mountcontroller.Status, error)
	Status() mountcontroller.Status
	Open(context.Context) error
	Stop(context.Context) (mountcontroller.Status, error)
	Close(context.Context) error
}

type appAggregateMountController interface {
	StartDrives(context.Context, []mountcontroller.Drive, mountcontroller.StartOptions) (mountcontroller.Status, error)
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
	ctx, cancel := a.mountMutationContext()
	defer cancel()
	release, err := a.acquireMountLifecycle(ctx)
	if err != nil {
		return MountView{}, fmt.Errorf("mount: wait for encryption transition: %w", err)
	}
	defer release()

	controller, err := a.requireMountController()
	if err != nil {
		return MountView{}, mountsafe.SanitizeError(err)
	}
	drive, err := a.resolveActiveMountDriveContext(ctx)
	if err != nil {
		return MountView{}, mountsafe.SanitizeError(err)
	}
	status, err := controller.Start(ctx, drive, mountcontroller.StartOptions{Mode: mountcontroller.ModeAuto})
	return mountView(status), mountsafe.SanitizeError(err)
}

// MountDrives attaches an explicit selection inside one TDrive volume. The
// client supplies IDs only; titles, kinds, and encryption state are resolved
// from the authoritative local projection before the controller is called.
func (a *App) MountDrives(channelIDs []int64) (MountView, error) {
	ctx, cancel := a.mountMutationContext()
	defer cancel()
	release, err := a.acquireMountLifecycle(ctx)
	if err != nil {
		return MountView{}, fmt.Errorf("mount: wait for encryption transition: %w", err)
	}
	defer release()

	controller, err := a.requireMountController()
	if err != nil {
		return MountView{}, mountsafe.SanitizeError(err)
	}
	aggregate, ok := controller.(appAggregateMountController)
	if !ok {
		return MountView{}, fmt.Errorf("mount: selected-drive mounting is unavailable")
	}
	drives, err := a.resolveMountDrivesContext(ctx, channelIDs)
	if err != nil {
		return MountView{}, mountsafe.SanitizeError(err)
	}
	status, err := aggregate.StartDrives(ctx, drives, mountcontroller.StartOptions{Mode: mountcontroller.ModeAuto})
	return mountView(status), mountsafe.SanitizeError(err)
}

func (a *App) MountStatus() MountView {
	controller := a.currentMountController()
	if controller == nil {
		return MountView{Phase: "idle"}
	}
	return mountView(controller.Status())
}

func (a *App) OpenMountedDrive() error {
	controller, err := a.requireMountController()
	if err != nil {
		return mountsafe.SanitizeError(err)
	}
	return mountsafe.SanitizeError(controller.Open(a.appContext()))
}

func (a *App) UnmountDrive() (MountView, error) {
	ctx, cancel := a.mountMutationContext()
	defer cancel()
	release, err := a.acquireMountLifecycle(ctx)
	if err != nil {
		return MountView{}, fmt.Errorf("mount: wait for encryption transition: %w", err)
	}
	defer release()

	controller, err := a.requireMountController()
	if err != nil {
		return MountView{}, mountsafe.SanitizeError(err)
	}
	status, err := controller.Stop(ctx)
	return mountView(status), mountsafe.SanitizeError(err)
}

func (a *App) mountMutationContext() (context.Context, context.CancelFunc) {
	return context.WithTimeout(a.appContext(), encryptionMountTransitionTimeout)
}

func (a *App) acquireMountLifecycle(ctx context.Context) (func(), error) {
	if a == nil {
		return nil, fmt.Errorf("mount: backend is not ready")
	}
	if err := a.mountLifecycle.Lock(ctx); err != nil {
		return nil, err
	}
	if a.mountLifecycleTerminal {
		a.mountLifecycle.Unlock()
		return nil, errAppMountLifecycleTerminal
	}
	return a.mountLifecycle.Unlock, nil
}

// shutdownMountController makes the lifecycle terminal before closing the
// controller. It intentionally bypasses acquireMountLifecycle's terminal
// rejection so a Wails shutdown triggered after Logout is idempotent and
// cannot recursively close or deadlock the controller.
func (a *App) shutdownMountController(ctx context.Context) error {
	if a == nil {
		return nil
	}
	if err := a.mountLifecycle.Lock(ctx); err != nil {
		return err
	}
	defer a.mountLifecycle.Unlock()
	if a.mountLifecycleTerminal {
		return nil
	}
	a.mountLifecycleTerminal = true
	return a.closeMountControllerLocked(ctx)
}

// closeMountControllerLocked requires mountLifecycle to be held by the caller.
func (a *App) closeMountControllerLocked(ctx context.Context) error {
	controller := a.currentMountController()
	if controller == nil {
		return nil
	}
	return controller.Close(ctx)
}

func (a *App) requireMountController() (appMountController, error) {
	return a.ensureMountController()
}

func (a *App) ensureMountController() (appMountController, error) {
	if a == nil {
		return nil, fmt.Errorf("mount: backend is not ready")
	}
	a.mountMu.Lock()
	defer a.mountMu.Unlock()
	if a.mountController != nil {
		return a.mountController, nil
	}
	factory := a.mountControllerFactory
	if factory == nil {
		factory = func(engine *core.Engine) (appMountController, error) {
			return mountcontroller.New(engine)
		}
	}
	controller, err := factory(a.engine)
	if err != nil {
		return nil, err
	}
	if controller == nil {
		return nil, fmt.Errorf("mount: backend is not ready")
	}
	a.mountController = controller
	return controller, nil
}

func (a *App) currentMountController() appMountController {
	if a == nil {
		return nil
	}
	a.mountMu.Lock()
	defer a.mountMu.Unlock()
	return a.mountController
}

func (a *App) resolveActiveMountDriveContext(ctx context.Context) (mountcontroller.Drive, error) {
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
			encrypted, unlocked, err := a.mountDriveEncryptionStatus(ctx, channel.ChannelID, channel.Kind)
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

func (a *App) resolveMountDrivesContext(ctx context.Context, channelIDs []int64) ([]mountcontroller.Drive, error) {
	requested := append([]int64(nil), channelIDs...)
	if a != nil && a.mountDrivesResolver != nil {
		return a.mountDrivesResolver(requested)
	}
	if a == nil || a.engine == nil || a.engine.ChannelService() == nil {
		return nil, fmt.Errorf("mount: backend is not ready")
	}
	if len(requested) == 0 || len(requested) > 256 {
		return nil, fmt.Errorf("mount: select between 1 and 256 drives")
	}
	selected := make(map[int64]struct{}, len(requested))
	for _, channelID := range requested {
		if channelID <= 0 {
			return nil, fmt.Errorf("mount: drive selection is invalid")
		}
		if _, exists := selected[channelID]; exists {
			return nil, fmt.Errorf("mount: drive selection contains duplicates")
		}
		selected[channelID] = struct{}{}
	}

	channels, err := a.engine.ChannelService().ListChannels()
	if err != nil {
		return nil, fmt.Errorf("mount: list drives: %w", err)
	}
	drives := make([]mountcontroller.Drive, 0, len(selected))
	for _, channel := range channels {
		if _, exists := selected[channel.ChannelID]; !exists {
			continue
		}
		encrypted, unlocked, err := a.mountDriveEncryptionStatus(ctx, channel.ChannelID, channel.Kind)
		if err != nil {
			return nil, err
		}
		drives = append(drives, mountcontroller.Drive{
			ID:                 channel.ChannelID,
			Title:              channel.Title,
			Kind:               channel.Kind,
			Encrypted:          encrypted,
			EncryptionUnlocked: unlocked,
		})
	}
	if len(drives) != len(selected) {
		return nil, fmt.Errorf("mount: one or more selected drives are unavailable")
	}
	return drives, nil
}

func (a *App) mountDriveEncryptionStatus(ctx context.Context, channelID int64, kind string) (bool, bool, error) {
	var engine *core.Engine
	var refresh func(context.Context, int64) error
	if a != nil {
		engine = a.engine
		refresh = a.mountEncryptionPolicyRefresh
	}
	policy, err := engine.ResolveMountEncryptionPolicy(
		ctx,
		channelID,
		kind,
		refresh,
		func(format string, args ...any) { fmt.Printf(format, args...) },
	)
	if err != nil {
		return false, false, err
	}
	return policy.Encrypted, policy.Unlocked, nil
}

func (a *App) appContext() context.Context {
	if a != nil && a.ctx != nil {
		return a.ctx
	}
	return context.Background()
}

func mountView(status mountcontroller.Status) MountView {
	location := status.Location
	if mountsafe.ContainsSensitive(location) {
		location = ""
	}
	return MountView{
		Phase:           mountViewPhase(status.Phase),
		Mounted:         status.Mounted,
		Mode:            string(status.Mode),
		WriteState:      string(status.WriteState),
		AcceptingWrites: status.AcceptingWrites,
		ActiveWrites:    status.ActiveWrites,
		Label:           status.Label,
		Location:        location,
		Error:           mountsafe.Message(status.Error),
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
