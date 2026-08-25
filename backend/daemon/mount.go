package daemon

import (
	"context"
	"fmt"
	"strings"

	"TDrive/backend/mountcontroller"
	"TDrive/backend/mountpolicy"
)

type daemonMountController interface {
	Start(context.Context, mountcontroller.Drive, mountcontroller.StartOptions) (mountcontroller.Status, error)
	Status() mountcontroller.Status
	Stop(context.Context) (mountcontroller.Status, error)
	Close(context.Context) error
}

func (s *Server) startMount(ctx context.Context, selector string, windowsDrive string, mode string) (MountResponse, error) {
	release, err := s.acquireMountLifecycle(ctx)
	if err != nil {
		return MountResponse{}, fmt.Errorf("mount: wait for encryption transition: %w", err)
	}
	defer release()

	controller, err := s.ensureMountController()
	if err != nil {
		return MountResponse{}, err
	}

	existing := controller.Status()
	selector = strings.TrimSpace(selector)
	reusePinnedDrive := mountStatusOwnsPinnedDrive(existing) && selector == ""
	var drive Drive
	var encrypted bool
	var encryptionUnlocked bool
	if reusePinnedDrive {
		drive = s.driveFromMountStatus(existing)
		encrypted = existing.DriveEncrypted
		encryptionUnlocked = existing.DriveEncryptionUnlocked
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
		encrypted, encryptionUnlocked, err = s.mountDriveEncryptionStatus(ctx, drive)
		if err != nil {
			return MountResponse{}, err
		}
	}

	status, err := controller.Start(ctx, mountcontroller.Drive{
		ID:                 drive.ID,
		Title:              drive.Title,
		Kind:               drive.Kind,
		Encrypted:          encrypted,
		EncryptionUnlocked: encryptionUnlocked,
	}, mountcontroller.StartOptions{WindowsDrive: windowsDrive, Mode: mountcontroller.Mode(mode)})
	return mountResponse(status, s.driveFromMountStatus(status)), err
}

func (s *Server) mountDriveEncryptionStatus(ctx context.Context, drive Drive) (bool, bool, error) {
	if drive.Kind != mountcontroller.DriveKindPersonal {
		return false, false, nil
	}
	if s == nil || s.engine == nil {
		return false, false, fmt.Errorf("mount: encryption eligibility is unavailable")
	}
	reads := s.engine.ReadService()
	if reads == nil || reads.DB == nil {
		return false, false, fmt.Errorf("mount: encryption eligibility is unavailable")
	}
	policy, err := mountpolicy.ResolvePersonal(
		ctx,
		reads.DB,
		drive.ID,
		s.refreshMountEncryptionPolicy,
		func() (bool, error) {
			status, err := s.engine.EncryptionService().StatusContext(ctx)
			return status.PasswordRemembered, err
		},
	)
	if err != nil {
		return false, false, err
	}
	return policy.Encrypted, policy.Unlocked, nil
}

func (s *Server) refreshMountEncryptionPolicy(ctx context.Context, channelID int64) error {
	if s != nil && s.mountEncryptionPolicyRefresh != nil {
		return s.mountEncryptionPolicyRefresh(ctx, channelID)
	}
	if s == nil || s.engine == nil {
		return mountpolicy.ErrEncryptionPolicyUnavailable
	}
	if err := s.engine.EnsureEncryptionPolicy(ctx, channelID); err != nil {
		if s.warnf != nil {
			s.warnf("mount: refresh personal-drive encryption policy: %v\n", err)
		}
		return err
	}
	return nil
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
	release, err := s.acquireMountLifecycle(ctx)
	if err != nil {
		return MountResponse{}, fmt.Errorf("mount: wait for encryption transition: %w", err)
	}
	defer release()
	return s.stopMountLocked(ctx)
}

func (s *Server) stopMountLocked(ctx context.Context) (MountResponse, error) {
	controller := s.currentMountController()
	if controller == nil {
		return MountResponse{Phase: string(mountcontroller.PhaseStopped)}, nil
	}
	status, err := controller.Stop(ctx)
	return mountResponse(status, s.driveFromMountStatus(status)), err
}

func (s *Server) stopMountServer(ctx context.Context) error {
	if s == nil {
		return nil
	}
	if err := s.mountLifecycle.lock(ctx); err != nil {
		return err
	}
	defer s.mountLifecycle.unlock()
	if s.mountLifecycleTerminal {
		return nil
	}
	s.mountLifecycleTerminal = true
	return s.stopMountServerLocked(ctx)
}

func (s *Server) stopMountServerLocked(ctx context.Context) error {
	controller := s.currentMountController()
	if controller == nil {
		return nil
	}
	return controller.Close(ctx)
}

func (s *Server) ensureMountController() (daemonMountController, error) {
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

func (s *Server) currentMountController() daemonMountController {
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
