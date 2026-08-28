// Shared-drive Telegram helpers.
//
// Megagroup creation, invite link export, invite-link parsing + join,
// channel leave. Personal-drive creation lives behind the explicit
// personal-drive setup service, not as an auth side effect.
package auth

import (
	"context"
	"fmt"
	"strings"

	"github.com/gotd/td/telegram"
	"github.com/gotd/td/tg"
)

type InviteInfo struct {
	AlreadyJoined bool
	RequestNeeded bool
	Title         string
	ChannelID     int64
	AccessHash    int64
}

type JoinRequest struct {
	UserID      int64
	AccessHash  int64
	DisplayName string
	Username    string
	RequestedAt int64
	About       string
}

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
func ExportInviteLink(ctx context.Context, api *tg.Client, peer *tg.InputPeerChannel, requestNeeded bool) (string, error) {
	res, err := api.MessagesExportChatInvite(ctx, &tg.MessagesExportChatInviteRequest{
		Peer:          peer,
		RequestNeeded: requestNeeded,
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

func CheckInvite(ctx context.Context, api *tg.Client, hash string) (InviteInfo, error) {
	res, err := api.MessagesCheckChatInvite(ctx, hash)
	if err != nil {
		return InviteInfo{}, fmt.Errorf("check invite: %w", err)
	}
	switch invite := res.(type) {
	case *tg.ChatInviteAlready:
		id, accessHash, title := channelFromChat(invite.Chat)
		if id == 0 {
			return InviteInfo{}, fmt.Errorf("check invite: already joined but no channel in response")
		}
		return InviteInfo{
			AlreadyJoined: true,
			Title:         title,
			ChannelID:     id,
			AccessHash:    accessHash,
		}, nil
	case *tg.ChatInvite:
		return InviteInfo{
			RequestNeeded: invite.RequestNeeded,
			Title:         invite.Title,
		}, nil
	case *tg.ChatInvitePeek:
		_, _, title := channelFromChat(invite.Chat)
		return InviteInfo{Title: title}, nil
	default:
		return InviteInfo{}, fmt.Errorf("check invite: unexpected response type %T", res)
	}
}

func RequestJoin(ctx context.Context, api *tg.Client, hash string) error {
	if _, err := api.MessagesImportChatInvite(ctx, hash); err != nil {
		msg := strings.ToUpper(err.Error())
		if strings.Contains(msg, "INVITE_REQUEST_SENT") || strings.Contains(msg, "REQUEST_SENT") {
			return nil
		}
		return fmt.Errorf("request join: %w", err)
	}
	return nil
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

func ListJoinRequests(ctx context.Context, api *tg.Client, peer *tg.InputPeerChannel) ([]JoinRequest, error) {
	res, err := api.MessagesGetChatInviteImporters(ctx, &tg.MessagesGetChatInviteImportersRequest{
		Peer:       peer,
		Requested:  true,
		OffsetUser: &tg.InputUserEmpty{},
		Limit:      100,
	})
	if err != nil {
		return nil, fmt.Errorf("list join requests: %w", err)
	}
	users := make(map[int64]*tg.User, len(res.Users))
	for _, u := range res.Users {
		if user, ok := u.(*tg.User); ok {
			users[user.ID] = user
		}
	}

	out := make([]JoinRequest, 0, len(res.Importers))
	for _, importer := range res.Importers {
		req := JoinRequest{
			UserID:      importer.UserID,
			RequestedAt: int64(importer.Date),
			About:       importer.About,
		}
		if user := users[importer.UserID]; user != nil {
			req.AccessHash = user.AccessHash
			req.Username = user.Username
			req.DisplayName = displayUserName(user)
		}
		if req.DisplayName == "" {
			req.DisplayName = fmt.Sprintf("User %d", req.UserID)
		}
		out = append(out, req)
	}
	return out, nil
}

func HideJoinRequest(ctx context.Context, api *tg.Client, peer *tg.InputPeerChannel, userID, accessHash int64, approved bool) error {
	if userID == 0 {
		return fmt.Errorf("join request user id required")
	}
	_, err := api.MessagesHideChatJoinRequest(ctx, &tg.MessagesHideChatJoinRequestRequest{
		Peer:     peer,
		UserID:   &tg.InputUser{UserID: userID, AccessHash: accessHash},
		Approved: approved,
	})
	if err != nil {
		return fmt.Errorf("hide join request: %w", err)
	}
	return nil
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

func channelFromChat(c tg.ChatClass) (int64, int64, string) {
	switch ch := c.(type) {
	case *tg.Channel:
		return ch.ID, ch.AccessHash, ch.Title
	case *tg.ChannelForbidden:
		return ch.ID, ch.AccessHash, ch.Title
	default:
		return 0, 0, ""
	}
}

func displayUserName(u *tg.User) string {
	if u == nil {
		return ""
	}
	name := strings.TrimSpace(strings.TrimSpace(u.FirstName) + " " + strings.TrimSpace(u.LastName))
	if name != "" {
		return name
	}
	if strings.TrimSpace(u.Username) != "" {
		return "@" + strings.TrimSpace(u.Username)
	}
	return ""
}
