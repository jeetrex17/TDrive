package daemon

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"TDrive/backend/core"
	"TDrive/backend/mountcontent"
	"TDrive/backend/mountdav"
	"TDrive/backend/processlock"
)

const (
	daemonMaxFrameBytes = 1 << 20 // 1 MiB is far above the CLI protocol needs.
	daemonReadTimeout   = 5 * time.Minute
	daemonWriteTimeout  = 30 * time.Second
	daemonDrainTimeout  = 10 * time.Second
)

type ServerConfig struct {
	CoreConfig core.Config
	Warnf      func(format string, args ...any)
}

type Server struct {
	engine *core.Engine
	lock   *processlock.Lock
	mount  *mountdav.Server
	// mountMu serializes mount start/stop and owns the active content opener.
	// WebDAV reads never take this lock, and mount operations never take the
	// daemon-wide mutation lock.
	mountMu      sync.Mutex
	mountContent *mountcontent.Opener
	warnf        func(format string, args ...any)
	state        *state

	mu        sync.Mutex
	eventMu   sync.Mutex
	eventSubs map[chan Event]struct{}
	// writeMu serializes operations that mutate daemon state or the remote
	// projection. The daemon can accept concurrent clients, but the underlying
	// Telegram send + local projection sequence is intentionally single-writer.
	writeMu sync.Mutex
	// streamMu keeps progress events request-scoped in v1. Backend services emit
	// global transfer events, and download_progress has no transfer id, so only
	// one streaming transfer may be active until events carry a request id.
	streamMu sync.Mutex
	stopOnce sync.Once
	stop     context.CancelFunc
	wg       sync.WaitGroup
}

func Run(ctx context.Context, cfg ServerConfig) error {
	if ctx == nil {
		ctx = context.Background()
	}
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	s := &Server{
		warnf: cfg.Warnf,
		mount: mountdav.NewServer(),
		stop:  cancel,
	}
	if s.warnf == nil {
		s.warnf = func(format string, args ...any) {
			fmt.Printf(format, args...)
		}
	}
	if cfg.CoreConfig.Events == nil {
		cfg.CoreConfig.Events = s
	} else {
		cfg.CoreConfig.Events = multiEventSink{s, cfg.CoreConfig.Events}
	}

	lock, err := processlock.Acquire("daemon")
	if err != nil {
		return err
	}
	s.lock = lock
	defer func() {
		if err := s.lock.Release(); err != nil {
			s.warnf("daemon: release lock: %v\n", err)
		}
	}()

	engine, err := core.New(runCtx, cfg.CoreConfig)
	if err != nil {
		return err
	}
	s.engine = engine
	defer s.engine.Close()
	defer s.wg.Wait()
	defer func() {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer stopCancel()
		if err := s.stopMountServer(stopCtx); err != nil {
			s.warnf("daemon: stop mount: %v\n", err)
		}
	}()

	if err := s.loadState(); err != nil {
		s.warnf("daemon: load cli state: %v\n", err)
	}

	socketPath, err := SocketPath()
	if err != nil {
		return err
	}
	ln, err := listenSocket(socketPath)
	if err != nil {
		return err
	}
	defer cleanupSocket(socketPath)
	defer func() { _ = ln.Close() }()

	go func() {
		<-runCtx.Done()
		_ = ln.Close()
	}()

	s.warnf("TDrive daemon listening on %s\n", socketPath)
	for {
		conn, err := ln.Accept()
		if err != nil {
			if runCtx.Err() != nil || errors.Is(err, net.ErrClosed) {
				return nil
			}
			s.warnf("daemon: accept: %v\n", err)
			continue
		}
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			s.handleConn(runCtx, conn)
		}()
	}
}

func (s *Server) handleConn(ctx context.Context, conn net.Conn) {
	defer func() { _ = conn.Close() }()

	reader := bufio.NewReader(conn)
	enc := json.NewEncoder(conn)
	var encMu sync.Mutex
	writeFrame := func(frame Frame) error {
		encMu.Lock()
		defer encMu.Unlock()
		_ = conn.SetWriteDeadline(time.Now().Add(daemonWriteTimeout))
		defer func() { _ = conn.SetWriteDeadline(time.Time{}) }()
		return enc.Encode(frame)
	}

	for {
		var req Request
		if err := readRequestFrame(conn, reader, &req); err != nil {
			return
		}
		if isStreamingCommand(req.Command) {
			reqCtx, cancelReq := context.WithCancel(ctx)
			// Streaming requests do not read from the socket again while work is
			// in flight. Watch for EOF so an abandoned prompt cancels the backend
			// operation and releases streamMu.
			_ = conn.SetReadDeadline(time.Time{})
			go cancelOnConnectionClose(reader, cancelReq)
			if err := s.handleStreamingRequest(reqCtx, req, writeFrame); err != nil {
				cancelReq()
				return
			}
			cancelReq()
			return
		}
		frame := s.handleRequest(ctx, req)
		if err := writeFrame(frame); err != nil {
			return
		}
	}
}

func cancelOnConnectionClose(reader *bufio.Reader, cancel context.CancelFunc) {
	_, _ = reader.Peek(1)
	cancel()
}

func readRequestFrame(conn net.Conn, reader *bufio.Reader, req *Request) error {
	_ = conn.SetReadDeadline(time.Now().Add(daemonReadTimeout))
	defer func() { _ = conn.SetReadDeadline(time.Time{}) }()

	line, err := readLimitedLine(reader, daemonMaxFrameBytes)
	if err != nil {
		return err
	}
	if len(strings.TrimSpace(string(line))) == 0 {
		return io.EOF
	}
	if err := json.Unmarshal(line, req); err != nil {
		return err
	}
	if len(req.Payload) > daemonMaxFrameBytes {
		return fmt.Errorf("daemon: payload too large")
	}
	return nil
}

func readLimitedLine(reader *bufio.Reader, limit int) ([]byte, error) {
	var out []byte
	for {
		part, isPrefix, err := reader.ReadLine()
		if err != nil {
			return nil, err
		}
		if len(out)+len(part) > limit {
			return nil, fmt.Errorf("daemon: request too large")
		}
		out = append(out, part...)
		if !isPrefix {
			return out, nil
		}
	}
}

func (s *Server) handleStreamingRequest(ctx context.Context, req Request, writeFrame func(Frame) error) error {
	s.streamMu.Lock()
	defer s.streamMu.Unlock()

	if err := validateRequest(req); err != nil {
		return writeFrame(ErrorResponse(req.ID, err))
	}

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

	switch req.Command {
	case CommandStatus:
		frame, err := Response(req.ID, s.status(ctx))
		if err != nil {
			return ErrorResponse(req.ID, err)
		}
		return frame
	case CommandShutdown:
		frame, err := Response(req.ID, map[string]string{"status": "stopping"})
		if err != nil {
			return ErrorResponse(req.ID, err)
		}
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
		out, err := s.vaultStatus()
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
		out, err := s.vaultUnlock(in.Password)
		if err != nil {
			return ErrorResponse(req.ID, err)
		}
		frame, err := Response(req.ID, out)
		if err != nil {
			return ErrorResponse(req.ID, err)
		}
		return frame
	case CommandVaultLock:
		out, err := s.vaultLock()
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
		out, err := s.startMount(ctx, in.Selector, in.WindowsDrive)
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

func (s *Server) status(ctx context.Context) Status {
	out := Status{
		PID:             os.Getpid(),
		ActiveChannelID: s.engine.ActiveChannelID(),
		CurrentPath:     s.currentPath(),
	}
	if enc := s.engine.EncryptionService(); enc != nil {
		if st, err := enc.Status(); err == nil {
			out.VaultAvailable = st.Available
			out.VaultConfigured = st.PasswordSet
			out.VaultUnlocked = st.PasswordRemembered
			out.VaultHint = st.Hint
		}
	}
	return out
}

func (s *Server) vaultStatus() (VaultResponse, error) {
	status, err := s.engine.EncryptionService().Status()
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

func (s *Server) vaultUnlock(password string) (VaultResponse, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	if err := s.engine.EncryptionService().UsePassword(password); err != nil {
		return VaultResponse{}, err
	}
	return s.vaultStatus()
}

func (s *Server) vaultLock() (VaultResponse, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	s.engine.ClearEncryptionSession()
	return s.vaultStatus()
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

func (s *Server) pwd() (PathResponse, error) {
	drive, err := s.activeDrive()
	if err != nil {
		return PathResponse{}, err
	}
	return PathResponse{Drive: drive, CurrentPath: s.currentPath()}, nil
}

func (s *Server) cd(input string) (PathResponse, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	drive, err := s.activeDrive()
	if err != nil {
		return PathResponse{}, err
	}
	resolved, err := s.engine.ResolveFolderPath(drive.ID, s.currentPath(), input)
	if err != nil {
		return PathResponse{}, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state == nil {
		s.state = newState()
	}
	s.state.CurrentDriveID = drive.ID
	s.state.setCWD(drive.ID, resolved.Path)
	if err := s.state.save(); err != nil {
		return PathResponse{}, err
	}
	return PathResponse{Drive: drive, CurrentPath: resolved.Path}, nil
}

func (s *Server) listPath(input string) (ListResponse, error) {
	drive, err := s.activeDrive()
	if err != nil {
		return ListResponse{}, err
	}
	resolved, err := s.engine.ResolveFolderPath(drive.ID, s.currentPath(), input)
	if err != nil {
		return ListResponse{}, err
	}
	fs, err := s.engine.ReadService().FolderContents(drive.ID, resolved.ID)
	if err != nil {
		return ListResponse{}, err
	}

	entries := make([]Entry, 0, len(fs.Folders)+len(fs.Files))
	for _, folder := range fs.Folders {
		entries = append(entries, Entry{
			Type: "folder",
			ID:   folder.ID,
			Name: folder.Name,
			Path: core.JoinRemotePath(resolved.Path, folder.Name),
		})
	}
	for _, file := range fs.Files {
		entries = append(entries, Entry{
			Type:       "file",
			ID:         strconv.FormatInt(file.MsgID, 10),
			MsgID:      file.MsgID,
			Name:       file.Name,
			Path:       core.JoinRemotePath(resolved.Path, file.Name),
			Size:       file.Size,
			UploadTime: file.UploadTime,
			Encrypted:  file.Encrypted,
		})
	}
	return ListResponse{Drive: drive, Path: resolved.Path, Entries: entries}, nil
}

func (s *Server) find(query string, limit int) (FindResponse, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return FindResponse{}, fmt.Errorf("query required")
	}
	drive, err := s.activeDrive()
	if err != nil {
		return FindResponse{}, err
	}
	if limit <= 0 {
		limit = 50
	}
	hits, err := s.engine.ReadService().Search(drive.ID, query, limit)
	if err != nil {
		return FindResponse{}, err
	}
	out := FindResponse{Drive: drive, Results: make([]Entry, 0, len(hits))}
	for _, hit := range hits {
		entry := Entry{
			Type:       hit.Type,
			ID:         hit.ID,
			Name:       hit.Name,
			Size:       hit.Size,
			UploadTime: hit.UploadTime,
		}
		if hit.Type == "folder" {
			path, err := s.engine.FolderPathByID(drive.ID, hit.ID)
			if err != nil {
				return FindResponse{}, err
			}
			entry.Path = path
		} else {
			if msgID, err := strconv.ParseInt(hit.ID, 10, 64); err == nil {
				entry.MsgID = msgID
			}
			parentPath, err := s.engine.FolderPathByID(drive.ID, hit.ParentID)
			if err != nil {
				return FindResponse{}, err
			}
			entry.Path = core.JoinRemotePath(parentPath, hit.Name)
		}
		out.Results = append(out.Results, entry)
	}
	return out, nil
}

func (s *Server) mkdir(input string, parents bool) (EntryResponse, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	drive, err := s.activeDrive()
	if err != nil {
		return EntryResponse{}, err
	}
	if parents {
		entry, err := s.ensureFolderPath(drive.ID, s.currentPath(), input)
		if err != nil {
			return EntryResponse{}, err
		}
		return EntryResponse{Drive: drive, Entry: entry}, nil
	}

	parent, err := s.engine.ResolveParentPath(drive.ID, s.currentPath(), input)
	if err != nil {
		return EntryResponse{}, err
	}
	if err := s.ensureNameFree(drive.ID, parent.FolderID, parent.Name, core.ResolvedEntry{}); err != nil {
		return EntryResponse{}, err
	}
	folder, err := s.engine.FolderService().Create(drive.ID, parent.Name, parent.FolderID)
	if err != nil {
		return EntryResponse{}, err
	}
	entry := Entry{
		Type: "folder",
		ID:   folder.ID,
		Name: folder.Name,
		Path: core.JoinRemotePath(parent.Path, folder.Name),
	}
	return EntryResponse{Drive: drive, Entry: entry}, nil
}

func (s *Server) remove(ctx context.Context, input string, recursive bool) (EntryResponse, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	drive, err := s.activeDrive()
	if err != nil {
		return EntryResponse{}, err
	}
	entry, err := s.engine.ResolveEntryPath(drive.ID, s.currentPath(), input)
	if err != nil {
		return EntryResponse{}, err
	}
	if entry.Path == "/" {
		return EntryResponse{}, fmt.Errorf("refusing to remove root")
	}

	switch entry.Type {
	case "file":
		if err := s.engine.FileService().Delete(ctx, drive.ID, int(entry.MsgID)); err != nil {
			return EntryResponse{}, err
		}
	case "folder":
		if !recursive {
			return EntryResponse{}, fmt.Errorf("%s is a folder; use rm -r", entry.Path)
		}
		if err := s.engine.FolderService().Delete(ctx, drive.ID, entry.ID); err != nil {
			return EntryResponse{}, err
		}
	default:
		return EntryResponse{}, fmt.Errorf("unsupported entry type %q", entry.Type)
	}
	return EntryResponse{Drive: drive, Entry: entryFromResolved(entry)}, nil
}

func (s *Server) move(ctx context.Context, source string, destination string) (EntryResponse, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	drive, err := s.activeDrive()
	if err != nil {
		return EntryResponse{}, err
	}
	src, err := s.engine.ResolveEntryPath(drive.ID, s.currentPath(), source)
	if err != nil {
		return EntryResponse{}, err
	}
	if src.Path == "/" {
		return EntryResponse{}, fmt.Errorf("refusing to move root")
	}

	dstAbs, err := core.NormalizeRemotePath(s.currentPath(), destination)
	if err != nil {
		return EntryResponse{}, err
	}
	if dstAbs == src.Path {
		return EntryResponse{Drive: drive, Entry: entryFromResolved(src)}, nil
	}
	targetParentID, targetParentPath, targetName, err := s.moveTarget(drive.ID, dstAbs, src.Name)
	if err != nil {
		return EntryResponse{}, err
	}
	if err := s.ensureNameFree(drive.ID, targetParentID, targetName, src); err != nil {
		return EntryResponse{}, err
	}

	switch src.Type {
	case "file":
		if src.ParentID != targetParentID {
			if err := s.engine.FileService().Move(ctx, drive.ID, int(src.MsgID), targetParentID); err != nil {
				return EntryResponse{}, err
			}
		}
		if src.Name != targetName {
			if err := s.engine.FileService().Rename(ctx, drive.ID, int(src.MsgID), targetName); err != nil {
				return EntryResponse{}, err
			}
		}
	case "folder":
		if src.ParentID != targetParentID {
			if err := s.engine.FolderService().Move(drive.ID, src.ID, targetParentID); err != nil {
				return EntryResponse{}, err
			}
		}
		if src.Name != targetName {
			if err := s.engine.FolderService().Rename(drive.ID, src.ID, targetName); err != nil {
				return EntryResponse{}, err
			}
		}
	default:
		return EntryResponse{}, fmt.Errorf("unsupported entry type %q", src.Type)
	}

	entry := entryFromResolved(src)
	entry.Name = targetName
	entry.Path = core.JoinRemotePath(targetParentPath, targetName)
	return EntryResponse{Drive: drive, Entry: entry}, nil
}

func (s *Server) upload(ctx context.Context, localPath string, remotePath string, encrypt bool, extract bool) (UploadResponse, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	localPath = strings.TrimSpace(localPath)
	if localPath == "" {
		return UploadResponse{}, fmt.Errorf("local path required")
	}
	if !filepath.IsAbs(localPath) {
		return UploadResponse{}, fmt.Errorf("local path must be absolute")
	}
	info, err := os.Stat(localPath)
	if err != nil {
		return UploadResponse{}, err
	}

	drive, err := s.activeDrive()
	if err != nil {
		return UploadResponse{}, err
	}
	if info.IsDir() || extract {
		parentID, parentPath, err := s.importTarget(drive.ID, remotePath)
		if err != nil {
			return UploadResponse{}, err
		}
		if err := s.engine.FileService().RunImport(ctx, drive.ID, []string{localPath}, parentID, encrypt, extract); err != nil {
			return UploadResponse{}, err
		}
		return UploadResponse{Drive: drive, Entry: folderEntry(parentID, parentPath)}, nil
	}

	parentID, parentPath, targetName, err := s.uploadTarget(drive.ID, remotePath, filepath.Base(localPath))
	if err != nil {
		return UploadResponse{}, err
	}
	if err := s.ensureNameFree(drive.ID, parentID, targetName, core.ResolvedEntry{}); err != nil {
		return UploadResponse{}, err
	}

	metas, err := s.engine.FileService().Upload(ctx, drive.ID, []string{localPath}, []string{parentID}, encrypt)
	if err != nil {
		return UploadResponse{}, err
	}
	if len(metas) == 0 {
		return UploadResponse{}, fmt.Errorf("upload produced no file")
	}
	meta := metas[0]
	if meta.Name != targetName {
		if err := s.engine.FileService().Rename(ctx, drive.ID, meta.MsgID, targetName); err != nil {
			return UploadResponse{}, err
		}
	}

	entry := Entry{
		Type:       "file",
		ID:         strconv.Itoa(meta.MsgID),
		MsgID:      int64(meta.MsgID),
		Name:       targetName,
		Path:       core.JoinRemotePath(parentPath, targetName),
		Size:       meta.Size,
		UploadTime: meta.UploadTime,
		Encrypted:  meta.Encrypted,
	}
	return UploadResponse{Drive: drive, Entry: entry}, nil
}

func (s *Server) download(ctx context.Context, remotePath string, localPath string) (DownloadResponse, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	localPath = strings.TrimSpace(localPath)
	if localPath == "" {
		return DownloadResponse{}, fmt.Errorf("local path required")
	}
	if !filepath.IsAbs(localPath) {
		return DownloadResponse{}, fmt.Errorf("local path must be absolute")
	}
	if err := os.MkdirAll(filepath.Dir(localPath), 0o755); err != nil {
		return DownloadResponse{}, err
	}

	drive, err := s.activeDrive()
	if err != nil {
		return DownloadResponse{}, err
	}
	resolved, err := s.engine.ResolveEntryPath(drive.ID, s.currentPath(), remotePath)
	if err != nil {
		return DownloadResponse{}, err
	}
	if resolved.Type != "file" {
		return DownloadResponse{}, fmt.Errorf("%s is not a file", resolved.Path)
	}

	entry := entryFromResolved(resolved)
	result := s.engine.FileService().Download(ctx, drive.ID, int(resolved.MsgID), int(resolved.MsgID), func(defaultName string) (string, error) {
		return localPath, nil
	})
	if result.Status != "success" {
		if result.Message == "" {
			result.Message = result.Status
		}
		return DownloadResponse{}, fmt.Errorf("%s", result.Message)
	}
	if result.SavedPath != "" {
		localPath = result.SavedPath
	}
	return DownloadResponse{Drive: drive, Entry: entry, SavedPath: localPath}, nil
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

func (s *Server) ensureFolderPath(channelID int64, cwd string, input string) (Entry, error) {
	abs, err := core.NormalizeRemotePath(cwd, input)
	if err != nil {
		return Entry{}, err
	}
	if abs == "/" {
		return Entry{Type: "folder", ID: "", Name: "", Path: "/"}, nil
	}

	curID := ""
	curPath := "/"
	var last Entry
	for _, part := range strings.Split(strings.Trim(abs, "/"), "/") {
		if part == "" {
			continue
		}
		fs, err := s.engine.ReadService().FolderContents(channelID, curID)
		if err != nil {
			return Entry{}, err
		}
		var found *Entry
		for _, folder := range fs.Folders {
			if folder.Name == part {
				entry := Entry{
					Type: "folder",
					ID:   folder.ID,
					Name: folder.Name,
					Path: core.JoinRemotePath(curPath, folder.Name),
				}
				found = &entry
				break
			}
		}
		if found == nil {
			for _, file := range fs.Files {
				if file.Name == part {
					return Entry{}, fmt.Errorf("%s exists and is not a folder", core.JoinRemotePath(curPath, part))
				}
			}
			folder, err := s.engine.FolderService().Create(channelID, part, curID)
			if err != nil {
				return Entry{}, err
			}
			entry := Entry{
				Type: "folder",
				ID:   folder.ID,
				Name: folder.Name,
				Path: core.JoinRemotePath(curPath, folder.Name),
			}
			found = &entry
		}
		curID = found.ID
		curPath = found.Path
		last = *found
	}
	return last, nil
}

func (s *Server) moveTarget(channelID int64, dstAbs string, sourceName string) (parentID string, parentPath string, name string, err error) {
	dst, err := s.engine.ResolveEntryPath(channelID, "/", dstAbs)
	if err == nil {
		if dst.Type != "folder" {
			return "", "", "", fmt.Errorf("destination exists and is not a folder: %s", dstAbs)
		}
		return dst.ID, dst.Path, sourceName, nil
	}
	if !errors.Is(err, core.ErrPathNotFound) {
		return "", "", "", err
	}

	parent, err := s.engine.ResolveParentPath(channelID, "/", dstAbs)
	if err != nil {
		return "", "", "", err
	}
	return parent.FolderID, parent.Path, parent.Name, nil
}

// importTarget resolves the destination folder for directory and archive
// imports. RunImport owns creating the imported top-level folders beneath it.
func (s *Server) importTarget(channelID int64, remotePath string) (folderID string, folderPath string, err error) {
	remotePath = strings.TrimSpace(remotePath)
	if remotePath == "" {
		folder, err := s.engine.ResolveFolderPath(channelID, s.currentPath(), ".")
		if err != nil {
			return "", "", err
		}
		return folder.ID, folder.Path, nil
	}

	dstAbs, err := core.NormalizeRemotePath(s.currentPath(), remotePath)
	if err != nil {
		return "", "", err
	}
	dst, err := s.engine.ResolveEntryPath(channelID, "/", dstAbs)
	if err == nil {
		if dst.Type != "folder" {
			return "", "", fmt.Errorf("destination exists and is not a folder: %s", dstAbs)
		}
		return dst.ID, dst.Path, nil
	}
	if !errors.Is(err, core.ErrPathNotFound) {
		return "", "", err
	}
	return "", "", fmt.Errorf("destination folder not found: %s", dstAbs)
}

func (s *Server) uploadTarget(channelID int64, remotePath string, defaultName string) (parentID string, parentPath string, name string, err error) {
	remotePath = strings.TrimSpace(remotePath)
	if remotePath == "" {
		folder, err := s.engine.ResolveFolderPath(channelID, s.currentPath(), ".")
		if err != nil {
			return "", "", "", err
		}
		return folder.ID, folder.Path, defaultName, nil
	}
	mustBeFolder := strings.HasSuffix(remotePath, "/")

	dstAbs, err := core.NormalizeRemotePath(s.currentPath(), remotePath)
	if err != nil {
		return "", "", "", err
	}
	dst, err := s.engine.ResolveEntryPath(channelID, "/", dstAbs)
	if err == nil {
		if dst.Type != "folder" {
			return "", "", "", fmt.Errorf("destination exists and is not a folder: %s", dstAbs)
		}
		return dst.ID, dst.Path, defaultName, nil
	}
	if !errors.Is(err, core.ErrPathNotFound) {
		return "", "", "", err
	}
	if mustBeFolder {
		return "", "", "", fmt.Errorf("destination folder not found: %s", dstAbs)
	}
	parent, err := s.engine.ResolveParentPath(channelID, "/", dstAbs)
	if err != nil {
		return "", "", "", err
	}
	return parent.FolderID, parent.Path, parent.Name, nil
}

func (s *Server) ensureNameFree(channelID int64, parentID string, name string, allow core.ResolvedEntry) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("name required")
	}
	fs, err := s.engine.ReadService().FolderContents(channelID, parentID)
	if err != nil {
		return err
	}
	for _, folder := range fs.Folders {
		if folder.Name != name {
			continue
		}
		if allow.Type == "folder" && allow.ID == folder.ID {
			return nil
		}
		return fmt.Errorf("destination already exists: %s", name)
	}
	for _, file := range fs.Files {
		if file.Name != name {
			continue
		}
		if allow.Type == "file" && allow.MsgID == file.MsgID {
			return nil
		}
		return fmt.Errorf("destination already exists: %s", name)
	}
	return nil
}

func folderEntry(id string, folderPath string) Entry {
	name := ""
	if folderPath != "/" {
		name = path.Base(folderPath)
	}
	return Entry{Type: "folder", ID: id, Name: name, Path: folderPath}
}

func entryFromResolved(entry core.ResolvedEntry) Entry {
	return Entry{
		Type:       entry.Type,
		ID:         entry.ID,
		MsgID:      entry.MsgID,
		Name:       entry.Name,
		Path:       entry.Path,
		Size:       entry.Size,
		UploadTime: entry.UploadTime,
		Encrypted:  entry.Encrypted,
	}
}

func decodePayload(raw json.RawMessage, out any) error {
	if len(raw) == 0 {
		return nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("invalid request payload: %w", err)
	}
	return nil
}
