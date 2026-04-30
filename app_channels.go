package main

import (
	"context"
	"fmt"
	"time"

	"TDrive/backend"
	"TDrive/backend/auth"
	"TDrive/backend/projection"

	"github.com/gotd/td/tg"
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

// ListChannels returns every drive known to this client (personal first,
// then shared in joined-at order). Used to render the sidebar.
func (a *App) ListChannels() ([]ChannelInfo, error) {
	if backend.DB == nil {
		return nil, fmt.Errorf("db not ready")
	}
	rows, err := projection.ListChannels(backend.DB)
	if err != nil {
		return nil, err
	}
	active := a.ActiveChannelID()
	out := make([]ChannelInfo, 0, len(rows))
	for _, c := range rows {
		out = append(out, ChannelInfo{
			ID:         c.ChannelID,
			Title:      c.Title,
			Kind:       c.Kind,
			IsActive:   c.ChannelID == active,
			InviteLink: c.InviteLink,
		})
	}
	return out, nil
}

// CreateSharedDrive creates a Telegram megagroup, exports an invite link,
// inserts the channel row, and switches the active drive to it.
//
// Returns the new ChannelInfo with the invite link populated.
func (a *App) CreateSharedDrive(title string, requireApproval bool) (ChannelInfo, error) {
	if backend.DB == nil {
		return ChannelInfo{}, fmt.Errorf("db not ready")
	}
	if title == "" {
		return ChannelInfo{}, fmt.Errorf("title required")
	}
	client, err := auth.Connect()
	if err != nil {
		return ChannelInfo{}, fmt.Errorf("connect: %w", err)
	}

	var (
		channelID  int64
		accessHash int64
		invite     string
	)
	err = client.Run(a.ctx, func(ctx context.Context) error {
		id, hash, err := auth.CreateMegagroup(ctx, client, title, "TDrive shared drive")
		if err != nil {
			return err
		}
		channelID, accessHash = id, hash

		peer := &tg.InputPeerChannel{ChannelID: id, AccessHash: hash}
		link, err := auth.ExportInviteLink(ctx, client.API(), peer, requireApproval)
		if err != nil {
			// Channel exists but no link yet. Save what we have; the
			// "Share" button will retry the export.
			fmt.Printf("warn: invite export failed for new drive %d: %v\n", id, err)
			return nil
		}
		invite = link
		return nil
	})
	if err != nil {
		return ChannelInfo{}, err
	}

	row := projection.Channel{
		ChannelID:            channelID,
		AccessHash:           accessHash,
		Title:                title,
		Kind:                 projection.KindShared,
		InviteLink:           invite,
		PersonalBackfillDone: true, // freshly created — nothing local to backfill
	}
	if err := projection.InsertChannel(backend.DB, row); err != nil {
		return ChannelInfo{}, err
	}
	a.activeChannelID.Store(channelID)

	return ChannelInfo{
		ID:         channelID,
		Title:      title,
		Kind:       projection.KindShared,
		IsActive:   true,
		InviteLink: invite,
	}, nil
}

// JoinSharedDrive imports an invite link. Immediate links return a joined
// channel. Approval-required links send a Telegram join request and return a
// durable pending record that can be checked later.
func (a *App) JoinSharedDrive(inviteLink string) (JoinDriveResult, error) {
	if backend.DB == nil {
		return JoinDriveResult{}, fmt.Errorf("db not ready")
	}
	hash, err := auth.ParseInviteHash(inviteLink)
	if err != nil {
		return JoinDriveResult{}, err
	}

	client, err := auth.Connect()
	if err != nil {
		return JoinDriveResult{}, fmt.Errorf("connect: %w", err)
	}

	var (
		channelID  int64
		accessHash int64
		title      string
		pending    bool
	)
	err = client.Run(a.ctx, func(ctx context.Context) error {
		info, err := auth.CheckInvite(ctx, client.API(), hash)
		if err != nil {
			return err
		}
		title = info.Title
		if info.AlreadyJoined {
			channelID, accessHash = info.ChannelID, info.AccessHash
			return nil
		}
		if info.RequestNeeded {
			if err := auth.RequestJoin(ctx, client.API(), hash); err != nil {
				return err
			}
			pending = true
			return nil
		}

		id, ah, err := auth.JoinByInvite(ctx, client.API(), hash)
		if err != nil {
			return err
		}
		channelID, accessHash = id, ah
		if resolvedTitle := lookupChannelTitle(ctx, client.API(), id, ah); resolvedTitle != "" {
			title = resolvedTitle
		}
		if title == "" {
			title = fmt.Sprintf("Drive %d", id)
		}
		return nil
	})
	if err != nil {
		return JoinDriveResult{}, err
	}

	if pending {
		p := projection.PendingJoin{
			InviteHash:  hash,
			InviteLink:  inviteLink,
			Title:       title,
			RequestedAt: time.Now().Unix(),
			Status:      projection.PendingJoinStatusPending,
		}
		if err := projection.UpsertPendingJoin(backend.DB, p); err != nil {
			return JoinDriveResult{}, err
		}
		info := pendingJoinInfo(p)
		return JoinDriveResult{Status: "pending", Pending: &info}, nil
	}

	info, err := a.registerJoinedSharedDrive(channelID, accessHash, title, "")
	if err != nil {
		return JoinDriveResult{}, err
	}
	_ = projection.DeletePendingJoin(backend.DB, hash)
	return JoinDriveResult{Status: "joined", Channel: &info}, nil
}

// ListPendingJoins returns approval-required joins this client is waiting on.
func (a *App) ListPendingJoins() ([]PendingJoinInfo, error) {
	if backend.DB == nil {
		return nil, fmt.Errorf("db not ready")
	}
	rows, err := projection.ListPendingJoins(backend.DB)
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
	if backend.DB == nil {
		return JoinDriveResult{}, fmt.Errorf("db not ready")
	}
	hash, err := auth.ParseInviteHash(inviteHash)
	if err != nil {
		return JoinDriveResult{}, err
	}
	p, err := projection.GetPendingJoin(backend.DB, hash)
	if err != nil {
		return JoinDriveResult{}, err
	}

	client, err := auth.Connect()
	if err != nil {
		_ = projection.UpdatePendingJoinCheck(backend.DB, hash, projection.PendingJoinStatusError, fmt.Sprintf("connect: %v", err))
		updated, _ := projection.GetPendingJoin(backend.DB, hash)
		info := pendingJoinInfo(updated)
		return JoinDriveResult{Status: "pending", Pending: &info}, nil
	}

	var invite auth.InviteInfo
	err = client.Run(a.ctx, func(ctx context.Context) error {
		var checkErr error
		invite, checkErr = auth.CheckInvite(ctx, client.API(), hash)
		return checkErr
	})
	if err != nil {
		_ = projection.UpdatePendingJoinCheck(backend.DB, hash, projection.PendingJoinStatusError, err.Error())
		updated, _ := projection.GetPendingJoin(backend.DB, hash)
		info := pendingJoinInfo(updated)
		return JoinDriveResult{Status: "pending", Pending: &info}, nil
	}
	if invite.AlreadyJoined {
		title := invite.Title
		if title == "" {
			title = p.Title
		}
		info, err := a.registerJoinedSharedDrive(invite.ChannelID, invite.AccessHash, title, p.InviteLink)
		if err != nil {
			return JoinDriveResult{}, err
		}
		_ = projection.DeletePendingJoin(backend.DB, hash)
		return JoinDriveResult{Status: "joined", Channel: &info}, nil
	}

	_ = projection.UpdatePendingJoinCheck(backend.DB, hash, projection.PendingJoinStatusPending, "")
	updated, _ := projection.GetPendingJoin(backend.DB, hash)
	info := pendingJoinInfo(updated)
	return JoinDriveResult{Status: "pending", Pending: &info}, nil
}

// RemovePendingJoin forgets a local pending request. It does not revoke the
// Telegram-side request; only a drive admin can reject it.
func (a *App) RemovePendingJoin(inviteHash string) error {
	if backend.DB == nil {
		return fmt.Errorf("db not ready")
	}
	hash, err := auth.ParseInviteHash(inviteHash)
	if err != nil {
		return err
	}
	return projection.DeletePendingJoin(backend.DB, hash)
}

// GetInviteLink fetches a fresh link from Telegram and caches it. Admin-
// only on the Telegram side; non-admin members will get an error from
// MessagesExportChatInvite. (Step 4 doesn't gate this client-side; we
// surface whatever Telegram returns.)
func (a *App) GetInviteLink(channelID int64) (string, error) {
	return a.exportInviteLink(channelID, false)
}

// GetApprovalInviteLink fetches an invite link where Telegram requires an
// admin to approve each requester before they become a member.
func (a *App) GetApprovalInviteLink(channelID int64) (string, error) {
	return a.exportInviteLink(channelID, true)
}

func (a *App) exportInviteLink(channelID int64, requireApproval bool) (string, error) {
	if backend.DB == nil {
		return "", fmt.Errorf("db not ready")
	}
	if channelID == 0 {
		channelID = a.ActiveChannelID()
	}
	if channelID == 0 {
		return "", fmt.Errorf("no channel id")
	}

	client, err := auth.Connect()
	if err != nil {
		return "", fmt.Errorf("connect: %w", err)
	}
	var link string
	err = client.Run(a.ctx, func(ctx context.Context) error {
		peer, err := a.inputPeerForChannel(ctx, client.API(), channelID)
		if err != nil {
			return err
		}
		l, err := auth.ExportInviteLink(ctx, client.API(), peer, requireApproval)
		if err != nil {
			return err
		}
		link = l
		return nil
	})
	if err != nil {
		return "", err
	}
	if err := projection.UpdateInviteLink(backend.DB, channelID, link); err != nil {
		fmt.Printf("warn: cache invite link: %v\n", err)
	}
	return link, nil
}

// ListJoinRequests lists Telegram users waiting for admin approval on a drive.
func (a *App) ListJoinRequests(channelID int64) ([]JoinRequestInfo, error) {
	if backend.DB == nil {
		return nil, fmt.Errorf("db not ready")
	}
	if channelID == 0 {
		return nil, fmt.Errorf("channel id required")
	}
	client, err := auth.Connect()
	if err != nil {
		return nil, fmt.Errorf("connect: %w", err)
	}

	var reqs []auth.JoinRequest
	err = client.Run(a.ctx, func(ctx context.Context) error {
		peer, err := a.inputPeerForChannel(ctx, client.API(), channelID)
		if err != nil {
			return err
		}
		rows, err := auth.ListJoinRequests(ctx, client.API(), peer)
		if err != nil {
			return err
		}
		reqs = rows
		return nil
	})
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
	return a.hideJoinRequest(channelID, userID, true)
}

func (a *App) RejectJoinRequest(channelID, userID int64) error {
	return a.hideJoinRequest(channelID, userID, false)
}

func (a *App) hideJoinRequest(channelID, userID int64, approved bool) error {
	if channelID == 0 {
		return fmt.Errorf("channel id required")
	}
	if userID == 0 {
		return fmt.Errorf("user id required")
	}
	client, err := auth.Connect()
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	return client.Run(a.ctx, func(ctx context.Context) error {
		peer, err := a.inputPeerForChannel(ctx, client.API(), channelID)
		if err != nil {
			return err
		}
		reqs, err := auth.ListJoinRequests(ctx, client.API(), peer)
		if err != nil {
			return err
		}
		var accessHash int64
		for _, r := range reqs {
			if r.UserID == userID {
				accessHash = r.AccessHash
				break
			}
		}
		if accessHash == 0 {
			return fmt.Errorf("join request for user %d not found", userID)
		}
		return auth.HideJoinRequest(ctx, client.API(), peer, userID, accessHash, approved)
	})
}

func (a *App) inputPeerForChannel(ctx context.Context, api *tg.Client, channelID int64) (*tg.InputPeerChannel, error) {
	if backend.DB != nil {
		if c, err := projection.GetChannel(backend.DB, channelID); err == nil && c.AccessHash != 0 {
			return &tg.InputPeerChannel{ChannelID: channelID, AccessHash: c.AccessHash}, nil
		}
	}
	_, peer, err := auth.ResolveDriveChannel(ctx, api, channelID)
	return peer, err
}

// LeaveSharedDrive leaves the Telegram channel and drops every local row
// scoped to it. If the active drive was this one, switches active to the
// personal drive.
func (a *App) LeaveSharedDrive(channelID int64) error {
	if backend.DB == nil {
		return fmt.Errorf("db not ready")
	}
	if channelID == 0 {
		return fmt.Errorf("channel id required")
	}
	c, err := projection.GetChannel(backend.DB, channelID)
	if err != nil {
		return err
	}
	if c.Kind != projection.KindShared {
		return fmt.Errorf("can only leave shared drives")
	}

	client, err := auth.Connect()
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	err = client.Run(a.ctx, func(ctx context.Context) error {
		inChan, _, err := auth.ResolveDriveChannel(ctx, client.API(), channelID)
		if err != nil {
			return err
		}
		return auth.LeaveChannel(ctx, client.API(), inChan)
	})
	if err != nil {
		// Even if Telegram refused, still drop local state — the user
		// asked to leave; they can rejoin via invite link if needed.
		fmt.Printf("warn: telegram leave failed: %v\n", err)
	}

	if err := projection.DeleteChannel(backend.DB, channelID); err != nil {
		return err
	}

	if a.ActiveChannelID() == channelID {
		// Switch to personal channel.
		rows, err := projection.ListChannels(backend.DB)
		if err == nil {
			for _, r := range rows {
				if r.Kind == projection.KindPersonal {
					a.activeChannelID.Store(r.ChannelID)
					break
				}
			}
		}
	}
	return nil
}

// lookupChannelTitle returns the channel title via ChannelsGetChannels.
// Returns "" on any failure — caller falls back to a placeholder.
func lookupChannelTitle(ctx context.Context, api *tg.Client, channelID, accessHash int64) string {
	chats, err := api.ChannelsGetChannels(ctx, []tg.InputChannelClass{
		&tg.InputChannel{ChannelID: channelID, AccessHash: accessHash},
	})
	if err != nil {
		return ""
	}
	if cc, ok := chats.(*tg.MessagesChats); ok {
		for _, ch := range cc.Chats {
			if c, ok := ch.(*tg.Channel); ok && c.ID == channelID {
				return c.Title
			}
		}
	}
	return ""
}

func (a *App) registerJoinedSharedDrive(channelID, accessHash int64, title, inviteLink string) (ChannelInfo, error) {
	if channelID == 0 {
		return ChannelInfo{}, fmt.Errorf("channel id required")
	}
	if title == "" {
		title = fmt.Sprintf("Drive %d", channelID)
	}
	row := projection.Channel{
		ChannelID:            channelID,
		AccessHash:           accessHash,
		Title:                title,
		Kind:                 projection.KindShared,
		InviteLink:           inviteLink,
		PersonalBackfillDone: true, // shared drives don't backfill local state
	}
	if err := projection.InsertChannel(backend.DB, row); err != nil {
		return ChannelInfo{}, err
	}
	a.activeChannelID.Store(channelID)

	// Keep this synchronous so callers only return once the joined drive has
	// projected whatever history exists.
	if err := a.syncEngine.InitialSyncEmptyChannel(a.ctx, channelID); err != nil {
		fmt.Printf("initial sync failed for joined drive %d: %v\n", channelID, err)
	}

	return ChannelInfo{
		ID:         channelID,
		Title:      title,
		Kind:       projection.KindShared,
		IsActive:   true,
		InviteLink: inviteLink,
	}, nil
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
