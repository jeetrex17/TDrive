package daemon

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	coreauth "TDrive/backend/auth"
	"TDrive/backend/projection"
	channelservice "TDrive/backend/services/channel"
	personaldriveservice "TDrive/backend/services/personaldrive"
	"TDrive/backend/tgclient"
)

func (s *Server) authSetup(apiID int, apiHash string) (AuthStatusResponse, error) {
	// Never log apiID/apiHash: these are Telegram API credentials.
	if apiID <= 0 {
		return AuthStatusResponse{}, fmt.Errorf("api id required")
	}
	if strings.TrimSpace(apiHash) == "" {
		return AuthStatusResponse{}, fmt.Errorf("api hash required")
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	if err := coreauth.SaveImpCredentials(apiID, strings.TrimSpace(apiHash)); err != nil {
		slog.Warn("daemon: auth setup failed to save credentials", "error", err)
		return AuthStatusResponse{}, err
	}
	slog.Info("daemon: auth credentials saved")
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
	// phone is not logged: treat it as PII. Login codes/2FA passwords arrive
	// via separate submit calls and must never be logged either.
	phone = strings.TrimSpace(phone)
	if phone == "" {
		return AuthLoginResponse{}, fmt.Errorf("phone number required")
	}

	events := s.subscribeEvents()
	defer s.unsubscribeEvents(events)

	slog.Info("daemon: login started")
	if err := s.engine.AuthService().StartLogin(ctx, phone); err != nil {
		slog.Warn("daemon: login start failed", "error", err)
		return AuthLoginResponse{}, err
	}

	for {
		select {
		case event := <-events:
			switch event.Name {
			case "login-success":
				slog.Info("daemon: login succeeded, preparing personal drive")
				setup, err := s.preparePersonalDrive(ctx)
				if err != nil {
					slog.Warn("daemon: personal drive preparation failed", "error", err)
					return AuthLoginResponse{}, err
				}
				active := s.engine.ActiveChannelID()
				if active != 0 {
					if err := s.saveCurrentDrive(active); err != nil {
						return AuthLoginResponse{}, err
					}
				}
				return AuthLoginResponse{
					LoggedIn:        true,
					InitDriveResult: setup.Status,
					ActiveChannelID: active,
					PersonalDrive:   setup,
				}, nil
			case "login-error":
				slog.Warn("daemon: login failed")
				return AuthLoginResponse{}, fmt.Errorf("%s", firstEventArg(event))
			}
		case <-ctx.Done():
			return AuthLoginResponse{}, ctx.Err()
		}
	}
}

// preparePersonalDrive composes the local and remote setup steps for the
// CLI: activate a saved drive, or list the channels the user may recover.
func (s *Server) preparePersonalDrive(ctx context.Context) (PersonalDriveSetup, error) {
	service := s.engine.PersonalDriveService()
	state, err := service.Prepare(ctx)
	if err != nil {
		return PersonalDriveSetup{}, err
	}
	setup := PersonalDriveSetup{Status: state.Status, Candidates: []PersonalDriveCandidate{}}
	if state.ChannelID > 0 {
		setup.ActiveChannelID = strconv.FormatInt(state.ChannelID, 10)
	}
	if state.Status != personaldriveservice.StatusSelectionRequired {
		return setup, nil
	}
	candidates, err := service.Discover(ctx)
	if err != nil {
		return PersonalDriveSetup{}, err
	}
	setup.Candidates = personalDriveCandidatesFromService(candidates)
	return setup, nil
}

func (s *Server) selectPersonalDrive(ctx context.Context, rawChannelID string) (PersonalDriveSetup, error) {
	channelID, err := parsePersonalDriveID(rawChannelID)
	if err != nil {
		return PersonalDriveSetup{}, err
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if err := s.engine.PersonalDriveService().Select(ctx, channelID); err != nil {
		return PersonalDriveSetup{}, err
	}
	return s.commitPersonalDrive(s.engine.ActiveChannelID())
}

func (s *Server) createPersonalDrive(ctx context.Context) (PersonalDriveSetup, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if err := s.engine.PersonalDriveService().Create(ctx); err != nil {
		return PersonalDriveSetup{}, err
	}
	return s.commitPersonalDrive(s.engine.ActiveChannelID())
}

func (s *Server) commitPersonalDrive(channelID int64) (PersonalDriveSetup, error) {
	if channelID <= 0 {
		return PersonalDriveSetup{}, fmt.Errorf("personal drive did not become active")
	}
	if err := s.saveCurrentDrive(channelID); err != nil {
		return PersonalDriveSetup{}, err
	}
	return PersonalDriveSetup{
		Status:          "ready",
		ActiveChannelID: strconv.FormatInt(channelID, 10),
		Candidates:      []PersonalDriveCandidate{},
	}, nil
}

func personalDriveCandidatesFromService(candidates []personaldriveservice.Candidate) []PersonalDriveCandidate {
	result := make([]PersonalDriveCandidate, len(candidates))
	for i, candidate := range candidates {
		result[i] = PersonalDriveCandidate{
			ID:          strconv.FormatInt(candidate.ID, 10),
			Title:       candidate.Title,
			CreatedAt:   candidate.CreatedAt,
			HasActivity: candidate.HasActivity,
			Recommended: candidate.Recommended,
		}
	}
	return result
}

func parsePersonalDriveID(raw string) (int64, error) {
	value := strings.TrimSpace(raw)
	channelID, err := strconv.ParseInt(value, 10, 64)
	if err != nil || channelID <= 0 || strconv.FormatInt(channelID, 10) != value {
		return 0, fmt.Errorf("invalid personal drive channel id")
	}
	return channelID, nil
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

	slog.Info("daemon: logout started", "mode", m)
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
