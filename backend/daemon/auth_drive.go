package daemon

import (
	"context"
	"fmt"
	"strings"
	"time"

	coreauth "TDrive/backend/auth"
	"TDrive/backend/projection"
	channelservice "TDrive/backend/services/channel"
	"TDrive/backend/tgclient"
)

func (s *Server) authSetup(apiID int, apiHash string) (AuthStatusResponse, error) {
	if apiID <= 0 {
		return AuthStatusResponse{}, fmt.Errorf("api id required")
	}
	if strings.TrimSpace(apiHash) == "" {
		return AuthStatusResponse{}, fmt.Errorf("api hash required")
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	if err := coreauth.SaveImpCredentials(apiID, strings.TrimSpace(apiHash)); err != nil {
		return AuthStatusResponse{}, err
	}
	return s.authStatus(context.Background())
}

func (s *Server) authStatus(ctx context.Context) (AuthStatusResponse, error) {
	status := AuthStatus{SystemStatus: s.engine.AuthService().SystemStatus()}
	if status.SystemStatus != "NEEDS_SETUP" {
		status.LoggedIn = s.engine.AuthService().IsLoggedIn(ctx)
	}
	return AuthStatusResponse{Status: status}, nil
}

func (s *Server) authLogin(ctx context.Context, phone string) (AuthLoginResponse, error) {
	phone = strings.TrimSpace(phone)
	if phone == "" {
		return AuthLoginResponse{}, fmt.Errorf("phone number required")
	}

	events := s.subscribeEvents()
	defer s.unsubscribeEvents(events)

	if err := s.engine.AuthService().StartLogin(ctx, phone); err != nil {
		return AuthLoginResponse{}, err
	}

	for {
		select {
		case event := <-events:
			switch event.Name {
			case "login-success":
				result := s.engine.LifecycleService().InitDrive(ctx)
				if strings.HasPrefix(result, "Error:") {
					return AuthLoginResponse{}, fmt.Errorf("%s", result)
				}
				active := s.engine.ActiveChannelID()
				if active != 0 {
					if err := s.saveCurrentDrive(active); err != nil {
						return AuthLoginResponse{}, err
					}
				}
				return AuthLoginResponse{LoggedIn: true, InitDriveResult: result, ActiveChannelID: active}, nil
			case "login-error":
				return AuthLoginResponse{}, fmt.Errorf("%s", firstEventArg(event))
			}
		case <-ctx.Done():
			return AuthLoginResponse{}, ctx.Err()
		}
	}
}

func (s *Server) authLogout(ctx context.Context, mode string) (AuthLogoutResponse, error) {
	m := coreauth.LogoutMode(strings.TrimSpace(mode))
	if m == "" {
		m = coreauth.LogoutFull
	}
	if m != coreauth.LogoutSoft && m != coreauth.LogoutFull {
		return AuthLogoutResponse{}, fmt.Errorf("logout: unknown mode %q", mode)
	}

	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	release, err := s.acquireMountLifecycle(ctx)
	if err != nil {
		return AuthLogoutResponse{}, fmt.Errorf("logout: wait for mount lifecycle: %w", err)
	}
	defer release()

	if _, err := s.stopMountLocked(ctx); err != nil {
		return AuthLogoutResponse{}, fmt.Errorf("logout: eject mounted drive: %w", err)
	}
	s.engine.ClearEncryptionSession()
	// Logout is terminal for this daemon instance. Queued mount starts acquire
	// the gate only after this bit is set and therefore fail before resolution or
	// controller access, including for plaintext drives.
	s.mountLifecycleTerminal = true
	s.engine.ClearUserCache()
	if m == coreauth.LogoutFull {
		s.revokeTelegramSession(ctx)
	}
	if s.engine != nil {
		s.engine.Close()
	}
	if err := coreauth.ClearUserData(m); err != nil {
		return AuthLogoutResponse{}, err
	}
	return AuthLogoutResponse{Mode: string(m), Stopping: true}, nil
}

func (s *Server) revokeTelegramSession(ctx context.Context) {
	client, err := coreauth.Connect()
	if err != nil {
		return
	}
	runCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	if err := client.Run(runCtx, func(ctx context.Context) error {
		_, err := client.API().AuthLogOut(ctx)
		return err
	}); err != nil {
		s.warnf("logout: revoke telegram session: %v\n", err)
	}
}

func (s *Server) whoami(ctx context.Context) (SelfUserResponse, error) {
	me, err := s.engine.UserService().Me(ctx)
	if err != nil {
		return SelfUserResponse{}, err
	}
	return SelfUserResponse{User: SelfUser{
		UserID:      me.UserID,
		DisplayName: me.DisplayName,
		Username:    me.Username,
	}}, nil
}

func (s *Server) createDrive(ctx context.Context, title string, requireApproval bool) (DriveUseResponse, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	row, err := s.engine.ChannelService().CreateSharedDrive(ctx, strings.TrimSpace(title), requireApproval)
	if err != nil {
		return DriveUseResponse{}, err
	}
	if err := s.saveCurrentDrive(row.ChannelID); err != nil {
		return DriveUseResponse{}, err
	}
	drive := driveFromChannel(row, row.ChannelID)
	return DriveUseResponse{Drive: drive, CurrentPath: s.currentPath()}, nil
}

func (s *Server) joinDrive(ctx context.Context, inviteLink string) (DriveJoinResponse, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	result, err := s.engine.ChannelService().JoinSharedDrive(ctx, strings.TrimSpace(inviteLink))
	if err != nil {
		return DriveJoinResponse{}, err
	}
	out := driveJoinResponse(result)
	if result.Channel != nil {
		if err := s.saveCurrentDrive(result.Channel.ChannelID); err != nil {
			return DriveJoinResponse{}, err
		}
	}
	return out, nil
}

func (s *Server) listPendingJoins() (PendingJoinsResponse, error) {
	rows, err := s.engine.ChannelService().ListPendingJoins()
	if err != nil {
		return PendingJoinsResponse{}, err
	}
	out := PendingJoinsResponse{Pending: make([]PendingJoin, 0, len(rows))}
	for _, row := range rows {
		out.Pending = append(out.Pending, pendingJoin(row))
	}
	return out, nil
}

func (s *Server) checkPendingJoin(ctx context.Context, inviteHash string) (DriveJoinResponse, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	result, err := s.engine.ChannelService().CheckPendingJoin(ctx, strings.TrimSpace(inviteHash))
	if err != nil {
		return DriveJoinResponse{}, err
	}
	out := driveJoinResponse(result)
	if result.Channel != nil {
		if err := s.saveCurrentDrive(result.Channel.ChannelID); err != nil {
			return DriveJoinResponse{}, err
		}
	}
	return out, nil
}

func (s *Server) removePendingJoin(inviteHash string) error {
	return s.engine.ChannelService().RemovePendingJoin(strings.TrimSpace(inviteHash))
}

func (s *Server) inviteLink(ctx context.Context, selector string, requireApproval bool) (InviteLinkResponse, error) {
	drive, err := s.driveBySelectorOrActive(selector)
	if err != nil {
		return InviteLinkResponse{}, err
	}
	link, err := s.engine.ChannelService().ExportInviteLink(ctx, drive.ID, requireApproval)
	if err != nil {
		return InviteLinkResponse{}, err
	}
	drive.InviteLink = link
	return InviteLinkResponse{Drive: drive, Link: link, RequireApproval: requireApproval}, nil
}

func (s *Server) joinRequests(ctx context.Context, selector string) (JoinRequestsResponse, error) {
	drive, err := s.driveBySelectorOrActive(selector)
	if err != nil {
		return JoinRequestsResponse{}, err
	}
	rows, err := s.engine.ChannelService().ListJoinRequests(ctx, drive.ID)
	if err != nil {
		return JoinRequestsResponse{}, err
	}
	return JoinRequestsResponse{Drive: drive, Requests: joinRequests(rows)}, nil
}

func (s *Server) resolveJoinRequest(ctx context.Context, selector string, userID int64, approve bool) (JoinRequestsResponse, error) {
	if userID == 0 {
		return JoinRequestsResponse{}, fmt.Errorf("user id required")
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	drive, err := s.driveBySelectorOrActive(selector)
	if err != nil {
		return JoinRequestsResponse{}, err
	}
	if err := s.engine.ChannelService().HideJoinRequest(ctx, drive.ID, userID, approve); err != nil {
		return JoinRequestsResponse{}, err
	}
	rows, err := s.engine.ChannelService().ListJoinRequests(ctx, drive.ID)
	if err != nil {
		return JoinRequestsResponse{}, err
	}
	return JoinRequestsResponse{Drive: drive, Requests: joinRequests(rows)}, nil
}

func (s *Server) leaveDrive(ctx context.Context, selector string) (DriveUseResponse, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	drive, err := s.driveBySelectorOrActive(selector)
	if err != nil {
		return DriveUseResponse{}, err
	}
	if err := s.engine.ChannelService().LeaveSharedDrive(ctx, drive.ID); err != nil {
		return DriveUseResponse{}, err
	}
	active, err := s.activeDrive()
	if err != nil {
		return DriveUseResponse{}, err
	}
	if err := s.saveCurrentDrive(active.ID); err != nil {
		return DriveUseResponse{}, err
	}
	return DriveUseResponse{Drive: active, CurrentPath: s.currentPath()}, nil
}

func (s *Server) syncDrive(ctx context.Context, selector string) (MaintenanceResponse, error) {
	drive, err := s.driveBySelectorOrActive(selector)
	if err != nil {
		return MaintenanceResponse{}, err
	}
	if err := s.engine.LifecycleService().SyncChannel(ctx, drive.ID); err != nil {
		return MaintenanceResponse{}, err
	}
	return MaintenanceResponse{Drive: drive}, nil
}

func (s *Server) rebuildDrive(selector string) (MaintenanceResponse, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	drive, err := s.driveBySelectorOrActive(selector)
	if err != nil {
		return MaintenanceResponse{}, err
	}
	if err := s.engine.LifecycleService().RebuildProjection(drive.ID); err != nil {
		return MaintenanceResponse{}, err
	}
	return MaintenanceResponse{Drive: drive}, nil
}

func (s *Server) driveBySelectorOrActive(selector string) (Drive, error) {
	if strings.TrimSpace(selector) == "" {
		return s.activeDrive()
	}
	return s.resolveDrive(selector)
}

func (s *Server) saveCurrentDrive(channelID int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state == nil {
		s.state = newState()
	}
	s.state.CurrentDriveID = channelID
	s.state.setCWD(channelID, s.state.cwd(channelID))
	return s.state.save()
}

func driveFromChannel(ch projection.Channel, active int64) Drive {
	return Drive{
		ID:         ch.ChannelID,
		Title:      ch.Title,
		Kind:       ch.Kind,
		Active:     ch.ChannelID == active,
		InviteLink: ch.InviteLink,
	}
}

func driveJoinResponse(result channelservice.JoinResult) DriveJoinResponse {
	out := DriveJoinResponse{Status: result.Status}
	if result.Channel != nil {
		drive := driveFromChannel(*result.Channel, result.Channel.ChannelID)
		out.Drive = &drive
	}
	if result.Pending != nil {
		p := pendingJoin(*result.Pending)
		out.Pending = &p
	}
	return out
}

func pendingJoin(row projection.PendingJoin) PendingJoin {
	return PendingJoin{
		InviteHash:    row.InviteHash,
		InviteLink:    row.InviteLink,
		Title:         row.Title,
		RequestedAt:   row.RequestedAt,
		LastCheckedAt: row.LastCheckedAt,
		Status:        row.Status,
		LastError:     row.LastError,
	}
}

func joinRequests(rows []tgclient.JoinRequest) []JoinRequest {
	out := make([]JoinRequest, 0, len(rows))
	for _, row := range rows {
		out = append(out, JoinRequest{
			UserID:      row.UserID,
			DisplayName: row.DisplayName,
			Username:    row.Username,
			RequestedAt: row.RequestedAt,
			About:       row.About,
		})
	}
	return out
}

func firstEventArg(event Event) string {
	if len(event.Args) == 0 {
		return ""
	}
	return fmt.Sprint(event.Args[0])
}
