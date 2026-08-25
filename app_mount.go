package main

import (
	"context"
	"errors"
	"fmt"

	"TDrive/backend/mountcontroller"
	"TDrive/backend/projection"
)

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
	controller, err := a.requireMountController()
	if err != nil {
		return MountView{}, err
	}
	status, err := controller.Stop(a.appContext())
	return mountView(status), err
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
			encrypted, err := a.mountDriveEncryptionEnabled(channel.ChannelID, channel.Kind)
			if err != nil {
				return mountcontroller.Drive{}, err
			}
			return mountcontroller.Drive{
				ID:        channel.ChannelID,
				Title:     channel.Title,
				Kind:      channel.Kind,
				Encrypted: encrypted,
			}, nil
		}
	}
	return mountcontroller.Drive{}, fmt.Errorf("mount: active drive is unavailable")
}

func (a *App) mountDriveEncryptionEnabled(channelID int64, kind string) (bool, error) {
	if kind != mountcontroller.DriveKindPersonal {
		return false, nil
	}
	if a == nil || a.engine == nil {
		return false, fmt.Errorf("mount: encryption eligibility is unavailable")
	}
	reads := a.engine.ReadService()
	if reads == nil || reads.DB == nil {
		return false, fmt.Errorf("mount: encryption eligibility is unavailable")
	}
	config, err := projection.GetEncryptionConfig(reads.DB, channelID)
	if errors.Is(err, projection.ErrEncryptionConfigNotFound) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("mount: check encryption eligibility: %w", err)
	}
	return config.Enabled, nil
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
