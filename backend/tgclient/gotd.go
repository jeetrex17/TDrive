package tgclient

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
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
	writes  *writeCoordinator

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
		writes:  newWriteCoordinator(time.Now, sleepContext),
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
	return g.SendControlWithRandomID(ctx, peer, text, silent, randomID())
}

func (g *Gotd) SendControlWithRandomID(ctx context.Context, peer InputPeer, text string, silent bool, sendRandomID int64) (int64, error) {
	if sendRandomID <= 0 {
		return 0, fmt.Errorf("tgclient: random id must be positive")
	}
	var msgID int64
	err := g.writes.Do(ctx, writeClassMessage, func() error {
		return g.run(ctx, func(ctx context.Context, api *tg.Client) error {
			req := &tg.MessagesSendMessageRequest{
				Peer:      toPeer(peer),
				Message:   text,
				RandomID:  sendRandomID,
				Silent:    silent,
				NoWebpage: true,
			}
			updates, err := api.MessagesSendMessage(ctx, req)
			if err != nil {
				return fmt.Errorf("%w: send message: %w", ErrSendOutcomeUnknown, err)
			}
			msgID, err = requiredSendMsgID(updates, sendRandomID, "send control")
			return err
		})
	})
	return msgID, err
}

func (g *Gotd) SendFile(ctx context.Context, peer InputPeer, r io.Reader, name, caption string, totalSize int64, onProgress func(sent, total int64)) (SendFileResult, error) {
	return g.SendFileWithRandomID(ctx, peer, r, name, caption, totalSize, onProgress, randomID())
}

func (g *Gotd) SendFileWithRandomID(ctx context.Context, peer InputPeer, r io.Reader, name, caption string, totalSize int64, onProgress func(sent, total int64), sendRandomID int64) (SendFileResult, error) {
	if sendRandomID <= 0 {
		return SendFileResult{}, fmt.Errorf("tgclient: random id must be positive")
	}
	// The uploader keeps each 512 KiB request buffer alive until its RPC
	// returns. Reacquiring inside retryingUploadClient therefore retries only
	// the failed Telegram part after a connection restart, rather than rereading
	// the complete ~1.9 GiB TDrive segment.
	partClient := &retryingUploadClient{
		policy: DefaultWriteFloodWaitRetryPolicy(),
		run: func(ctx context.Context, action func(uploader.Client) error) error {
			return g.writes.Do(ctx, writeClassUploadPart, func() error {
				return g.run(ctx, func(ctx context.Context, api *tg.Client) error {
					return action(api)
				})
			})
		},
	}
	u := uploader.NewUploader(partClient).WithPartSize(uploader.MaximumPartSize)
	var src io.Reader = r
	if onProgress != nil {
		src = &progressReader{
			r:          r,
			total:      totalSize,
			onProgress: onProgress,
		}
	}
	var uploadResult tg.InputFileClass
	var err error
	if totalSize > 0 {
		uploadResult, err = u.Upload(ctx, uploader.NewUpload(name, src, totalSize))
	} else {
		uploadResult, err = u.FromReader(ctx, name, src)
	}
	if err != nil {
		return SendFileResult{}, fmt.Errorf("tgclient: upload: %w", err)
	}

	var result SendFileResult
	err = g.writes.Do(ctx, writeClassMessage, func() error {
		return g.run(ctx, func(ctx context.Context, api *tg.Client) error {
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
				RandomID: sendRandomID,
				Message:  caption,
			}
			updates, err := api.MessagesSendMedia(ctx, req)
			if err != nil {
				// Uploading the document precedes MessagesSendMedia. A transport error
				// at this boundary can arrive after Telegram accepted the random_id,
				// so callers must reconcile with the same id before cleanup.
				return fmt.Errorf("%w: send media: %w", ErrSendOutcomeUnknown, err)
			}
			result.MsgID, err = requiredSendMsgID(updates, sendRandomID, "send file")
			return err
		})
	})
	return result, err
}

func (g *Gotd) GetHistory(ctx context.Context, peer InputPeer, minID, offsetID int64, limit int) ([]HistoryMessage, error) {
	if limit <= 0 {
		limit = 100
	}
	slog.Debug("tgclient: MessagesGetHistory", "channel_id", peer.ChannelID, "min_id", minID, "offset_id", offsetID, "limit", limit)
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
	if err != nil {
		slog.Error("tgclient: MessagesGetHistory failed", "channel_id", peer.ChannelID, "error", err)
	} else {
		slog.Debug("tgclient: MessagesGetHistory returned", "channel_id", peer.ChannelID, "messages", len(out))
	}
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
	if err != nil {
		slog.Error("tgclient: GetFileDocument failed", "channel_id", peer.ChannelID, "msg_id", msgID, "error", err)
	} else {
		slog.Debug("tgclient: GetFileDocument resolved", "channel_id", peer.ChannelID, "msg_id", msgID, "name", info.Name, "size", info.Size)
	}
	return info, err
}

func (g *Gotd) DownloadFile(ctx context.Context, peer InputPeer, msgID int64, w io.Writer, onProgress func(done, total int64)) error {
	slog.Debug("tgclient: DownloadFile starting", "channel_id", peer.ChannelID, "msg_id", msgID)
	err := g.run(ctx, func(ctx context.Context, api *tg.Client) error {
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
		release, err := AcquireBackgroundGetFileSlots(ctx, 1)
		if err != nil {
			return err
		}
		defer release()

		d := downloader.NewDownloader()
		if _, err := d.Download(api, doc.AsInputDocumentFileLocation()).Stream(ctx, dst); err != nil {
			return fmt.Errorf("tgclient: download: %w", err)
		}
		return nil
	})
	if err != nil {
		slog.Error("tgclient: DownloadFile failed", "channel_id", peer.ChannelID, "msg_id", msgID, "error", err)
	} else {
		slog.Debug("tgclient: DownloadFile completed", "channel_id", peer.ChannelID, "msg_id", msgID)
	}
	return err
}

func (g *Gotd) DownloadFileAt(ctx context.Context, peer InputPeer, msgID int64, w io.WriterAt, baseOffset int64, onProgress func(done, total int64)) error {
	slog.Debug("tgclient: DownloadFileAt starting", "channel_id", peer.ChannelID, "msg_id", msgID, "base_offset", baseOffset)
	err := g.run(ctx, func(ctx context.Context, api *tg.Client) error {
		doc, _, err := getDocumentByMessageID(ctx, api, peer, msgID)
		if err != nil {
			return err
		}

		threads := DefaultDownloadThreads
		release, err := AcquireBackgroundGetFileSlots(ctx, threads)
		if err != nil {
			return err
		}
		defer release()

		dst := io.WriterAt(offsetWriterAt{w: w, base: baseOffset})
		if onProgress != nil {
			dst = &progressWriterAt{
				w:          dst,
				total:      doc.Size,
				onProgress: onProgress,
			}
		}
		d := downloader.NewDownloader()
		if _, err := d.Download(api, doc.AsInputDocumentFileLocation()).WithThreads(threads).Parallel(ctx, dst); err != nil {
			return fmt.Errorf("tgclient: download: %w", err)
		}
		if onProgress != nil {
			onProgress(doc.Size, doc.Size)
		}
		return nil
	})
	if err != nil {
		slog.Error("tgclient: DownloadFileAt failed", "channel_id", peer.ChannelID, "msg_id", msgID, "error", err)
	} else {
		slog.Debug("tgclient: DownloadFileAt completed", "channel_id", peer.ChannelID, "msg_id", msgID)
	}
	return err
}

func (g *Gotd) DownloadFileThumbnail(ctx context.Context, peer InputPeer, msgID int64, thumbType string, w io.Writer) error {
	return g.run(ctx, func(ctx context.Context, api *tg.Client) error {
		doc, _, err := getDocumentByMessageID(ctx, api, peer, msgID)
		if err != nil {
			return err
		}
		release, err := AcquireBackgroundGetFileSlots(ctx, 1)
		if err != nil {
			return err
		}
		defer release()

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
	slog.Debug("tgclient: ChannelsDeleteMessages", "channel_id", peer.ChannelID, "count", len(msgIDs))
	err := g.writes.Do(ctx, writeClassMessage, func() error {
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
	})
	if err != nil {
		slog.Error("tgclient: ChannelsDeleteMessages failed", "channel_id", peer.ChannelID, "count", len(msgIDs), "error", err)
	}
	return err
}

// MissingMessages reports which of msgIDs no longer resolve to a real
// message in the channel. Telegram returns a MessageEmpty placeholder (or
// simply omits the id) for anything deleted, so presence is checked by
// scanning the response for a real *tg.Message/*tg.MessageService with a
// matching id rather than relying on response length or ordering.
func (g *Gotd) MissingMessages(ctx context.Context, peer InputPeer, msgIDs []int64) ([]int64, error) {
	if len(msgIDs) == 0 {
		return nil, nil
	}
	const chunk = 100
	var missing []int64
	err := g.run(ctx, func(ctx context.Context, api *tg.Client) error {
		for start := 0; start < len(msgIDs); start += chunk {
			end := min(start+chunk, len(msgIDs))
			batch := msgIDs[start:end]
			ids := make([]tg.InputMessageClass, 0, len(batch))
			for _, id := range batch {
				ids = append(ids, &tg.InputMessageID{ID: int(id)})
			}
			result, err := api.ChannelsGetMessages(ctx, &tg.ChannelsGetMessagesRequest{
				Channel: &tg.InputChannel{ChannelID: peer.ChannelID, AccessHash: peer.AccessHash},
				ID:      ids,
			})
			if err != nil {
				return err
			}
			found := make(map[int64]struct{}, len(batch))
			if mcm, ok := result.(*tg.MessagesChannelMessages); ok {
				for _, m := range mcm.Messages {
					switch msg := m.(type) {
					case *tg.Message:
						found[int64(msg.ID)] = struct{}{}
					case *tg.MessageService:
						found[int64(msg.ID)] = struct{}{}
					}
				}
			}
			for _, id := range batch {
				if _, ok := found[id]; !ok {
					missing = append(missing, id)
				}
			}
		}
		return nil
	})
	if err != nil {
		slog.Error("tgclient: ChannelsGetMessages failed", "channel_id", peer.ChannelID, "count", len(msgIDs), "error", err)
		return nil, err
	}
	return missing, nil
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

func extractMsgID(updates tg.UpdatesClass, randomID int64) int64 {
	switch u := updates.(type) {
	case *tg.UpdateShortSentMessage:
		return int64(u.ID)
	case *tg.Updates:
		return extractMsgIDFromUpdates(u.Updates, randomID)
	case *tg.UpdatesCombined:
		return extractMsgIDFromUpdates(u.Updates, randomID)
	}
	return 0
}

func requiredSendMsgID(updates tg.UpdatesClass, randomID int64, operation string) (int64, error) {
	msgID := extractMsgID(updates, randomID)
	if msgID <= 0 {
		return 0, fmt.Errorf("%w: %s returned no msg id", ErrSendOutcomeUnknown, operation)
	}
	return msgID, nil
}

func extractMsgIDFromUpdates(updates []tg.UpdateClass, randomID int64) int64 {
	for _, update := range updates {
		switch value := update.(type) {
		case *tg.UpdateMessageID:
			if value.RandomID == randomID {
				return int64(value.ID)
			}
		case *tg.UpdateNewMessage:
			if message, ok := value.Message.(*tg.Message); ok {
				return int64(message.ID)
			}
		case *tg.UpdateNewChannelMessage:
			if message, ok := value.Message.(*tg.Message); ok {
				return int64(message.ID)
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

type offsetWriterAt struct {
	w    io.WriterAt
	base int64
}

func (o offsetWriterAt) WriteAt(b []byte, off int64) (int, error) {
	return o.w.WriteAt(b, o.base+off)
}

type progressWriterAt struct {
	w          io.WriterAt
	total      int64
	done       atomic.Int64
	lastEmit   atomic.Int64
	onProgress func(done, total int64)
}

func (p *progressWriterAt) WriteAt(b []byte, off int64) (int, error) {
	n, err := p.w.WriteAt(b, off)
	if n > 0 && p.onProgress != nil {
		done := p.done.Add(int64(n))
		now := time.Now().UnixNano()
		last := p.lastEmit.Load()
		if now-last >= int64(100*time.Millisecond) && p.lastEmit.CompareAndSwap(last, now) {
			p.onProgress(done, p.total)
		}
	}
	return n, err
}
