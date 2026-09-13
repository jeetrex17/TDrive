package daemon

import (
	"context"
	"fmt"
	"strconv"
	"strings"
)

func (s *Server) vaultStatus(ctx context.Context) (VaultResponse, error) {
	status, err := s.engine.EncryptionService().StatusContext(ctx)
	if err != nil {
		return VaultResponse{}, err
	}
	return VaultResponse{Status: VaultStatus{
		Available:  status.Available,
		Configured: status.PasswordSet,
		Unlocked:   status.PasswordRemembered,
		Hint:       status.Hint,
	}}, nil
}

func (s *Server) vaultUnlock(ctx context.Context, password string) (VaultResponse, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	release, err := s.acquireMountLifecycle(ctx)
	if err != nil {
		return VaultResponse{}, fmt.Errorf("vault unlock: wait for mount lifecycle: %w", err)
	}
	defer release()

	if err := s.engine.EncryptionService().UsePassword(password); err != nil {
		return VaultResponse{}, err
	}
	return s.vaultStatus(ctx)
}

func (s *Server) vaultLock(ctx context.Context) (VaultResponse, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	release, err := s.acquireMountLifecycle(ctx)
	if err != nil {
		return VaultResponse{}, fmt.Errorf("vault lock: wait for mount lifecycle: %w", err)
	}
	defer release()

	if _, err := s.stopMountLocked(ctx); err != nil {
		return VaultResponse{}, fmt.Errorf("vault lock: eject mounted drive: %w", err)
	}
	s.engine.ClearEncryptionSession()
	return s.vaultStatus(ctx)
}

func (s *Server) loadState() error {
	st, err := loadState()
	if err != nil {
		st = newState()
	}

	s.mu.Lock()
	s.state = st
	s.mu.Unlock()

	if st.CurrentDriveID != 0 {
		if err := s.engine.SetActiveChannel(st.CurrentDriveID); err != nil {
			s.warnf("daemon: saved drive %d is not available: %v\n", st.CurrentDriveID, err)
			st.CurrentDriveID = s.engine.ActiveChannelID()
		}
	} else if active := s.engine.ActiveChannelID(); active != 0 {
		st.CurrentDriveID = active
	}
	if st.CurrentDriveID != 0 {
		st.setCWD(st.CurrentDriveID, st.cwd(st.CurrentDriveID))
		if err := st.save(); err != nil {
			s.warnf("daemon: save cli state: %v\n", err)
		}
	}
	return err
}

func (s *Server) listDrives() (DriveListResponse, error) {
	channels, err := s.engine.ChannelService().ListChannels()
	if err != nil {
		return DriveListResponse{}, err
	}
	active := s.engine.ActiveChannelID()
	out := DriveListResponse{Drives: make([]Drive, 0, len(channels))}
	for _, ch := range channels {
		out.Drives = append(out.Drives, driveFromChannel(ch, active))
	}
	return out, nil
}

func (s *Server) useDrive(selector string) (DriveUseResponse, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	drive, err := s.resolveDrive(selector)
	if err != nil {
		return DriveUseResponse{}, err
	}
	if err := s.engine.SetActiveChannel(drive.ID); err != nil {
		return DriveUseResponse{}, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state == nil {
		s.state = newState()
	}
	s.state.CurrentDriveID = drive.ID
	s.state.setCWD(drive.ID, s.state.cwd(drive.ID))
	if err := s.state.save(); err != nil {
		return DriveUseResponse{}, err
	}
	drive.Active = true
	return DriveUseResponse{Drive: drive, CurrentPath: s.state.cwd(drive.ID)}, nil
}

func (s *Server) resolveDrive(selector string) (Drive, error) {
	selector = strings.TrimSpace(selector)
	if selector == "" {
		return Drive{}, fmt.Errorf("drive selector required")
	}
	list, err := s.listDrives()
	if err != nil {
		return Drive{}, err
	}
	if id, err := strconv.ParseInt(selector, 10, 64); err == nil {
		for _, drive := range list.Drives {
			if drive.ID == id {
				return drive, nil
			}
		}
		return Drive{}, fmt.Errorf("drive %d not found", id)
	}

	var matches []Drive
	for _, drive := range list.Drives {
		if drive.Title == selector {
			matches = append(matches, drive)
		}
	}
	if len(matches) == 0 {
		needle := strings.ToLower(selector)
		for _, drive := range list.Drives {
			if strings.ToLower(drive.Title) == needle {
				matches = append(matches, drive)
			}
		}
	}
	switch len(matches) {
	case 0:
		return Drive{}, fmt.Errorf("drive %q not found", selector)
	case 1:
		return matches[0], nil
	default:
		return Drive{}, fmt.Errorf("drive name %q is ambiguous; use the numeric id", selector)
	}
}

func (s *Server) activeDrive() (Drive, error) {
	active := s.engine.ActiveChannelID()
	if active == 0 {
		return Drive{}, fmt.Errorf("no active drive")
	}
	list, err := s.listDrives()
	if err != nil {
		return Drive{}, err
	}
	for _, drive := range list.Drives {
		if drive.ID == active {
			return drive, nil
		}
	}
	return Drive{}, fmt.Errorf("active drive %d is not available", active)
}

func (s *Server) currentPath() string {
	active := s.engine.ActiveChannelID()
	if active == 0 {
		return ""
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state == nil {
		return "/"
	}
	return s.state.cwd(active)
}
