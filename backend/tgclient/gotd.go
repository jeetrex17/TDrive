package tgclient

import (
	"context"
	"fmt"
	"io"
	"math/rand"
	"strings"

	"TDrive/backend/auth"

	"github.com/gotd/td/telegram"
	"github.com/gotd/td/telegram/uploader"
	"github.com/gotd/td/tg"
)

// Gotd is the production Client implementation. Each call opens a fresh
// gotd Run scope so we don't have to keep a long-lived client in TDrive's
// previous style.
type Gotd struct {
	connect func() (*telegram.Client, error)
}

// NewGotd constructs a Client that uses the given factory to spin up gotd
// clients on demand. In production wire this to auth.Connect.
func NewGotd(connect func() (*telegram.Client, error)) *Gotd {
	return &Gotd{connect: connect}
}

func (g *Gotd) run(ctx context.Context, fn func(ctx context.Context, api *tg.Client) error) error {
	client, err := g.connect()
	if err != nil {
		return fmt.Errorf("tgclient: connect: %w", err)
	}
	err = client.Run(ctx, func(rctx context.Context) error {
		return fn(rctx, client.API())
	})
	return normalizeError(err)
}

func (g *Gotd) runClient(ctx context.Context, fn func(ctx context.Context, client *telegram.Client) error) error {
	client, err := g.connect()
	if err != nil {
		return fmt.Errorf("tgclient: connect: %w", err)
	}
	err = client.Run(ctx, func(rctx context.Context) error {
		return fn(rctx, client)
	})
	return normalizeError(err)
}

func normalizeError(err error) error {
	if err == nil {
		return nil
	}
	if wait, ok := telegram.AsFloodWait(err); ok {
		return NewFloodWaitError(wait)
	}
	return err
}

func (g *Gotd) SelfID(ctx context.Context) (int64, error) {
	var id int64
	err := g.run(ctx, func(ctx context.Context, api *tg.Client) error {
		me, err := api.UsersGetUsers(ctx, []tg.InputUserClass{&tg.InputUserSelf{}})
		if err != nil {
			return err
		}
		for _, u := range me {
			if user, ok := u.(*tg.User); ok && user.Self {
				id = user.ID
				return nil
			}
		}
		return fmt.Errorf("tgclient: self user not found")
	})
	return id, err
}

func (g *Gotd) SendControl(ctx context.Context, peer InputPeer, text string, silent bool) (int64, error) {
	var msgID int64
	err := g.run(ctx, func(ctx context.Context, api *tg.Client) error {
		req := &tg.MessagesSendMessageRequest{
			Peer:      toPeer(peer),
			Message:   text,
			RandomID:  rand.Int63(),
			Silent:    silent,
			NoWebpage: true,
		}
		updates, err := api.MessagesSendMessage(ctx, req)
		if err != nil {
			return err
		}
		msgID = extractMsgID(updates)
		if msgID == 0 {
			return fmt.Errorf("tgclient: send control returned no msg id")
		}
		return nil
	})
	return msgID, err
}

func (g *Gotd) SendFile(ctx context.Context, peer InputPeer, r io.Reader, name, caption string, totalSize int64, onProgress func(sent, total int64)) (SendFileResult, error) {
	var result SendFileResult
	err := g.run(ctx, func(ctx context.Context, api *tg.Client) error {
		u := uploader.NewUploader(api)
		var src io.Reader = r
		if onProgress != nil {
			src = &progressReader{
				r:          r,
				total:      totalSize,
				onProgress: onProgress,
			}
		}
		uploadResult, err := u.FromReader(ctx, name, src)
		if err != nil {
			return fmt.Errorf("tgclient: upload: %w", err)
		}

		req := &tg.MessagesSendMediaRequest{
			Peer: toPeer(peer),
			Media: &tg.InputMediaUploadedDocument{
				File:      uploadResult,
				MimeType:  "application/octet-stream",
				ForceFile: true,
				Attributes: []tg.DocumentAttributeClass{
					&tg.DocumentAttributeFilename{FileName: name},
				},
			},
			RandomID: rand.Int63(),
			Message:  caption,
		}
		updates, err := api.MessagesSendMedia(ctx, req)
		if err != nil {
			return fmt.Errorf("tgclient: send media: %w", err)
		}
		result.MsgID = extractMsgID(updates)
		if result.MsgID == 0 {
			return fmt.Errorf("tgclient: send file returned no msg id")
		}
		return nil
	})
	return result, err
}

func (g *Gotd) GetHistory(ctx context.Context, peer InputPeer, minID, offsetID int64, limit int) ([]HistoryMessage, error) {
	if limit <= 0 {
		limit = 100
	}
	var out []HistoryMessage
	err := g.run(ctx, func(ctx context.Context, api *tg.Client) error {
		req := &tg.MessagesGetHistoryRequest{
			Peer:     toPeer(peer),
			MinID:    int(minID),
			OffsetID: int(offsetID),
			Limit:    limit,
		}
		result, err := api.MessagesGetHistory(ctx, req)
		if err != nil {
			return err
		}
		var messages []tg.MessageClass
		switch r := result.(type) {
		case *tg.MessagesMessages:
			messages = r.Messages
		case *tg.MessagesMessagesSlice:
			messages = r.Messages
		case *tg.MessagesChannelMessages:
			messages = r.Messages
		}

		for _, msg := range messages {
			fullMsg, ok := msg.(*tg.Message)
			if !ok {
				continue
			}
			text := strings.TrimRight(fullMsg.Message, "\r\n")
			fromID := int64(0)
			if from, ok := fullMsg.FromID.(*tg.PeerUser); ok {
				fromID = from.UserID
			}
			var (
				hasMedia  bool
				mediaSize int64
			)
			if media, ok := fullMsg.Media.(*tg.MessageMediaDocument); ok {
				hasMedia = true
				if doc, ok := media.Document.(*tg.Document); ok {
					mediaSize = doc.Size
				}
			}

			out = append(out, HistoryMessage{
				MsgID:     int64(fullMsg.ID),
				Date:      int64(fullMsg.Date),
				FromID:    fromID,
				Text:      text,
				HasMedia:  hasMedia,
				MediaSize: mediaSize,
			})
		}
		return nil
	})
	return out, err
}

func (g *Gotd) DeleteMessages(ctx context.Context, peer InputPeer, msgIDs []int64) error {
	if len(msgIDs) == 0 {
		return nil
	}
	return g.run(ctx, func(ctx context.Context, api *tg.Client) error {
		ids := make([]int, 0, len(msgIDs))
		for _, id := range msgIDs {
			ids = append(ids, int(id))
		}
		_, err := api.ChannelsDeleteMessages(ctx, &tg.ChannelsDeleteMessagesRequest{
			Channel: &tg.InputChannel{ChannelID: peer.ChannelID, AccessHash: peer.AccessHash},
			ID:      ids,
		})
		return err
	})
}

func (g *Gotd) CreateMegagroup(ctx context.Context, title, about string) (InputPeer, error) {
	var peer InputPeer
	err := g.runClient(ctx, func(ctx context.Context, client *telegram.Client) error {
		channelID, accessHash, err := auth.CreateMegagroup(ctx, client, title, about)
		if err != nil {
			return err
		}
		peer = InputPeer{ChannelID: channelID, AccessHash: accessHash}
		return nil
	})
	return peer, err
}

func (g *Gotd) ExportInviteLink(ctx context.Context, peer InputPeer, requestNeeded bool) (string, error) {
	var link string
	err := g.run(ctx, func(ctx context.Context, api *tg.Client) error {
		l, err := auth.ExportInviteLink(ctx, api, toPeer(peer), requestNeeded)
		if err != nil {
			return err
		}
		link = l
		return nil
	})
	return link, err
}

func (g *Gotd) CheckInvite(ctx context.Context, hash string) (InviteInfo, error) {
	var out InviteInfo
	err := g.run(ctx, func(ctx context.Context, api *tg.Client) error {
		info, err := auth.CheckInvite(ctx, api, hash)
		if err != nil {
			return err
		}
		out = InviteInfo{
			AlreadyJoined: info.AlreadyJoined,
			RequestNeeded: info.RequestNeeded,
			Title:         info.Title,
			ChannelID:     info.ChannelID,
			AccessHash:    info.AccessHash,
		}
		return nil
	})
	return out, err
}

func (g *Gotd) RequestJoin(ctx context.Context, hash string) error {
	return g.run(ctx, func(ctx context.Context, api *tg.Client) error {
		return auth.RequestJoin(ctx, api, hash)
	})
}

func (g *Gotd) JoinByInvite(ctx context.Context, hash string) (InputPeer, error) {
	var peer InputPeer
	err := g.run(ctx, func(ctx context.Context, api *tg.Client) error {
		channelID, accessHash, err := auth.JoinByInvite(ctx, api, hash)
		if err != nil {
			return err
		}
		peer = InputPeer{ChannelID: channelID, AccessHash: accessHash}
		return nil
	})
	return peer, err
}

func (g *Gotd) LookupChannelTitle(ctx context.Context, peer InputPeer) (string, error) {
	var title string
	err := g.run(ctx, func(ctx context.Context, api *tg.Client) error {
		chats, err := api.ChannelsGetChannels(ctx, []tg.InputChannelClass{
			&tg.InputChannel{ChannelID: peer.ChannelID, AccessHash: peer.AccessHash},
		})
		if err != nil {
			return err
		}
		if cc, ok := chats.(*tg.MessagesChats); ok {
			for _, ch := range cc.Chats {
				if c, ok := ch.(*tg.Channel); ok && c.ID == peer.ChannelID {
					title = c.Title
					return nil
				}
			}
		}
		return nil
	})
	return title, err
}

func (g *Gotd) ListJoinRequests(ctx context.Context, peer InputPeer) ([]JoinRequest, error) {
	var out []JoinRequest
	err := g.run(ctx, func(ctx context.Context, api *tg.Client) error {
		rows, err := auth.ListJoinRequests(ctx, api, toPeer(peer))
		if err != nil {
			return err
		}
		out = make([]JoinRequest, 0, len(rows))
		for _, r := range rows {
			out = append(out, JoinRequest{
				UserID:      r.UserID,
				AccessHash:  r.AccessHash,
				DisplayName: r.DisplayName,
				Username:    r.Username,
				RequestedAt: r.RequestedAt,
				About:       r.About,
			})
		}
		return nil
	})
	return out, err
}

func (g *Gotd) HideJoinRequest(ctx context.Context, peer InputPeer, userID, accessHash int64, approved bool) error {
	return g.run(ctx, func(ctx context.Context, api *tg.Client) error {
		return auth.HideJoinRequest(ctx, api, toPeer(peer), userID, accessHash, approved)
	})
}

func (g *Gotd) ResolveDriveChannel(ctx context.Context, channelID int64) (InputPeer, error) {
	var peer InputPeer
	err := g.run(ctx, func(ctx context.Context, api *tg.Client) error {
		_, resolved, err := auth.ResolveDriveChannel(ctx, api, channelID)
		if err != nil {
			return err
		}
		peer = InputPeer{ChannelID: resolved.ChannelID, AccessHash: resolved.AccessHash}
		return nil
	})
	return peer, err
}

func (g *Gotd) LeaveChannel(ctx context.Context, peer InputPeer) error {
	return g.run(ctx, func(ctx context.Context, api *tg.Client) error {
		return auth.LeaveChannel(ctx, api, &tg.InputChannel{
			ChannelID:  peer.ChannelID,
			AccessHash: peer.AccessHash,
		})
	})
}

func toPeer(p InputPeer) *tg.InputPeerChannel {
	return &tg.InputPeerChannel{ChannelID: p.ChannelID, AccessHash: p.AccessHash}
}

func extractMsgID(updates tg.UpdatesClass) int64 {
	switch u := updates.(type) {
	case *tg.Updates:
		for _, update := range u.Updates {
			if msg, ok := update.(*tg.UpdateNewMessage); ok {
				if m, ok := msg.Message.(*tg.Message); ok {
					return int64(m.ID)
				}
			}
			if msg, ok := update.(*tg.UpdateNewChannelMessage); ok {
				if m, ok := msg.Message.(*tg.Message); ok {
					return int64(m.ID)
				}
			}
		}
	case *tg.UpdatesCombined:
		for _, update := range u.Updates {
			if msg, ok := update.(*tg.UpdateNewChannelMessage); ok {
				if m, ok := msg.Message.(*tg.Message); ok {
					return int64(m.ID)
				}
			}
		}
	}
	return 0
}

type progressReader struct {
	r          io.Reader
	total      int64
	sent       int64
	onProgress func(sent, total int64)
}

func (p *progressReader) Read(b []byte) (int, error) {
	n, err := p.r.Read(b)
	if n > 0 {
		p.sent += int64(n)
		if p.onProgress != nil {
			p.onProgress(p.sent, p.total)
		}
	}
	return n, err
}
