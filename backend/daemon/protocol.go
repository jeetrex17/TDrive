package daemon

import (
	"encoding/json"
	"fmt"
)

const ProtocolVersion = 1

const (
	CommandStatus             = "daemon.status"
	CommandShutdown           = "daemon.shutdown"
	CommandAuthSetup          = "auth.setup"
	CommandAuthStatus         = "auth.status"
	CommandAuthLogin          = "auth.login"
	CommandAuthSubmitCode     = "auth.submit_code"
	CommandAuthSubmitPassword = "auth.submit_password"
	CommandAuthLogout         = "auth.logout"
	CommandWhoami             = "auth.whoami"
	CommandDriveList          = "drive.list"
	CommandDriveUse           = "drive.use"
	CommandDriveCreate        = "drive.create"
	CommandDriveJoin          = "drive.join"
	CommandDrivePendingList   = "drive.pending.list"
	CommandDrivePendingCheck  = "drive.pending.check"
	CommandDrivePendingRemove = "drive.pending.remove"
	CommandDriveInviteLink    = "drive.invite_link"
	CommandDriveJoinRequests  = "drive.join_requests"
	CommandDriveJoinAction    = "drive.join_action"
	CommandDriveLeave         = "drive.leave"
	CommandSync               = "maintenance.sync"
	CommandRebuild            = "maintenance.rebuild"
	CommandPWD                = "fs.pwd"
	CommandCD                 = "fs.cd"
	CommandList               = "fs.list"
	CommandFind               = "fs.find"
	CommandMkdir              = "fs.mkdir"
	CommandRemove             = "fs.remove"
	CommandMove               = "fs.move"
	CommandVaultStatus        = "vault.status"
	CommandVaultUnlock        = "vault.unlock"
	CommandVaultLock          = "vault.lock"
	CommandUpload             = "transfer.upload"
	CommandDownload           = "transfer.download"
)

type Request struct {
	ID      string          `json:"id,omitempty"`
	Version int             `json:"version"`
	Command string          `json:"command"`
	Payload json.RawMessage `json:"payload,omitempty"`
}

type Frame struct {
	ID      string          `json:"id,omitempty"`
	Version int             `json:"version"`
	Type    string          `json:"type"`
	OK      bool            `json:"ok,omitempty"`
	Event   string          `json:"event,omitempty"`
	Error   string          `json:"error,omitempty"`
	Payload json.RawMessage `json:"payload,omitempty"`
}

type Status struct {
	PID             int    `json:"pid"`
	ActiveChannelID int64  `json:"active_channel_id"`
	CurrentPath     string `json:"current_path,omitempty"`
	VaultAvailable  bool   `json:"vault_available"`
	VaultConfigured bool   `json:"vault_configured"`
	VaultUnlocked   bool   `json:"vault_unlocked"`
	VaultHint       string `json:"vault_hint,omitempty"`
}

type Drive struct {
	ID         int64  `json:"id"`
	Title      string `json:"title"`
	Kind       string `json:"kind"`
	Active     bool   `json:"active"`
	InviteLink string `json:"invite_link,omitempty"`
}

type DriveListResponse struct {
	Drives []Drive `json:"drives"`
}

type AuthSetupRequest struct {
	APIID   int    `json:"api_id"`
	APIHash string `json:"api_hash"`
}

type AuthStatus struct {
	SystemStatus string `json:"system_status"`
	LoggedIn     bool   `json:"logged_in"`
}

type AuthStatusResponse struct {
	Status AuthStatus `json:"status"`
}

type AuthLoginRequest struct {
	Phone string `json:"phone"`
}

type AuthLoginResponse struct {
	LoggedIn        bool   `json:"logged_in"`
	InitDriveResult string `json:"init_drive_result,omitempty"`
	ActiveChannelID int64  `json:"active_channel_id,omitempty"`
}

type AuthSubmitRequest struct {
	Value string `json:"value"`
}

type AuthLogoutRequest struct {
	Mode string `json:"mode,omitempty"`
}

type AuthLogoutResponse struct {
	Mode     string `json:"mode"`
	Stopping bool   `json:"stopping"`
}

type SelfUser struct {
	UserID      int64  `json:"user_id"`
	DisplayName string `json:"display_name"`
	Username    string `json:"username,omitempty"`
}

type SelfUserResponse struct {
	User SelfUser `json:"user"`
}

type DriveUseRequest struct {
	Selector string `json:"selector"`
}

type DriveUseResponse struct {
	Drive       Drive  `json:"drive"`
	CurrentPath string `json:"current_path"`
}

type DriveCreateRequest struct {
	Title           string `json:"title"`
	RequireApproval bool   `json:"require_approval,omitempty"`
}

type DriveJoinRequest struct {
	InviteLink string `json:"invite_link"`
}

type PendingJoin struct {
	InviteHash    string `json:"invite_hash"`
	InviteLink    string `json:"invite_link"`
	Title         string `json:"title"`
	RequestedAt   int64  `json:"requested_at"`
	LastCheckedAt int64  `json:"last_checked_at"`
	Status        string `json:"status"`
	LastError     string `json:"last_error,omitempty"`
}

type DriveJoinResponse struct {
	Status  string       `json:"status"`
	Drive   *Drive       `json:"drive,omitempty"`
	Pending *PendingJoin `json:"pending,omitempty"`
}

type PendingJoinsResponse struct {
	Pending []PendingJoin `json:"pending"`
}

type PendingJoinRequest struct {
	InviteHash string `json:"invite_hash"`
}

type DriveSelectorRequest struct {
	Selector string `json:"selector,omitempty"`
}

type InviteLinkRequest struct {
	Selector        string `json:"selector,omitempty"`
	RequireApproval bool   `json:"require_approval,omitempty"`
}

type InviteLinkResponse struct {
	Drive           Drive  `json:"drive"`
	Link            string `json:"link"`
	RequireApproval bool   `json:"require_approval,omitempty"`
}

type JoinRequest struct {
	UserID      int64  `json:"user_id"`
	DisplayName string `json:"display_name"`
	Username    string `json:"username,omitempty"`
	RequestedAt int64  `json:"requested_at"`
	About       string `json:"about,omitempty"`
}

type JoinRequestsResponse struct {
	Drive    Drive         `json:"drive"`
	Requests []JoinRequest `json:"requests"`
}

type JoinRequestActionRequest struct {
	Selector string `json:"selector,omitempty"`
	UserID   int64  `json:"user_id"`
	Approve  bool   `json:"approve"`
}

type MaintenanceResponse struct {
	Drive Drive `json:"drive"`
}

type PathRequest struct {
	Path string `json:"path"`
}

type PathResponse struct {
	Drive       Drive  `json:"drive"`
	CurrentPath string `json:"current_path"`
}

type Entry struct {
	Type       string `json:"type"`
	ID         string `json:"id,omitempty"`
	MsgID      int64  `json:"msg_id,omitempty"`
	Name       string `json:"name"`
	Path       string `json:"path"`
	Size       int64  `json:"size,omitempty"`
	UploadTime int64  `json:"upload_time,omitempty"`
	Encrypted  bool   `json:"encrypted,omitempty"`
}

type ListResponse struct {
	Drive   Drive   `json:"drive"`
	Path    string  `json:"path"`
	Entries []Entry `json:"entries"`
}

type FindRequest struct {
	Query string `json:"query"`
	Limit int    `json:"limit,omitempty"`
}

type FindResponse struct {
	Drive   Drive   `json:"drive"`
	Results []Entry `json:"results"`
}

type MkdirRequest struct {
	Path    string `json:"path"`
	Parents bool   `json:"parents,omitempty"`
}

type RemoveRequest struct {
	Path      string `json:"path"`
	Recursive bool   `json:"recursive,omitempty"`
}

type MoveRequest struct {
	Source      string `json:"source"`
	Destination string `json:"destination"`
}

type EntryResponse struct {
	Drive Drive `json:"drive"`
	Entry Entry `json:"entry"`
}

type VaultStatus struct {
	Available  bool   `json:"available"`
	Configured bool   `json:"configured"`
	Unlocked   bool   `json:"unlocked"`
	Hint       string `json:"hint,omitempty"`
}

type VaultUnlockRequest struct {
	Password string `json:"password"`
}

type VaultResponse struct {
	Status VaultStatus `json:"status"`
}

type UploadRequest struct {
	LocalPath  string `json:"local_path"`
	RemotePath string `json:"remote_path,omitempty"`
	Encrypt    bool   `json:"encrypt,omitempty"`
	Extract    bool   `json:"extract,omitempty"`
}

type UploadResponse struct {
	Drive Drive `json:"drive"`
	Entry Entry `json:"entry"`
}

type DownloadRequest struct {
	RemotePath string `json:"remote_path"`
	LocalPath  string `json:"local_path"`
}

type DownloadResponse struct {
	Drive     Drive  `json:"drive"`
	Entry     Entry  `json:"entry"`
	SavedPath string `json:"saved_path"`
}

type Event struct {
	Name string `json:"name"`
	Args []any  `json:"args,omitempty"`
}

func NewRequest(command string, payload any) (Request, error) {
	raw, err := marshalPayload(payload)
	if err != nil {
		return Request{}, err
	}
	return Request{
		Version: ProtocolVersion,
		Command: command,
		Payload: raw,
	}, nil
}

func Response(id string, payload any) (Frame, error) {
	raw, err := marshalPayload(payload)
	if err != nil {
		return Frame{}, err
	}
	return Frame{
		ID:      id,
		Version: ProtocolVersion,
		Type:    "response",
		OK:      true,
		Payload: raw,
	}, nil
}

func EventFrame(id string, event Event) (Frame, error) {
	raw, err := marshalPayload(event)
	if err != nil {
		return Frame{}, err
	}
	return Frame{
		ID:      id,
		Version: ProtocolVersion,
		Type:    "event",
		OK:      true,
		Event:   event.Name,
		Payload: raw,
	}, nil
}

func ErrorResponse(id string, err error) Frame {
	msg := ""
	if err != nil {
		msg = err.Error()
	}
	return Frame{
		ID:      id,
		Version: ProtocolVersion,
		Type:    "response",
		OK:      false,
		Error:   msg,
	}
}

func validateRequest(req Request) error {
	if req.Version != ProtocolVersion {
		return fmt.Errorf("unsupported daemon protocol version %d", req.Version)
	}
	if req.Command == "" {
		return fmt.Errorf("missing command")
	}
	return nil
}

func marshalPayload(v any) (json.RawMessage, error) {
	if v == nil {
		return nil, nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return b, nil
}
