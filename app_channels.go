package main

import (
	"fmt"

	"TDrive/backend/core"
	"TDrive/backend/projection"
	channelservice "TDrive/backend/services/channel"
)

// DriveService owns the set of drives themselves: which ones this client knows
// about, how a shared one is created, joined, approved or left, and how the
// personal one is first chosen or made.
//
// The line it draws is between a drive and its contents. Everything here
// changes *which* drives exist or who may reach them; nothing here reads or
// writes a file inside one. That matters because these are the only calls that
// can run before a drive is selected at all -- a fresh install has no personal
// channel, and a user recovering an old one has no projection yet -- so they
// must not share a type with methods that assume an active drive is already
// scoped. They are also the only calls that go to Telegram's channel and invite
// APIs rather than to the local projection, which is why they fail in ways
// (approval pending, admin-only, flood wait) no file operation ever produces.
type DriveService struct {
	host serviceHost
}

func newDriveService(host serviceHost) *DriveService {
	return &DriveService{host: host}
}

// ChannelInfo is the Wails-bound DTO for a drive listed in the sidebar.
type ChannelInfo struct {
	ID         int64  `json:"id"`
	Title      string `json:"title"`
	Kind       string `json:"kind"` // "personal" | "shared"
	IsActive   bool   `json:"is_active"`
	InviteLink string `json:"invite_link,omitempty"`
}

// PendingJoinInfo is a local record for approval-required invite links where
// the current Telegram account has requested access but is not a member yet.
type PendingJoinInfo struct {
	InviteHash    string `json:"invite_hash"`
	InviteLink    string `json:"invite_link"`
	Title         string `json:"title"`
	RequestedAt   int64  `json:"requested_at"`
	LastCheckedAt int64  `json:"last_checked_at"`
	Status        string `json:"status"`
	LastError     string `json:"last_error"`
}

// JoinDriveResult distinguishes instant joins from approval-required requests.
type JoinDriveResult struct {
	Status  string           `json:"status"` // "joined" | "pending"
	Channel *ChannelInfo     `json:"channel,omitempty"`
	Pending *PendingJoinInfo `json:"pending,omitempty"`
}

// JoinRequestInfo is shown to admins for approval-required invite links.
type JoinRequestInfo struct {
	UserID      int64  `json:"user_id"`
	DisplayName string `json:"display_name"`
	Username    string `json:"username,omitempty"`
	RequestedAt int64  `json:"requested_at"`
	About       string `json:"about,omitempty"`
}

// engine is the running engine, or nil until ServiceStartup has built one.
// Drive listing is one of the first things the sidebar asks for, so it really
// can arrive before there is an engine to answer with.
func (s *DriveService) engine() *core.Engine {
	return s.host.coreEngine()
}

func (s *DriveService) channelService() *channelservice.Service {
	engine := s.engine()
	if engine == nil {
		return nil
	}
	return engine.ChannelService()
}

func (s *DriveService) requireChannelService() (*channelservice.Service, error) {
	if svc := s.channelService(); svc != nil {
		return svc, nil
	}
	return nil, fmt.Errorf("backend not ready")
}

// ListChannels returns every drive known to this client (personal first,
// then shared in joined-at order). Used to render the sidebar.
func (s *DriveService) ListChannels() ([]ChannelInfo, error) {
	svc, err := s.requireChannelService()
	if err != nil {
		return nil, err
	}
	rows, err := svc.ListChannels()
	if err != nil {
		return nil, err
	}
	active := s.host.activeChannelID()
	out := make([]ChannelInfo, 0, len(rows))
	for _, c := range rows {
		out = append(out, channelInfo(c, active))
	}
	return out, nil
}

// CreateSharedDrive creates a Telegram megagroup, exports an invite link,
// inserts the channel row, and switches the active drive to it.
//
// Returns the new ChannelInfo with the invite link populated.
func (s *DriveService) CreateSharedDrive(title string, requireApproval bool) (ChannelInfo, error) {
	svc, err := s.requireChannelService()
	if err != nil {
		return ChannelInfo{}, err
	}
	row, err := svc.CreateSharedDrive(s.host.appContext(), title, requireApproval)
	if err != nil {
		return ChannelInfo{}, err
	}
	return channelInfo(row, row.ChannelID), nil
}

// JoinSharedDrive imports an invite link. Immediate links return a joined
// channel. Approval-required links send a Telegram join request and return a
// durable pending record that can be checked later.
func (s *DriveService) JoinSharedDrive(inviteLink string) (JoinDriveResult, error) {
	svc, err := s.requireChannelService()
	if err != nil {
		return JoinDriveResult{}, err
	}
	result, err := svc.JoinSharedDrive(s.host.appContext(), inviteLink)
	if err != nil {
		return JoinDriveResult{}, err
	}
	return joinDriveResult(result), nil
}

// ListPendingJoins returns approval-required joins this client is waiting on.
func (s *DriveService) ListPendingJoins() ([]PendingJoinInfo, error) {
	svc, err := s.requireChannelService()
	if err != nil {
		return nil, err
	}
	rows, err := svc.ListPendingJoins()
	if err != nil {
		return nil, err
	}
	out := make([]PendingJoinInfo, 0, len(rows))
	for _, p := range rows {
		out = append(out, pendingJoinInfo(p))
	}
	return out, nil
}

// CheckPendingJoin checks whether a prior approval-required request has now
// become a membership. Users call this manually from the sidebar; no realtime
// Telegram update stream is required for v1.
func (s *DriveService) CheckPendingJoin(inviteHash string) (JoinDriveResult, error) {
	svc, err := s.requireChannelService()
	if err != nil {
		return JoinDriveResult{}, err
	}
	result, err := svc.CheckPendingJoin(s.host.appContext(), inviteHash)
	if err != nil {
		return JoinDriveResult{}, err
	}
	return joinDriveResult(result), nil
}

// RemovePendingJoin forgets a local pending request. It does not revoke the
// Telegram-side request; only a drive admin can reject it.
func (s *DriveService) RemovePendingJoin(inviteHash string) error {
	svc, err := s.requireChannelService()
	if err != nil {
		return err
	}
	return svc.RemovePendingJoin(inviteHash)
}

// GetInviteLink fetches a fresh link from Telegram and caches it. Admin-
// only on the Telegram side; non-admin members will get an error from
// MessagesExportChatInvite. (Step 4 doesn't gate this client-side; we
// surface whatever Telegram returns.)
func (s *DriveService) GetInviteLink(channelID int64) (string, error) {
	svc, err := s.requireChannelService()
	if err != nil {
		return "", err
	}
	return svc.ExportInviteLink(s.host.appContext(), channelID, false)
}

// GetApprovalInviteLink fetches an invite link where Telegram requires an
// admin to approve each requester before they become a member.
func (s *DriveService) GetApprovalInviteLink(channelID int64) (string, error) {
	svc, err := s.requireChannelService()
	if err != nil {
		return "", err
	}
	return svc.ExportInviteLink(s.host.appContext(), channelID, true)
}

// ListJoinRequests lists Telegram users waiting for admin approval on a drive.
func (s *DriveService) ListJoinRequests(channelID int64) ([]JoinRequestInfo, error) {
	svc, err := s.requireChannelService()
	if err != nil {
		return nil, err
	}
	reqs, err := svc.ListJoinRequests(s.host.appContext(), channelID)
	if err != nil {
		return nil, err
	}

	out := make([]JoinRequestInfo, 0, len(reqs))
	for _, r := range reqs {
		out = append(out, JoinRequestInfo{
			UserID:      r.UserID,
			DisplayName: r.DisplayName,
			Username:    r.Username,
			RequestedAt: r.RequestedAt,
			About:       r.About,
		})
	}
	return out, nil
}

func (s *DriveService) ApproveJoinRequest(channelID, userID int64) error {
	svc, err := s.requireChannelService()
	if err != nil {
		return err
	}
	return svc.HideJoinRequest(s.host.appContext(), channelID, userID, true)
}

func (s *DriveService) RejectJoinRequest(channelID, userID int64) error {
	svc, err := s.requireChannelService()
	if err != nil {
		return err
	}
	return svc.HideJoinRequest(s.host.appContext(), channelID, userID, false)
}

// LeaveSharedDrive leaves the Telegram channel and drops every local row
// scoped to it. If the active drive was this one, switches active to the
// personal drive.
func (s *DriveService) LeaveSharedDrive(channelID int64) error {
	svc, err := s.requireChannelService()
	if err != nil {
		return err
	}
	return svc.LeaveSharedDrive(s.host.appContext(), channelID)
}

func channelInfo(c projection.Channel, active int64) ChannelInfo {
	return ChannelInfo{
		ID:         c.ChannelID,
		Title:      c.Title,
		Kind:       c.Kind,
		IsActive:   c.ChannelID == active,
		InviteLink: c.InviteLink,
	}
}

func joinDriveResult(result channelservice.JoinResult) JoinDriveResult {
	out := JoinDriveResult{Status: result.Status}
	if result.Channel != nil {
		info := channelInfo(*result.Channel, result.Channel.ChannelID)
		out.Channel = &info
	}
	if result.Pending != nil {
		info := pendingJoinInfo(*result.Pending)
		out.Pending = &info
	}
	return out
}

func pendingJoinInfo(p projection.PendingJoin) PendingJoinInfo {
	return PendingJoinInfo{
		InviteHash:    p.InviteHash,
		InviteLink:    p.InviteLink,
		Title:         p.Title,
		RequestedAt:   p.RequestedAt,
		LastCheckedAt: p.LastCheckedAt,
		Status:        p.Status,
		LastError:     p.LastError,
	}
}
