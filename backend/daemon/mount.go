package daemon

import (
	"context"
	"fmt"
	"strings"

	"TDrive/backend/mountcontroller"
)

func (s *Server) startMount(ctx context.Context, selector string, windowsDrive string) (MountResponse, error) {
	controller, err := s.ensureMountController()
	if err != nil {
		return MountResponse{}, err
	}

	existing := controller.Status()
	selector = strings.TrimSpace(selector)
	var drive Drive
	if mountStatusOwnsPinnedDrive(existing) && selector == "" {
		drive = s.driveFromMountStatus(existing)
		if windowsDrive == "" {
			windowsDrive = existing.WindowsDrive
		}
	} else if selector == "" {
		drive, err = s.activeDrive()
	} else {
		drive, err = s.resolveDrive(selector)
	}
	if err != nil {
		return MountResponse{}, err
	}

	status, err := controller.Start(ctx, mountcontroller.Drive{
		ID:    drive.ID,
		Title: drive.Title,
		Kind:  drive.Kind,
	}, mountcontroller.StartOptions{WindowsDrive: windowsDrive})
	return mountResponse(status, s.driveFromMountStatus(status)), err
}

func mountStatusOwnsPinnedDrive(status mountcontroller.Status) bool {
	return status.Running || status.Mounted ||
		status.Phase == mountcontroller.PhasePreparing ||
		status.Phase == mountcontroller.PhaseAttaching
}

func (s *Server) mountStatus() MountResponse {
	controller := s.currentMountController()
	if controller == nil {
		return MountResponse{Phase: string(mountcontroller.PhaseStopped)}
	}
	status := controller.Status()
	return mountResponse(status, s.driveFromMountStatus(status))
}

func (s *Server) stopMount(ctx context.Context) (MountResponse, error) {
	controller := s.currentMountController()
	if controller == nil {
		return MountResponse{Phase: string(mountcontroller.PhaseStopped)}, nil
	}
	status, err := controller.Stop(ctx)
	return mountResponse(status, s.driveFromMountStatus(status)), err
}

func (s *Server) stopMountServer(ctx context.Context) error {
	controller := s.currentMountController()
	if controller == nil {
		return nil
	}
	return controller.Close(ctx)
}

func (s *Server) ensureMountController() (*mountcontroller.Controller, error) {
	if s == nil {
		return nil, fmt.Errorf("mount: daemon is not ready")
	}
	s.mountMu.Lock()
	defer s.mountMu.Unlock()
	if s.mountController != nil {
		return s.mountController, nil
	}
	controller, err := mountcontroller.New(s.engine)
	if err != nil {
		return nil, err
	}
	s.mountController = controller
	return controller, nil
}

func (s *Server) currentMountController() *mountcontroller.Controller {
	if s == nil {
		return nil
	}
	s.mountMu.Lock()
	defer s.mountMu.Unlock()
	return s.mountController
}

func (s *Server) driveFromMountStatus(status mountcontroller.Status) Drive {
	if status.DriveID == 0 {
		return Drive{}
	}
	active := int64(0)
	if s != nil && s.engine != nil {
		active = s.engine.ActiveChannelID()
	}
	return Drive{
		ID:     status.DriveID,
		Title:  status.DriveTitle,
		Kind:   status.DriveKind,
		Active: status.DriveID == active,
	}
}

func mountResponse(status mountcontroller.Status, drive Drive) MountResponse {
	return MountResponse{
		Running:      status.Running,
		Mounted:      status.Mounted,
		Phase:        string(status.Phase),
		Mode:         status.Mode,
		Label:        status.Label,
		Location:     status.Location,
		Error:        status.Error,
		Drive:        drive,
		WindowsDrive: status.WindowsDrive,
	}
}
