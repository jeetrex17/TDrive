package daemon

import (
	"context"
	"fmt"
	"log/slog"
	"time"
)

const daemonDrainTimeout = 10 * time.Second

func (s *Server) handleStreamingRequest(ctx context.Context, req Request, writeFrame func(Frame) error) error {
	s.streamMu.Lock()
	defer s.streamMu.Unlock()

	if err := validateRequest(req); err != nil {
		return writeFrame(ErrorResponse(req.ID, err))
	}
	slog.Debug("daemon: streaming command started", "command", req.Command)

	events := s.subscribeEvents()
	defer s.unsubscribeEvents(events)

	done := make(chan Frame, 1)
	go func() {
		done <- s.handleRequest(ctx, req)
	}()

	for {
		select {
		case event := <-events:
			frame, err := EventFrame(req.ID, event)
			if err != nil {
				frame = ErrorResponse(req.ID, err)
			}
			if err := writeFrame(frame); err != nil {
				return err
			}
		case frame := <-done:
			return writeFrame(frame)
		case <-ctx.Done():
			select {
			case frame := <-done:
				return writeFrame(frame)
			case <-time.After(daemonDrainTimeout):
				return ctx.Err()
			}
		}
	}
}

func isStreamingCommand(command string) bool {
	switch command {
	case CommandAuthLogin, CommandUpload, CommandDownload:
		return true
	default:
		return false
	}
}

func (s *Server) handleRequest(ctx context.Context, req Request) Frame {
	if err := validateRequest(req); err != nil {
		return ErrorResponse(req.ID, err)
	}
	// req.Payload is never logged: several commands (auth.setup, auth.submit_password,
	// vault.unlock) carry credentials in it. Only the command name is safe to log.
	slog.Debug("daemon: dispatching command", "command", req.Command)

	switch req.Command {
	case CommandStatus:
		out, err := s.status(ctx)
		if err != nil {
			return ErrorResponse(req.ID, err)
		}
		frame, err := Response(req.ID, out)
		if err != nil {
			return ErrorResponse(req.ID, err)
		}
		return frame
	case CommandShutdown:
		frame, err := Response(req.ID, map[string]string{"status": "stopping"})
		if err != nil {
			return ErrorResponse(req.ID, err)
		}
		slog.Info("daemon: shutdown requested")
		go s.stopOnce.Do(s.stop)
		return frame
	case CommandAuthSetup:
		var in AuthSetupRequest
		if err := decodePayload(req.Payload, &in); err != nil {
			return ErrorResponse(req.ID, err)
		}
		out, err := s.authSetup(in.APIID, in.APIHash)
		if err != nil {
			return ErrorResponse(req.ID, err)
		}
		frame, err := Response(req.ID, out)
		if err != nil {
			return ErrorResponse(req.ID, err)
		}
		return frame
	case CommandAuthStatus:
		out, err := s.authStatus(ctx)
		if err != nil {
			return ErrorResponse(req.ID, err)
		}
		frame, err := Response(req.ID, out)
		if err != nil {
			return ErrorResponse(req.ID, err)
		}
		return frame
	case CommandAuthLogin:
		var in AuthLoginRequest
		if err := decodePayload(req.Payload, &in); err != nil {
			return ErrorResponse(req.ID, err)
		}
		out, err := s.authLogin(ctx, in.Phone)
		if err != nil {
			return ErrorResponse(req.ID, err)
		}
		frame, err := Response(req.ID, out)
		if err != nil {
			return ErrorResponse(req.ID, err)
		}
		return frame
	case CommandPersonalDrivePrepare:
		out, err := s.preparePersonalDrive(ctx)
		if err != nil {
			return ErrorResponse(req.ID, err)
		}
		frame, err := Response(req.ID, out)
		if err != nil {
			return ErrorResponse(req.ID, err)
		}
		return frame
	case CommandPersonalDriveSelect:
		var in PersonalDriveSelectRequest
		if err := decodePayload(req.Payload, &in); err != nil {
			return ErrorResponse(req.ID, err)
		}
		out, err := s.selectPersonalDrive(ctx, in.ChannelID)
		if err != nil {
			return ErrorResponse(req.ID, err)
		}
		frame, err := Response(req.ID, out)
		if err != nil {
			return ErrorResponse(req.ID, err)
		}
		return frame
	case CommandPersonalDriveCreate:
		out, err := s.createPersonalDrive(ctx)
		if err != nil {
			return ErrorResponse(req.ID, err)
		}
		frame, err := Response(req.ID, out)
		if err != nil {
			return ErrorResponse(req.ID, err)
		}
		return frame
	case CommandAuthSubmitCode:
		var in AuthSubmitRequest
		if err := decodePayload(req.Payload, &in); err != nil {
			return ErrorResponse(req.ID, err)
		}
		s.engine.AuthService().SubmitCode(in.Value)
		frame, err := Response(req.ID, map[string]string{"status": "ok"})
		if err != nil {
			return ErrorResponse(req.ID, err)
		}
		return frame
	case CommandAuthSubmitPassword:
		var in AuthSubmitRequest
		if err := decodePayload(req.Payload, &in); err != nil {
			return ErrorResponse(req.ID, err)
		}
		s.engine.AuthService().SubmitPassword(in.Value)
		frame, err := Response(req.ID, map[string]string{"status": "ok"})
		if err != nil {
			return ErrorResponse(req.ID, err)
		}
		return frame
	case CommandAuthLogout:
		var in AuthLogoutRequest
		if err := decodePayload(req.Payload, &in); err != nil {
			return ErrorResponse(req.ID, err)
		}
		out, err := s.authLogout(ctx, in.Mode)
		if err != nil {
			return ErrorResponse(req.ID, err)
		}
		frame, err := Response(req.ID, out)
		if err != nil {
			return ErrorResponse(req.ID, err)
		}
		go s.stopOnce.Do(s.stop)
		return frame
	case CommandWhoami:
		out, err := s.whoami(ctx)
		if err != nil {
			return ErrorResponse(req.ID, err)
		}
		frame, err := Response(req.ID, out)
		if err != nil {
			return ErrorResponse(req.ID, err)
		}
		return frame
	case CommandDriveList:
		out, err := s.listDrives()
		if err != nil {
			return ErrorResponse(req.ID, err)
		}
		frame, err := Response(req.ID, out)
		if err != nil {
			return ErrorResponse(req.ID, err)
		}
		return frame
	case CommandDriveUse:
		var in DriveUseRequest
		if err := decodePayload(req.Payload, &in); err != nil {
			return ErrorResponse(req.ID, err)
		}
		out, err := s.useDrive(in.Selector)
		if err != nil {
			return ErrorResponse(req.ID, err)
		}
		frame, err := Response(req.ID, out)
		if err != nil {
			return ErrorResponse(req.ID, err)
		}
		return frame
	case CommandDriveCreate:
		var in DriveCreateRequest
		if err := decodePayload(req.Payload, &in); err != nil {
			return ErrorResponse(req.ID, err)
		}
		out, err := s.createDrive(ctx, in.Title, in.RequireApproval)
		if err != nil {
			return ErrorResponse(req.ID, err)
		}
		frame, err := Response(req.ID, out)
		if err != nil {
			return ErrorResponse(req.ID, err)
		}
		return frame
	case CommandDriveJoin:
		var in DriveJoinRequest
		if err := decodePayload(req.Payload, &in); err != nil {
			return ErrorResponse(req.ID, err)
		}
		out, err := s.joinDrive(ctx, in.InviteLink)
		if err != nil {
			return ErrorResponse(req.ID, err)
		}
		frame, err := Response(req.ID, out)
		if err != nil {
			return ErrorResponse(req.ID, err)
		}
		return frame
	case CommandDrivePendingList:
		out, err := s.listPendingJoins()
		if err != nil {
			return ErrorResponse(req.ID, err)
		}
		frame, err := Response(req.ID, out)
		if err != nil {
			return ErrorResponse(req.ID, err)
		}
		return frame
	case CommandDrivePendingCheck:
		var in PendingJoinRequest
		if err := decodePayload(req.Payload, &in); err != nil {
			return ErrorResponse(req.ID, err)
		}
		out, err := s.checkPendingJoin(ctx, in.InviteHash)
		if err != nil {
			return ErrorResponse(req.ID, err)
		}
		frame, err := Response(req.ID, out)
		if err != nil {
			return ErrorResponse(req.ID, err)
		}
		return frame
	case CommandDrivePendingRemove:
		var in PendingJoinRequest
		if err := decodePayload(req.Payload, &in); err != nil {
			return ErrorResponse(req.ID, err)
		}
		if err := s.removePendingJoin(in.InviteHash); err != nil {
			return ErrorResponse(req.ID, err)
		}
		frame, err := Response(req.ID, map[string]string{"status": "ok"})
		if err != nil {
			return ErrorResponse(req.ID, err)
		}
		return frame
	case CommandDriveInviteLink:
		var in InviteLinkRequest
		if err := decodePayload(req.Payload, &in); err != nil {
			return ErrorResponse(req.ID, err)
		}
		out, err := s.inviteLink(ctx, in.Selector, in.RequireApproval)
		if err != nil {
			return ErrorResponse(req.ID, err)
		}
		frame, err := Response(req.ID, out)
		if err != nil {
			return ErrorResponse(req.ID, err)
		}
		return frame
	case CommandDriveJoinRequests:
		var in DriveSelectorRequest
		if err := decodePayload(req.Payload, &in); err != nil {
			return ErrorResponse(req.ID, err)
		}
		out, err := s.joinRequests(ctx, in.Selector)
		if err != nil {
			return ErrorResponse(req.ID, err)
		}
		frame, err := Response(req.ID, out)
		if err != nil {
			return ErrorResponse(req.ID, err)
		}
		return frame
	case CommandDriveJoinAction:
		var in JoinRequestActionRequest
		if err := decodePayload(req.Payload, &in); err != nil {
			return ErrorResponse(req.ID, err)
		}
		out, err := s.resolveJoinRequest(ctx, in.Selector, in.UserID, in.Approve)
		if err != nil {
			return ErrorResponse(req.ID, err)
		}
		frame, err := Response(req.ID, out)
		if err != nil {
			return ErrorResponse(req.ID, err)
		}
		return frame
	case CommandDriveLeave:
		var in DriveSelectorRequest
		if err := decodePayload(req.Payload, &in); err != nil {
			return ErrorResponse(req.ID, err)
		}
		out, err := s.leaveDrive(ctx, in.Selector)
		if err != nil {
			return ErrorResponse(req.ID, err)
		}
		frame, err := Response(req.ID, out)
		if err != nil {
			return ErrorResponse(req.ID, err)
		}
		return frame
	case CommandSync:
		var in DriveSelectorRequest
		if err := decodePayload(req.Payload, &in); err != nil {
			return ErrorResponse(req.ID, err)
		}
		out, err := s.syncDrive(ctx, in.Selector)
		if err != nil {
			return ErrorResponse(req.ID, err)
		}
		frame, err := Response(req.ID, out)
		if err != nil {
			return ErrorResponse(req.ID, err)
		}
		return frame
	case CommandRebuild:
		var in DriveSelectorRequest
		if err := decodePayload(req.Payload, &in); err != nil {
			return ErrorResponse(req.ID, err)
		}
		out, err := s.rebuildDrive(in.Selector)
		if err != nil {
			return ErrorResponse(req.ID, err)
		}
		frame, err := Response(req.ID, out)
		if err != nil {
			return ErrorResponse(req.ID, err)
		}
		return frame
	case CommandPWD:
		out, err := s.pwd()
		if err != nil {
			return ErrorResponse(req.ID, err)
		}
		frame, err := Response(req.ID, out)
		if err != nil {
			return ErrorResponse(req.ID, err)
		}
		return frame
	case CommandCD:
		var in PathRequest
		if err := decodePayload(req.Payload, &in); err != nil {
			return ErrorResponse(req.ID, err)
		}
		out, err := s.cd(in.Path)
		if err != nil {
			return ErrorResponse(req.ID, err)
		}
		frame, err := Response(req.ID, out)
		if err != nil {
			return ErrorResponse(req.ID, err)
		}
		return frame
	case CommandList:
		var in PathRequest
		if err := decodePayload(req.Payload, &in); err != nil {
			return ErrorResponse(req.ID, err)
		}
		out, err := s.listPath(in.Path)
		if err != nil {
			return ErrorResponse(req.ID, err)
		}
		frame, err := Response(req.ID, out)
		if err != nil {
			return ErrorResponse(req.ID, err)
		}
		return frame
	case CommandFind:
		var in FindRequest
		if err := decodePayload(req.Payload, &in); err != nil {
			return ErrorResponse(req.ID, err)
		}
		out, err := s.find(in.Query, in.Limit)
		if err != nil {
			return ErrorResponse(req.ID, err)
		}
		frame, err := Response(req.ID, out)
		if err != nil {
			return ErrorResponse(req.ID, err)
		}
		return frame
	case CommandMkdir:
		var in MkdirRequest
		if err := decodePayload(req.Payload, &in); err != nil {
			return ErrorResponse(req.ID, err)
		}
		out, err := s.mkdir(in.Path, in.Parents)
		if err != nil {
			return ErrorResponse(req.ID, err)
		}
		frame, err := Response(req.ID, out)
		if err != nil {
			return ErrorResponse(req.ID, err)
		}
		return frame
	case CommandRemove:
		var in RemoveRequest
		if err := decodePayload(req.Payload, &in); err != nil {
			return ErrorResponse(req.ID, err)
		}
		out, err := s.remove(ctx, in.Path, in.Recursive)
		if err != nil {
			return ErrorResponse(req.ID, err)
		}
		frame, err := Response(req.ID, out)
		if err != nil {
			return ErrorResponse(req.ID, err)
		}
		return frame
	case CommandMove:
		var in MoveRequest
		if err := decodePayload(req.Payload, &in); err != nil {
			return ErrorResponse(req.ID, err)
		}
		out, err := s.move(ctx, in.Source, in.Destination)
		if err != nil {
			return ErrorResponse(req.ID, err)
		}
		frame, err := Response(req.ID, out)
		if err != nil {
			return ErrorResponse(req.ID, err)
		}
		return frame
	case CommandVaultStatus:
		out, err := s.vaultStatus(ctx)
		if err != nil {
			return ErrorResponse(req.ID, err)
		}
		frame, err := Response(req.ID, out)
		if err != nil {
			return ErrorResponse(req.ID, err)
		}
		return frame
	case CommandVaultUnlock:
		var in VaultUnlockRequest
		if err := decodePayload(req.Payload, &in); err != nil {
			return ErrorResponse(req.ID, err)
		}
		out, err := s.vaultUnlock(ctx, in.Password)
		if err != nil {
			return ErrorResponse(req.ID, err)
		}
		frame, err := Response(req.ID, out)
		if err != nil {
			return ErrorResponse(req.ID, err)
		}
		return frame
	case CommandVaultLock:
		out, err := s.vaultLock(ctx)
		if err != nil {
			return ErrorResponse(req.ID, err)
		}
		frame, err := Response(req.ID, out)
		if err != nil {
			return ErrorResponse(req.ID, err)
		}
		return frame
	case CommandUpload:
		var in UploadRequest
		if err := decodePayload(req.Payload, &in); err != nil {
			return ErrorResponse(req.ID, err)
		}
		out, err := s.upload(ctx, in.LocalPath, in.RemotePath, in.Encrypt, in.Extract)
		if err != nil {
			return ErrorResponse(req.ID, err)
		}
		frame, err := Response(req.ID, out)
		if err != nil {
			return ErrorResponse(req.ID, err)
		}
		return frame
	case CommandDownload:
		var in DownloadRequest
		if err := decodePayload(req.Payload, &in); err != nil {
			return ErrorResponse(req.ID, err)
		}
		out, err := s.download(ctx, in.RemotePath, in.LocalPath)
		if err != nil {
			return ErrorResponse(req.ID, err)
		}
		frame, err := Response(req.ID, out)
		if err != nil {
			return ErrorResponse(req.ID, err)
		}
		return frame
	case CommandMountStart:
		var in MountStartRequest
		if err := decodePayload(req.Payload, &in); err != nil {
			return ErrorResponse(req.ID, err)
		}
		out, err := s.startMount(ctx, in.Selector, in.WindowsDrive, in.Mode)
		if err != nil {
			return ErrorResponse(req.ID, err)
		}
		frame, err := Response(req.ID, out)
		if err != nil {
			return ErrorResponse(req.ID, err)
		}
		return frame
	case CommandMountStatus:
		out := s.mountStatus()
		frame, err := Response(req.ID, out)
		if err != nil {
			return ErrorResponse(req.ID, err)
		}
		return frame
	case CommandMountStop:
		out, err := s.stopMount(ctx)
		if err != nil {
			return ErrorResponse(req.ID, err)
		}
		frame, err := Response(req.ID, out)
		if err != nil {
			return ErrorResponse(req.ID, err)
		}
		return frame
	default:
		return ErrorResponse(req.ID, fmt.Errorf("unknown command %q", req.Command))
	}
}
