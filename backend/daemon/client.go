package daemon

import (
	"bufio"
	"encoding/json"
	"fmt"
)

type Client struct {
	socketPath string
}

type EventHandler func(Event)

func NewClient() (*Client, error) {
	path, err := SocketPath()
	if err != nil {
		return nil, err
	}
	return &Client{socketPath: path}, nil
}

func (c *Client) Status() (Status, error) {
	var status Status
	if err := c.call(CommandStatus, nil, &status); err != nil {
		return Status{}, err
	}
	return status, nil
}

func (c *Client) Shutdown() error {
	return c.call(CommandShutdown, nil, nil)
}

func (c *Client) AuthSetup(apiID int, apiHash string) (AuthStatusResponse, error) {
	var out AuthStatusResponse
	if err := c.call(CommandAuthSetup, AuthSetupRequest{APIID: apiID, APIHash: apiHash}, &out); err != nil {
		return AuthStatusResponse{}, err
	}
	return out, nil
}

func (c *Client) AuthStatus() (AuthStatusResponse, error) {
	var out AuthStatusResponse
	if err := c.call(CommandAuthStatus, nil, &out); err != nil {
		return AuthStatusResponse{}, err
	}
	return out, nil
}

func (c *Client) Login(phone string, onEvent EventHandler) (AuthLoginResponse, error) {
	var out AuthLoginResponse
	if err := c.stream(CommandAuthLogin, AuthLoginRequest{Phone: phone}, &out, onEvent); err != nil {
		return AuthLoginResponse{}, err
	}
	return out, nil
}

func (c *Client) PreparePersonalDrive() (PersonalDriveSetup, error) {
	var out PersonalDriveSetup
	if err := c.call(CommandPersonalDrivePrepare, nil, &out); err != nil {
		return PersonalDriveSetup{}, err
	}
	return out, nil
}

func (c *Client) SelectPersonalDrive(channelID string) (PersonalDriveSetup, error) {
	var out PersonalDriveSetup
	if err := c.call(CommandPersonalDriveSelect, PersonalDriveSelectRequest{ChannelID: channelID}, &out); err != nil {
		return PersonalDriveSetup{}, err
	}
	return out, nil
}

func (c *Client) CreatePersonalDrive() (PersonalDriveSetup, error) {
	var out PersonalDriveSetup
	if err := c.call(CommandPersonalDriveCreate, nil, &out); err != nil {
		return PersonalDriveSetup{}, err
	}
	return out, nil
}

func (c *Client) SubmitLoginCode(code string) error {
	return c.call(CommandAuthSubmitCode, AuthSubmitRequest{Value: code}, nil)
}

func (c *Client) SubmitLoginPassword(password string) error {
	return c.call(CommandAuthSubmitPassword, AuthSubmitRequest{Value: password}, nil)
}

func (c *Client) Logout(mode string) (AuthLogoutResponse, error) {
	var out AuthLogoutResponse
	if err := c.call(CommandAuthLogout, AuthLogoutRequest{Mode: mode}, &out); err != nil {
		return AuthLogoutResponse{}, err
	}
	return out, nil
}

func (c *Client) Whoami() (SelfUserResponse, error) {
	var out SelfUserResponse
	if err := c.call(CommandWhoami, nil, &out); err != nil {
		return SelfUserResponse{}, err
	}
	return out, nil
}

func (c *Client) ListDrives() (DriveListResponse, error) {
	var out DriveListResponse
	if err := c.call(CommandDriveList, nil, &out); err != nil {
		return DriveListResponse{}, err
	}
	return out, nil
}

func (c *Client) UseDrive(selector string) (DriveUseResponse, error) {
	var out DriveUseResponse
	if err := c.call(CommandDriveUse, DriveUseRequest{Selector: selector}, &out); err != nil {
		return DriveUseResponse{}, err
	}
	return out, nil
}

func (c *Client) CreateDrive(title string, requireApproval bool) (DriveUseResponse, error) {
	var out DriveUseResponse
	if err := c.call(CommandDriveCreate, DriveCreateRequest{Title: title, RequireApproval: requireApproval}, &out); err != nil {
		return DriveUseResponse{}, err
	}
	return out, nil
}

func (c *Client) JoinDrive(inviteLink string) (DriveJoinResponse, error) {
	var out DriveJoinResponse
	if err := c.call(CommandDriveJoin, DriveJoinRequest{InviteLink: inviteLink}, &out); err != nil {
		return DriveJoinResponse{}, err
	}
	return out, nil
}

func (c *Client) ListPendingJoins() (PendingJoinsResponse, error) {
	var out PendingJoinsResponse
	if err := c.call(CommandDrivePendingList, nil, &out); err != nil {
		return PendingJoinsResponse{}, err
	}
	return out, nil
}

func (c *Client) CheckPendingJoin(inviteHash string) (DriveJoinResponse, error) {
	var out DriveJoinResponse
	if err := c.call(CommandDrivePendingCheck, PendingJoinRequest{InviteHash: inviteHash}, &out); err != nil {
		return DriveJoinResponse{}, err
	}
	return out, nil
}

func (c *Client) RemovePendingJoin(inviteHash string) error {
	return c.call(CommandDrivePendingRemove, PendingJoinRequest{InviteHash: inviteHash}, nil)
}

func (c *Client) InviteLink(selector string, requireApproval bool) (InviteLinkResponse, error) {
	var out InviteLinkResponse
	if err := c.call(CommandDriveInviteLink, InviteLinkRequest{Selector: selector, RequireApproval: requireApproval}, &out); err != nil {
		return InviteLinkResponse{}, err
	}
	return out, nil
}

func (c *Client) JoinRequests(selector string) (JoinRequestsResponse, error) {
	var out JoinRequestsResponse
	if err := c.call(CommandDriveJoinRequests, DriveSelectorRequest{Selector: selector}, &out); err != nil {
		return JoinRequestsResponse{}, err
	}
	return out, nil
}

func (c *Client) ResolveJoinRequest(selector string, userID int64, approve bool) (JoinRequestsResponse, error) {
	var out JoinRequestsResponse
	if err := c.call(CommandDriveJoinAction, JoinRequestActionRequest{Selector: selector, UserID: userID, Approve: approve}, &out); err != nil {
		return JoinRequestsResponse{}, err
	}
	return out, nil
}

func (c *Client) LeaveDrive(selector string) (DriveUseResponse, error) {
	var out DriveUseResponse
	if err := c.call(CommandDriveLeave, DriveSelectorRequest{Selector: selector}, &out); err != nil {
		return DriveUseResponse{}, err
	}
	return out, nil
}

func (c *Client) Sync(selector string) (MaintenanceResponse, error) {
	var out MaintenanceResponse
	if err := c.call(CommandSync, DriveSelectorRequest{Selector: selector}, &out); err != nil {
		return MaintenanceResponse{}, err
	}
	return out, nil
}

func (c *Client) Rebuild(selector string) (MaintenanceResponse, error) {
	var out MaintenanceResponse
	if err := c.call(CommandRebuild, DriveSelectorRequest{Selector: selector}, &out); err != nil {
		return MaintenanceResponse{}, err
	}
	return out, nil
}

func (c *Client) PWD() (PathResponse, error) {
	var out PathResponse
	if err := c.call(CommandPWD, nil, &out); err != nil {
		return PathResponse{}, err
	}
	return out, nil
}

func (c *Client) CD(path string) (PathResponse, error) {
	var out PathResponse
	if err := c.call(CommandCD, PathRequest{Path: path}, &out); err != nil {
		return PathResponse{}, err
	}
	return out, nil
}

func (c *Client) List(path string) (ListResponse, error) {
	var out ListResponse
	if err := c.call(CommandList, PathRequest{Path: path}, &out); err != nil {
		return ListResponse{}, err
	}
	return out, nil
}

func (c *Client) Find(query string, limit int) (FindResponse, error) {
	var out FindResponse
	if err := c.call(CommandFind, FindRequest{Query: query, Limit: limit}, &out); err != nil {
		return FindResponse{}, err
	}
	return out, nil
}

func (c *Client) Mkdir(path string, parents bool) (EntryResponse, error) {
	var out EntryResponse
	if err := c.call(CommandMkdir, MkdirRequest{Path: path, Parents: parents}, &out); err != nil {
		return EntryResponse{}, err
	}
	return out, nil
}

func (c *Client) Remove(path string, recursive bool) (EntryResponse, error) {
	var out EntryResponse
	if err := c.call(CommandRemove, RemoveRequest{Path: path, Recursive: recursive}, &out); err != nil {
		return EntryResponse{}, err
	}
	return out, nil
}

func (c *Client) Move(source string, destination string) (EntryResponse, error) {
	var out EntryResponse
	if err := c.call(CommandMove, MoveRequest{Source: source, Destination: destination}, &out); err != nil {
		return EntryResponse{}, err
	}
	return out, nil
}

func (c *Client) VaultStatus() (VaultResponse, error) {
	var out VaultResponse
	if err := c.call(CommandVaultStatus, nil, &out); err != nil {
		return VaultResponse{}, err
	}
	return out, nil
}

func (c *Client) VaultUnlock(password string) (VaultResponse, error) {
	var out VaultResponse
	if err := c.call(CommandVaultUnlock, VaultUnlockRequest{Password: password}, &out); err != nil {
		return VaultResponse{}, err
	}
	return out, nil
}

func (c *Client) VaultLock() (VaultResponse, error) {
	var out VaultResponse
	if err := c.call(CommandVaultLock, nil, &out); err != nil {
		return VaultResponse{}, err
	}
	return out, nil
}

func (c *Client) Upload(localPath string, remotePath string, encrypt bool, extract bool, onEvent EventHandler) (UploadResponse, error) {
	var out UploadResponse
	err := c.stream(CommandUpload, UploadRequest{
		LocalPath:  localPath,
		RemotePath: remotePath,
		Encrypt:    encrypt,
		Extract:    extract,
	}, &out, onEvent)
	if err != nil {
		return UploadResponse{}, err
	}
	return out, nil
}

func (c *Client) Download(remotePath string, localPath string, onEvent EventHandler) (DownloadResponse, error) {
	var out DownloadResponse
	err := c.stream(CommandDownload, DownloadRequest{
		RemotePath: remotePath,
		LocalPath:  localPath,
	}, &out, onEvent)
	if err != nil {
		return DownloadResponse{}, err
	}
	return out, nil
}

func (c *Client) MountStart(selector string, windowsDrive string, mode string) (MountResponse, error) {
	var out MountResponse
	if err := c.call(CommandMountStart, MountStartRequest{Selector: selector, WindowsDrive: windowsDrive, Mode: mode}, &out); err != nil {
		return MountResponse{}, err
	}
	return out, nil
}

func (c *Client) MountStatus() (MountResponse, error) {
	var out MountResponse
	if err := c.call(CommandMountStatus, nil, &out); err != nil {
		return MountResponse{}, err
	}
	return out, nil
}

func (c *Client) MountStop() (MountResponse, error) {
	var out MountResponse
	if err := c.call(CommandMountStop, nil, &out); err != nil {
		return MountResponse{}, err
	}
	return out, nil
}

func (c *Client) call(command string, payload any, out any) error {
	req, err := NewRequest(command, payload)
	if err != nil {
		return err
	}

	conn, err := dialSocket(c.socketPath)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()

	enc := json.NewEncoder(conn)
	dec := json.NewDecoder(bufio.NewReader(conn))

	if err := enc.Encode(req); err != nil {
		return fmt.Errorf("daemon request: %w", err)
	}
	var frame Frame
	if err := dec.Decode(&frame); err != nil {
		return fmt.Errorf("daemon response: %w", err)
	}
	if !frame.OK {
		if frame.Error == "" {
			frame.Error = "daemon request failed"
		}
		return fmt.Errorf("%s", frame.Error)
	}
	if out != nil && len(frame.Payload) > 0 {
		if err := json.Unmarshal(frame.Payload, out); err != nil {
			return fmt.Errorf("daemon response payload: %w", err)
		}
	}
	return nil
}

func (c *Client) stream(command string, payload any, out any, onEvent EventHandler) error {
	req, err := NewRequest(command, payload)
	if err != nil {
		return err
	}

	conn, err := dialSocket(c.socketPath)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()

	enc := json.NewEncoder(conn)
	dec := json.NewDecoder(bufio.NewReader(conn))

	if err := enc.Encode(req); err != nil {
		return fmt.Errorf("daemon request: %w", err)
	}
	for {
		var frame Frame
		if err := dec.Decode(&frame); err != nil {
			return fmt.Errorf("daemon response: %w", err)
		}
		switch frame.Type {
		case "event":
			if onEvent == nil {
				continue
			}
			var event Event
			if len(frame.Payload) > 0 {
				if err := json.Unmarshal(frame.Payload, &event); err != nil {
					return fmt.Errorf("daemon event payload: %w", err)
				}
			}
			if event.Name == "" {
				event.Name = frame.Event
			}
			onEvent(event)
		case "response", "":
			if !frame.OK {
				if frame.Error == "" {
					frame.Error = "daemon request failed"
				}
				return fmt.Errorf("%s", frame.Error)
			}
			if out != nil && len(frame.Payload) > 0 {
				if err := json.Unmarshal(frame.Payload, out); err != nil {
					return fmt.Errorf("daemon response payload: %w", err)
				}
			}
			return nil
		default:
			return fmt.Errorf("unexpected daemon frame type %q", frame.Type)
		}
	}
}
