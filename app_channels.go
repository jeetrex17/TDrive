package main

import (
	"context"
	"fmt"

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
func (a *App) CreateSharedDrive(title string) (ChannelInfo, error) {
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
		link, err := auth.ExportInviteLink(ctx, client.API(), peer)
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

// JoinSharedDrive imports an invite link, inserts the channel row, switches
// the active drive to it, and kicks off InitialSyncEmptyChannel in a
// goroutine. Returns immediately; the frontend renders sync progress
// from emitted events.
func (a *App) JoinSharedDrive(inviteLink string) (ChannelInfo, error) {
	if backend.DB == nil {
		return ChannelInfo{}, fmt.Errorf("db not ready")
	}
	hash, err := auth.ParseInviteHash(inviteLink)
	if err != nil {
		return ChannelInfo{}, err
	}

	client, err := auth.Connect()
	if err != nil {
		return ChannelInfo{}, fmt.Errorf("connect: %w", err)
	}

	var (
		channelID  int64
		accessHash int64
		title      string
	)
	err = client.Run(a.ctx, func(ctx context.Context) error {
		id, ah, err := auth.JoinByInvite(ctx, client.API(), hash)
		if err != nil {
			return err
		}
		channelID, accessHash = id, ah

		title = lookupChannelTitle(ctx, client.API(), id, ah)
		if title == "" {
			title = fmt.Sprintf("Drive %d", id)
		}
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
		PersonalBackfillDone: true, // shared drives don't backfill local state
	}
	if err := projection.InsertChannel(backend.DB, row); err != nil {
		return ChannelInfo{}, err
	}
	a.activeChannelID.Store(channelID)

	// Run the initial sync synchronously so the caller (frontend) only
	// returns once the drive's history is projected. Avoids the prior race
	// where a follow-up Incremental could grab the per-channel mutex first
	// and trigger ChannelIsEmpty=false on this path. The frontend Join
	// modal stays in a loading state during this call.
	if err := a.syncEngine.InitialSyncEmptyChannel(a.ctx, channelID); err != nil {
		fmt.Printf("initial sync failed for joined drive %d: %v\n", channelID, err)
		// Don't fail the whole join — the channel row is inserted, the user
		// can still see / refresh later. Surface as a non-fatal warning to
		// the UI by returning the info; frontend can decide whether to
		// announce the partial state.
	}

	return ChannelInfo{
		ID:       channelID,
		Title:    title,
		Kind:     projection.KindShared,
		IsActive: true,
	}, nil
}

// GetInviteLink fetches a fresh link from Telegram and caches it. Admin-
// only on the Telegram side; non-admin members will get an error from
// MessagesExportChatInvite. (Step 4 doesn't gate this client-side; we
// surface whatever Telegram returns.)
func (a *App) GetInviteLink(channelID int64) (string, error) {
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
		_, peer, err := auth.ResolveDriveChannel(ctx, client.API(), channelID)
		if err != nil {
			return err
		}
		l, err := auth.ExportInviteLink(ctx, client.API(), peer)
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
