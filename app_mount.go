package main

import (
	"context"
	"fmt"

	"TDrive/backend/mountcontroller"
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
	Phase        string         `json:"phase"`
	Mounted      bool           `json:"mounted"`
	Mode         string         `json:"mode,omitempty"`
	Label        string         `json:"label,omitempty"`
	Location     string         `json:"location,omitempty"`
	Error        string         `json:"error,omitempty"`
	Drive        MountDriveView `json:"drive,omitempty"`
	WindowsDrive string         `json:"windows_drive,omitempty"`
}

type MountDriveView struct {
	ID    int64  `json:"id,omitempty"`
	Title string `json:"title,omitempty"`
	Kind  string `json:"kind,omitempty"`
}

// MountDrive attaches the active drive without changing it. The controller
// pins this immutable drive record until the user disconnects it.
func (a *App) MountDrive() (MountView, error) {
	controller, err := a.requireMountController()
	if err != nil {
		return MountView{}, err
	}
	drive, err := a.resolveActiveMountDrive()
	if err != nil {
		return MountView{}, err
	}
	status, err := controller.Start(a.appContext(), drive, mountcontroller.StartOptions{})
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
			return mountcontroller.Drive{
				ID:    channel.ChannelID,
				Title: channel.Title,
				Kind:  channel.Kind,
			}, nil
		}
	}
	return mountcontroller.Drive{}, fmt.Errorf("mount: active drive is unavailable")
}

func (a *App) appContext() context.Context {
	if a != nil && a.ctx != nil {
		return a.ctx
	}
	return context.Background()
}

func mountView(status mountcontroller.Status) MountView {
	return MountView{
		Phase:    mountViewPhase(status.Phase),
		Mounted:  status.Mounted,
		Mode:     status.Mode,
		Label:    status.Label,
		Location: status.Location,
		Error:    status.Error,
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
	case mountcontroller.PhaseFailed:
		return "error"
	default:
		return "idle"
	}
}
