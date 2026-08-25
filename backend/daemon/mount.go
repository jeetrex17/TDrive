package daemon

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"TDrive/backend/mountcontroller"
	"TDrive/backend/projection"
)

func (s *Server) startMount(ctx context.Context, selector string, windowsDrive string, mode string) (MountResponse, error) {
	controller, err := s.ensureMountController()
	if err != nil {
		return MountResponse{}, err
	}

	existing := controller.Status()
	selector = strings.TrimSpace(selector)
	reusePinnedDrive := mountStatusOwnsPinnedDrive(existing) && selector == ""
	var drive Drive
	var encrypted bool
	if reusePinnedDrive {
		drive = s.driveFromMountStatus(existing)
		encrypted = existing.DriveEncrypted
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
	if !reusePinnedDrive {
		encrypted, err = s.mountDriveEncryptionEnabled(drive)
		if err != nil {
			return MountResponse{}, err
		}
	}

	status, err := controller.Start(ctx, mountcontroller.Drive{
		ID:        drive.ID,
		Title:     drive.Title,
		Kind:      drive.Kind,
		Encrypted: encrypted,
	}, mountcontroller.StartOptions{WindowsDrive: windowsDrive, Mode: mountcontroller.Mode(mode)})
	return mountResponse(status, s.driveFromMountStatus(status)), err
}

func (s *Server) mountDriveEncryptionEnabled(drive Drive) (bool, error) {
	if drive.Kind != mountcontroller.DriveKindPersonal {
		return false, nil
	}
	if s == nil || s.engine == nil {
		return false, fmt.Errorf("mount: encryption eligibility is unavailable")
	}
	reads := s.engine.ReadService()
	if reads == nil || reads.DB == nil {
		return false, fmt.Errorf("mount: encryption eligibility is unavailable")
	}
	config, err := projection.GetEncryptionConfig(reads.DB, drive.ID)
	if errors.Is(err, projection.ErrEncryptionConfigNotFound) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("mount: check encryption eligibility: %w", err)
	}
	return config.Enabled, nil
}

func mountStatusOwnsPinnedDrive(status mountcontroller.Status) bool {
	return status.Running || status.Mounted ||
		status.Phase == mountcontroller.PhasePreparing ||
		status.Phase == mountcontroller.PhaseAttaching ||
		status.Phase == mountcontroller.PhaseDraining ||
		status.Phase == mountcontroller.PhaseDetaching
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
		Running:         status.Running,
		Mounted:         status.Mounted,
		Phase:           string(status.Phase),
		Mode:            string(status.Mode),
		WriteState:      string(status.WriteState),
		AcceptingWrites: status.AcceptingWrites,
		ActiveWrites:    status.ActiveWrites,
		Label:           status.Label,
		Location:        status.Location,
		Error:           status.Error,
		Drive:           drive,
		WindowsDrive:    status.WindowsDrive,
	}
}
