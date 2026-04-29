// Shared-drive Telegram helpers.
//
// Megagroup creation, invite link export, invite-link parsing + join,
// channel leave. The personal-channel helpers (CreateTDriveChannel etc.)
// stay in channel.go; this file is shared-drive specific so the layering
// is obvious.
package auth

import (
	"context"
	"fmt"
	"strings"

	"github.com/gotd/td/telegram"
	"github.com/gotd/td/tg"
)

// CreateMegagroup creates a Telegram megagroup (not a broadcast channel) and
// returns its channel ID + access hash. Megagroup so every TDrive member can
// upload; broadcast would require admin rights to post.
func CreateMegagroup(ctx context.Context, client *telegram.Client, title, about string) (channelID, accessHash int64, err error) {
	updates, err := client.API().ChannelsCreateChannel(ctx, &tg.ChannelsCreateChannelRequest{
		Megagroup: true,
		Broadcast: false,
		Title:     title,
		About:     about,
	})
	if err != nil {
		return 0, 0, fmt.Errorf("create megagroup: %w", err)
	}
	channelID, accessHash = findMegagroup(updates)
	if channelID == 0 {
		return 0, 0, fmt.Errorf("create megagroup: no channel in response")
	}
	return channelID, accessHash, nil
}

// ExportInviteLink generates a fresh `t.me/+...` invite link for the given
// channel. Telegram returns a different link object every time; the URL
// itself only changes when an admin explicitly revokes.
func ExportInviteLink(ctx context.Context, api *tg.Client, peer *tg.InputPeerChannel) (string, error) {
	res, err := api.MessagesExportChatInvite(ctx, &tg.MessagesExportChatInviteRequest{
		Peer: peer,
	})
	if err != nil {
		return "", fmt.Errorf("export invite: %w", err)
	}
	if exp, ok := res.(*tg.ChatInviteExported); ok {
		link := strings.TrimSpace(exp.Link)
		if link == "" {
			return "", fmt.Errorf("export invite: empty link")
		}
		return link, nil
	}
	return "", fmt.Errorf("export invite: unexpected response type %T", res)
}

// ParseInviteHash extracts the hash portion from a Telegram invite link.
// Accepts:
//
//	t.me/+abc123
//	https://t.me/+abc123
//	telegram.me/+abc123
//	t.me/joinchat/abc123
//	https://t.me/joinchat/abc123
//
// And the bare hash itself ("abc123"). Returns the hash or an error.
func ParseInviteHash(link string) (string, error) {
	s := strings.TrimSpace(link)
	if s == "" {
		return "", fmt.Errorf("invite link is empty")
	}
	// Strip scheme.
	for _, prefix := range []string{"https://", "http://"} {
		if strings.HasPrefix(s, prefix) {
			s = s[len(prefix):]
			break
		}
	}
	// Strip host.
	for _, host := range []string{"t.me/", "telegram.me/", "telegram.dog/"} {
		if strings.HasPrefix(s, host) {
			s = s[len(host):]
			break
		}
	}
	// Strip joinchat/ prefix or leading +.
	switch {
	case strings.HasPrefix(s, "joinchat/"):
		s = s[len("joinchat/"):]
	case strings.HasPrefix(s, "+"):
		s = s[1:]
	}
	// Trim trailing slash + any query string.
	if i := strings.IndexAny(s, "/?#"); i >= 0 {
		s = s[:i]
	}
	if s == "" {
		return "", fmt.Errorf("invite link has no hash")
	}
	for _, r := range s {
		ok := (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') ||
			r == '_' || r == '-'
		if !ok {
			return "", fmt.Errorf("invite hash contains invalid character %q", r)
		}
	}
	return s, nil
}

// JoinByInvite accepts a parsed invite hash and returns the channel ID +
// access hash of the joined channel.
func JoinByInvite(ctx context.Context, api *tg.Client, hash string) (channelID, accessHash int64, err error) {
	updates, err := api.MessagesImportChatInvite(ctx, hash)
	if err != nil {
		return 0, 0, fmt.Errorf("import invite: %w", err)
	}
	channelID, accessHash = findMegagroup(updates)
	if channelID == 0 {
		return 0, 0, fmt.Errorf("import invite: no channel in response")
	}
	return channelID, accessHash, nil
}

// LeaveChannel removes the current account from the given channel/megagroup.
func LeaveChannel(ctx context.Context, api *tg.Client, peer *tg.InputChannel) error {
	_, err := api.ChannelsLeaveChannel(ctx, peer)
	if err != nil {
		return fmt.Errorf("leave channel: %w", err)
	}
	return nil
}

// findMegagroup scans an UpdatesClass for the first megagroup-or-channel
// chat and returns its (id, access_hash). Returns (0, 0) if none found.
func findMegagroup(updates tg.UpdatesClass) (int64, int64) {
	var chats []tg.ChatClass
	switch u := updates.(type) {
	case *tg.Updates:
		chats = u.Chats
	case *tg.UpdatesCombined:
		chats = u.Chats
	}
	for _, c := range chats {
		switch ch := c.(type) {
		case *tg.Channel:
			return ch.ID, ch.AccessHash
		case *tg.ChannelForbidden:
			return ch.ID, ch.AccessHash
		}
	}
	return 0, 0
}
