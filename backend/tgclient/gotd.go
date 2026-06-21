package tgclient

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"math/rand"
	"strings"
	"sync"
	"time"

	"TDrive/backend/auth"

	"github.com/gotd/td/telegram"
	"github.com/gotd/td/telegram/downloader"
	"github.com/gotd/td/telegram/uploader"
	"github.com/gotd/td/tg"
)

// Gotd is the production Client implementation. It keeps one long-lived gotd
// Run scope (a single authenticated connection) and dispatches every call onto
// it, instead of dialing a fresh connection per call. The scope starts lazily
// on first use and is torn down by Close.
type Gotd struct {
	connect func() (*telegram.Client, error)
	conn    *liveConn

	mu     sync.Mutex
	client *telegram.Client

	cdnMu sync.Mutex
	cdn   map[int]telegram.CloseInvoker
}

// NewGotd constructs a Client that dispatches onto one shared connection built
// from the given factory. In production wire this to auth.Connect.
func NewGotd(connect func() (*telegram.Client, error)) *Gotd {
	g := &Gotd{
		connect: connect,
		cdn:     make(map[int]telegram.CloseInvoker),
	}
	g.conn = newLiveConn(g.scope)
	return g
}

// scope is the liveConn scopeFn: connect, publish the client, signal ready,
// then block until the connection's context is cancelled (Close or a dropped
// link). The connection stays usable for concurrent API calls while blocked.
func (g *Gotd) scope(runCtx context.Context, ready func()) error {
	client, err := g.connect()
	if err != nil {
		return fmt.Errorf("tgclient: connect: %w", err)
	}
	return client.Run(runCtx, func(rctx context.Context) error {
		defer g.closeCDN()
		g.mu.Lock()
		g.client = client
		g.mu.Unlock()
		ready()
		<-rctx.Done()
		return rctx.Err()
	})
}

// acquire blocks until the shared connection is ready, then returns the live
// client. The per-call ctx bounds the wait and the API calls the caller makes;
// the connection itself lives under the liveConn's own lifetime, not ctx.
func (g *Gotd) acquire(ctx context.Context) (*telegram.Client, error) {
	if err := g.conn.acquire(ctx); err != nil {
		return nil, err
	}
	g.mu.Lock()
	client := g.client
	g.mu.Unlock()
	if client == nil {
		return nil, fmt.Errorf("tgclient: client unavailable after connect")
	}
	return client, nil
}

func (g *Gotd) run(ctx context.Context, fn func(ctx context.Context, api *tg.Client) error) error {
	client, err := g.acquire(ctx)
	if err != nil {
		return normalizeError(err)
	}
	return normalizeError(fn(ctx, client.API()))
}

func (g *Gotd) runClient(ctx context.Context, fn func(ctx context.Context, client *telegram.Client) error) error {
	client, err := g.acquire(ctx)
	if err != nil {
		return normalizeError(err)
	}
	return normalizeError(fn(ctx, client))
}

// Close tears down the shared connection. Safe to call once at shutdown.
func (g *Gotd) Close() {
	g.closeCDN()
	g.conn.Close()
}

func (g *Gotd) closeCDN() {
	g.cdnMu.Lock()
	conns := g.cdn
	g.cdn = make(map[int]telegram.CloseInvoker)
	g.cdnMu.Unlock()
	for _, conn := range conns {
		_ = conn.Close()
	}
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

func (g *Gotd) SelfProfile(ctx context.Context) (UserProfile, error) {
	var out UserProfile
	err := g.run(ctx, func(ctx context.Context, api *tg.Client) error {
		me, err := api.UsersGetUsers(ctx, []tg.InputUserClass{&tg.InputUserSelf{}})
		if err != nil {
			return err
		}
		var u *tg.User
		for _, raw := range me {
			if user, ok := raw.(*tg.User); ok && user.ID != 0 {
				u = user
				break
			}
		}
		if u == nil {
			return fmt.Errorf("tgclient: self user not found")
		}

		out = UserProfile{
			ID:        u.ID,
			FirstName: u.FirstName,
			LastName:  u.LastName,
			Username:  u.Username,
			Premium:   u.Premium,
		}

		photo, ok := u.Photo.(*tg.UserProfilePhoto)
		if !ok {
			return nil
		}
		dlCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		var buf bytes.Buffer
		loc := &tg.InputPeerPhotoFileLocation{
			Big:     false,
			Peer:    &tg.InputPeerSelf{},
			PhotoID: photo.PhotoID,
		}
		if _, err := downloader.NewDownloader().Download(api, loc).Stream(dlCtx, &buf); err != nil {
			fmt.Printf("self photo download failed: %v\n", err)
			return nil
		}
		out.PhotoBytes = append([]byte(nil), buf.Bytes()...)
		return nil
	})
	return out, err
}

func (g *Gotd) ResolveUsersFromMessages(ctx context.Context, peer InputPeer, refs []UserMessageRef) ([]UserProfile, error) {
	var out []UserProfile
	err := g.run(ctx, func(ctx context.Context, api *tg.Client) error {
		const batchSize = 100
		inputs := make([]tg.InputUserClass, 0, len(refs))
		for _, ref := range refs {
			if ref.UserID <= 0 || ref.MsgID <= 0 {
				continue
			}
			inputs = append(inputs, &tg.InputUserFromMessage{
				Peer:   toPeer(peer),
				MsgID:  int(ref.MsgID),
				UserID: ref.UserID,
			})
		}
		for i := 0; i < len(inputs); i += batchSize {
			end := i + batchSize
			if end > len(inputs) {
				end = len(inputs)
			}
			resolved, err := api.UsersGetUsers(ctx, inputs[i:end])
			if err != nil {
				return err
			}
			for _, raw := range resolved {
				user, ok := raw.(*tg.User)
				if !ok || user.ID == 0 {
					continue
				}
				out = append(out, UserProfile{
					ID:        user.ID,
					FirstName: user.FirstName,
					LastName:  user.LastName,
					Username:  user.Username,
				})
			}
		}
		return nil
	})
	return out, err
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
				hasMedia           bool
				mediaSize          int64
				documentName       string
				documentAccessHash int64
			)
			if media, ok := fullMsg.Media.(*tg.MessageMediaDocument); ok {
				hasMedia = true
				if doc, ok := media.Document.(*tg.Document); ok {
					mediaSize = doc.Size
					documentAccessHash = doc.AccessHash
					for _, attr := range doc.Attributes {
						if fname, ok := attr.(*tg.DocumentAttributeFilename); ok {
							documentName = fname.FileName
							break
						}
					}
				}
			}

			out = append(out, HistoryMessage{
				MsgID:              int64(fullMsg.ID),
				Date:               int64(fullMsg.Date),
				FromID:             fromID,
				Text:               text,
				HasMedia:           hasMedia,
				MediaSize:          mediaSize,
				DocumentName:       documentName,
				DocumentAccessHash: documentAccessHash,
			})
		}
		return nil
	})
	return out, err
}

func (g *Gotd) GetFileDocument(ctx context.Context, peer InputPeer, msgID int64) (FileDocument, error) {
	var info FileDocument
	err := g.run(ctx, func(ctx context.Context, api *tg.Client) error {
		doc, name, err := getDocumentByMessageID(ctx, api, peer, msgID)
		if err != nil {
			return err
		}
		info = FileDocument{
			MsgID:  msgID,
			Name:   name,
			Size:   doc.Size,
			Thumbs: fileThumbsFromDocument(doc),
		}
		return nil
	})
	return info, err
}

func (g *Gotd) DownloadFile(ctx context.Context, peer InputPeer, msgID int64, w io.Writer, onProgress func(done, total int64)) error {
	return g.run(ctx, func(ctx context.Context, api *tg.Client) error {
		doc, _, err := getDocumentByMessageID(ctx, api, peer, msgID)
		if err != nil {
			return err
		}
		var dst io.Writer = w
		if onProgress != nil {
			dst = &progressWriter{
				w:          w,
				total:      doc.Size,
				onProgress: onProgress,
			}
		}
		d := downloader.NewDownloader()
		if _, err := d.Download(api, doc.AsInputDocumentFileLocation()).Stream(ctx, dst); err != nil {
			return fmt.Errorf("tgclient: download: %w", err)
		}
		return nil
	})
}

func (g *Gotd) DownloadFileThumbnail(ctx context.Context, peer InputPeer, msgID int64, thumbType string, w io.Writer) error {
	return g.run(ctx, func(ctx context.Context, api *tg.Client) error {
		doc, _, err := getDocumentByMessageID(ctx, api, peer, msgID)
		if err != nil {
			return err
		}
		d := downloader.NewDownloader()
		location := &tg.InputDocumentFileLocation{
			ID:            doc.ID,
			AccessHash:    doc.AccessHash,
			FileReference: doc.FileReference,
			ThumbSize:     thumbType,
		}
		if _, err := d.Download(api, location).Stream(ctx, w); err != nil {
			return fmt.Errorf("tgclient: download thumbnail: %w", err)
		}
		return nil
	})
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

func getDocumentByMessageID(ctx context.Context, api *tg.Client, peer InputPeer, msgID int64) (*tg.Document, string, error) {
	messageResult, err := api.ChannelsGetMessages(ctx, &tg.ChannelsGetMessagesRequest{
		Channel: &tg.InputChannel{ChannelID: peer.ChannelID, AccessHash: peer.AccessHash},
		ID:      []tg.InputMessageClass{&tg.InputMessageID{ID: int(msgID)}},
	})
	if err != nil {
		return nil, "", err
	}

	var targetMsg *tg.Message
	switch m := messageResult.(type) {
	case *tg.MessagesChannelMessages:
		if len(m.Messages) > 0 {
			targetMsg, _ = m.Messages[0].(*tg.Message)
		}
	}
	if targetMsg == nil {
		return nil, "", ErrMessageNotFound
	}

	docMedia, ok := targetMsg.Media.(*tg.MessageMediaDocument)
	if !ok {
		return nil, "", ErrNotFile
	}
	doc, ok := docMedia.Document.(*tg.Document)
	if !ok {
		return nil, "", ErrEmptyDocument
	}

	name := "tdrive_download"
	for _, attr := range doc.Attributes {
		if fname, ok := attr.(*tg.DocumentAttributeFilename); ok {
			name = fname.FileName
			break
		}
	}
	return doc, name, nil
}

func fileThumbsFromDocument(doc *tg.Document) []FileThumb {
	if doc == nil {
		return nil
	}
	out := make([]FileThumb, 0, len(doc.Thumbs))
	for _, size := range doc.Thumbs {
		thumb := FileThumb{Type: strings.TrimSpace(size.GetType())}
		switch t := size.(type) {
		case *tg.PhotoCachedSize:
			thumb.Bytes = append([]byte(nil), t.Bytes...)
			thumb.Width = t.W
			thumb.Height = t.H
			if len(t.Bytes) > 0 {
				thumb.Size = len(t.Bytes)
			}
		case *tg.PhotoSize:
			thumb.Width = t.W
			thumb.Height = t.H
			thumb.Size = t.Size
		case *tg.PhotoSizeProgressive:
			thumb.Width = t.W
			thumb.Height = t.H
			if n := len(t.Sizes); n > 0 {
				thumb.Size = t.Sizes[n-1]
			}
		}
		if thumb.Type != "" || len(thumb.Bytes) > 0 {
			out = append(out, thumb)
		}
	}
	return out
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

type progressWriter struct {
	w          io.Writer
	total      int64
	done       int64
	onProgress func(done, total int64)
}

func (p *progressWriter) Write(b []byte) (int, error) {
	n, err := p.w.Write(b)
	if n > 0 {
		p.done += int64(n)
		if p.onProgress != nil {
			p.onProgress(p.done, p.total)
		}
	}
	return n, err
}
