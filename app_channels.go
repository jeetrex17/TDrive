package main

import (
	"fmt"

	"TDrive/backend/projection"
	channelservice "TDrive/backend/services/channel"
)

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

func (a *App) channelService() *channelservice.Service {
	if a.engine == nil {
		return nil
	}
	return a.engine.ChannelService()
}

func (a *App) requireChannelService() (*channelservice.Service, error) {
	if svc := a.channelService(); svc != nil {
		return svc, nil
	}
	return nil, fmt.Errorf("backend not ready")
}

// ListChannels returns every drive known to this client (personal first,
// then shared in joined-at order). Used to render the sidebar.
func (a *App) ListChannels() ([]ChannelInfo, error) {
	svc, err := a.requireChannelService()
	if err != nil {
		return nil, err
	}
	rows, err := svc.ListChannels()
	if err != nil {
		return nil, err
	}
	active := a.ActiveChannelID()
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
func (a *App) CreateSharedDrive(title string, requireApproval bool) (ChannelInfo, error) {
	svc, err := a.requireChannelService()
	if err != nil {
		return ChannelInfo{}, err
	}
	row, err := svc.CreateSharedDrive(a.ctx, title, requireApproval)
	if err != nil {
		return ChannelInfo{}, err
	}
	return channelInfo(row, row.ChannelID), nil
}

// JoinSharedDrive imports an invite link. Immediate links return a joined
// channel. Approval-required links send a Telegram join request and return a
// durable pending record that can be checked later.
func (a *App) JoinSharedDrive(inviteLink string) (JoinDriveResult, error) {
	svc, err := a.requireChannelService()
	if err != nil {
		return JoinDriveResult{}, err
	}
	result, err := svc.JoinSharedDrive(a.ctx, inviteLink)
	if err != nil {
		return JoinDriveResult{}, err
	}
	return joinDriveResult(result), nil
}

// ListPendingJoins returns approval-required joins this client is waiting on.
func (a *App) ListPendingJoins() ([]PendingJoinInfo, error) {
	svc, err := a.requireChannelService()
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
func (a *App) CheckPendingJoin(inviteHash string) (JoinDriveResult, error) {
	svc, err := a.requireChannelService()
	if err != nil {
		return JoinDriveResult{}, err
	}
	result, err := svc.CheckPendingJoin(a.ctx, inviteHash)
	if err != nil {
		return JoinDriveResult{}, err
	}
	return joinDriveResult(result), nil
}

// RemovePendingJoin forgets a local pending request. It does not revoke the
// Telegram-side request; only a drive admin can reject it.
func (a *App) RemovePendingJoin(inviteHash string) error {
	svc, err := a.requireChannelService()
	if err != nil {
		return err
	}
	return svc.RemovePendingJoin(inviteHash)
}

// GetInviteLink fetches a fresh link from Telegram and caches it. Admin-
// only on the Telegram side; non-admin members will get an error from
// MessagesExportChatInvite. (Step 4 doesn't gate this client-side; we
// surface whatever Telegram returns.)
func (a *App) GetInviteLink(channelID int64) (string, error) {
	svc, err := a.requireChannelService()
	if err != nil {
		return "", err
	}
	return svc.ExportInviteLink(a.ctx, channelID, false)
}

// GetApprovalInviteLink fetches an invite link where Telegram requires an
// admin to approve each requester before they become a member.
func (a *App) GetApprovalInviteLink(channelID int64) (string, error) {
	svc, err := a.requireChannelService()
	if err != nil {
		return "", err
	}
	return svc.ExportInviteLink(a.ctx, channelID, true)
}

// ListJoinRequests lists Telegram users waiting for admin approval on a drive.
func (a *App) ListJoinRequests(channelID int64) ([]JoinRequestInfo, error) {
	svc, err := a.requireChannelService()
	if err != nil {
		return nil, err
	}
	reqs, err := svc.ListJoinRequests(a.ctx, channelID)
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

func (a *App) ApproveJoinRequest(channelID, userID int64) error {
	svc, err := a.requireChannelService()
	if err != nil {
		return err
	}
	return svc.HideJoinRequest(a.ctx, channelID, userID, true)
}

func (a *App) RejectJoinRequest(channelID, userID int64) error {
	svc, err := a.requireChannelService()
	if err != nil {
		return err
	}
	return svc.HideJoinRequest(a.ctx, channelID, userID, false)
}

// LeaveSharedDrive leaves the Telegram channel and drops every local row
// scoped to it. If the active drive was this one, switches active to the
// personal drive.
func (a *App) LeaveSharedDrive(channelID int64) error {
	svc, err := a.requireChannelService()
	if err != nil {
		return err
	}
	return svc.LeaveSharedDrive(a.ctx, channelID)
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
