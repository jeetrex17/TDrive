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
	"golang.org/x/sync/singleflight"
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

	// mediaMu guards the per-data-center connections that carry file bytes:
	// CDN invokers and upload.getFile pools. They belong to the run scope and
	// are closed with it; poolRetryAt backs off a data center whose pool could
	// not be dialed, and poolFlights lets concurrent first reads share a dial.
	mediaMu     sync.Mutex
	cdn         map[int]telegram.CloseInvoker
	pools       map[int]telegram.CloseInvoker
	poolRetryAt map[int]time.Time
	poolFlights singleflight.Group
}

// NewGotd constructs a Client that dispatches onto one shared connection built
// from the given factory. In production wire this to auth.Connect.
func NewGotd(connect func() (*telegram.Client, error)) *Gotd {
	g := &Gotd{
		connect:     connect,
		cdn:         make(map[int]telegram.CloseInvoker),
		pools:       make(map[int]telegram.CloseInvoker),
		poolRetryAt: make(map[int]time.Time),
		writes:      newWriteCoordinator(time.Now, sleepContext),
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
		defer g.closeMediaConns()
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
	g.closeMediaConns()
	g.conn.Close()
}

// closeMediaConns drops every CDN invoker and getFile pool. They are dialed
// again on demand by the next run scope.
func (g *Gotd) closeMediaConns() {
	g.mediaMu.Lock()
	conns := make([]telegram.CloseInvoker, 0, len(g.cdn)+len(g.pools))
	for _, conn := range g.cdn {
		conns = append(conns, conn)
	}
	for _, pool := range g.pools {
		conns = append(conns, pool)
	}
	g.cdn = make(map[int]telegram.CloseInvoker)
	g.pools = make(map[int]telegram.CloseInvoker)
	g.mediaMu.Unlock()
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
	// the complete ~1.9 GiB TDrive segment. Parts travel over the home data
	// center's connection pool, several at a time.
	partClient := &retryingUploadClient{
		policy: DefaultWriteFloodWaitRetryPolicy(),
		run: func(ctx context.Context, action func(uploader.Client) error) error {
			return g.writes.Do(ctx, writeClassUploadPart, func() error {
				return g.runClient(ctx, func(ctx context.Context, client *telegram.Client) error {
					return action(g.uploadAPI(ctx, client))
				})
			})
		},
	}
	u := uploader.NewUploader(partClient).WithPartSize(uploader.MaximumPartSize).WithThreads(UploadThreads)
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

// placeholderMessage reports the id of a history entry that carries no
// projectable payload: a service event, or the stub Telegram substitutes for a
// deleted message. Only the id is meaningful, and it is what lets a caller keep
// paging backwards past a stretch of them.
func placeholderMessage(msg tg.MessageClass) (msgID int64, date int64, ok bool) {
	switch m := msg.(type) {
	case *tg.MessageService:
		return int64(m.ID), int64(m.Date), true
	case *tg.MessageEmpty:
		return int64(m.ID), 0, true
	default:
		return 0, 0, false
	}
}

// historyMessageFromTG converts one tg.MessageClass into the HistoryMessage
// shape GetHistory and GetChannelDifference both report. ok is false only for
// a message kind that carries no id worth reporting (see placeholderMessage).
func historyMessageFromTG(msg tg.MessageClass) (HistoryMessage, bool) {
	fullMsg, ok := msg.(*tg.Message)
	if !ok {
		// Service messages and the placeholders left behind by deletions hold
		// nothing to project, but they still consume message ids. Report them
		// so callers paginating on page size can tell a page thinned by
		// deletions from the end of the channel, and so backwards paging can
		// step over a run of them. A non-positive id cannot be paged from, so
		// reporting one would let a caller reset its cursor and walk the same
		// pages forever. Those are dropped as before.
		if id, date, ok := placeholderMessage(msg); ok && id > 0 {
			return HistoryMessage{MsgID: id, Date: date, Placeholder: true}, true
		}
		return HistoryMessage{}, false
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

	return HistoryMessage{
		MsgID:              int64(fullMsg.ID),
		Date:               int64(fullMsg.Date),
		FromID:             fromID,
		Text:               text,
		HasMedia:           hasMedia,
		MediaSize:          mediaSize,
		DocumentName:       documentName,
		DocumentAccessHash: documentAccessHash,
	}, true
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
			if hm, ok := historyMessageFromTG(msg); ok {
				out = append(out, hm)
			}
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
	err := g.runClient(ctx, func(ctx context.Context, client *telegram.Client) error {
		doc, name, err := getDocumentByMessageID(ctx, client.API(), peer, msgID)
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

		return g.newDownload(documentRefFromTG(peer, msgID, doc, name)).stream(ctx, dst)
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
	var retried int64
	err := g.runClient(ctx, func(ctx context.Context, client *telegram.Client) error {
		doc, name, err := getDocumentByMessageID(ctx, client.API(), peer, msgID)
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
		download := g.newDownload(documentRefFromTG(peer, msgID, doc, name))
		err = download.parallel(ctx, dst, threads)
		retried = download.retries.Load()
		if err != nil {
			return err
		}
		if onProgress != nil {
			onProgress(doc.Size, doc.Size)
		}
		return nil
	})
	if err != nil {
		slog.Error("tgclient: DownloadFileAt failed", "channel_id", peer.ChannelID, "msg_id", msgID, "block_retries", retried, "error", err)
	} else {
		slog.Debug("tgclient: DownloadFileAt completed", "channel_id", peer.ChannelID, "msg_id", msgID, "block_retries", retried)
	}
	return err
}

func (g *Gotd) DownloadFileThumbnail(ctx context.Context, peer InputPeer, msgID int64, thumbType string, w io.Writer) error {
	return g.runClient(ctx, func(ctx context.Context, client *telegram.Client) error {
		doc, _, err := getDocumentByMessageID(ctx, client.API(), peer, msgID)
		if err != nil {
			return err
		}
		release, err := AcquireBackgroundGetFileSlots(ctx, 1)
		if err != nil {
			return err
		}
		defer release()

		location := &tg.InputDocumentFileLocation{
			ID:            doc.ID,
			AccessHash:    doc.AccessHash,
			FileReference: doc.FileReference,
			ThumbSize:     thumbType,
		}
		return g.downloadVia(ctx, client, doc, func(api *tg.Client) error {
			if _, err := downloader.NewDownloader().Download(api, location).Stream(ctx, w); err != nil {
				return fmt.Errorf("tgclient: download thumbnail: %w", err)
			}
			return nil
		})
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

// GetChannelDifference returns one page of changes since pts. It is the
// gotd-backed half of the sync engine's incremental path: given a stored
// pts, ask Telegram what changed instead of rescanning history and probing
// every message's existence.
func (g *Gotd) GetChannelDifference(ctx context.Context, peer InputPeer, pts int64, limit int) (ChannelDifference, error) {
	var out ChannelDifference
	err := g.run(ctx, func(ctx context.Context, api *tg.Client) error {
		req := &tg.UpdatesGetChannelDifferenceRequest{
			Channel: &tg.InputChannel{ChannelID: peer.ChannelID, AccessHash: peer.AccessHash},
			Filter:  &tg.ChannelMessagesFilterEmpty{},
			Pts:     int(pts),
			Limit:   limit,
		}
		result, err := api.UpdatesGetChannelDifference(ctx, req)
		if err != nil {
			return err
		}
		switch diff := result.(type) {
		case *tg.UpdatesChannelDifferenceEmpty:
			out = ChannelDifference{Pts: int64(diff.Pts), Final: diff.Final}
		case *tg.UpdatesChannelDifference:
			out = channelDifferenceFromTG(diff)
		case *tg.UpdatesChannelDifferenceTooLong:
			out = ChannelDifference{Final: true, TooLong: true}
			if dialog, ok := diff.Dialog.(*tg.Dialog); ok {
				out.Pts = int64(dialog.Pts)
			}
		default:
			return fmt.Errorf("tgclient: updates.getChannelDifference: unexpected result type %T", result)
		}
		return nil
	})
	if err != nil {
		slog.Error("tgclient: UpdatesGetChannelDifference failed", "channel_id", peer.ChannelID, "pts", pts, "error", err)
		return ChannelDifference{}, fmt.Errorf("tgclient: updates.getChannelDifference: %w", err)
	}
	return out, nil
}

// channelDifferenceFromTG maps a non-empty, non-too-long channel difference.
// Edits are deliberately ignored: the sync engine's incremental path does not
// apply them today, only new messages and deletions.
func channelDifferenceFromTG(diff *tg.UpdatesChannelDifference) ChannelDifference {
	out := ChannelDifference{Pts: int64(diff.Pts), Final: diff.Final}
	for _, msg := range diff.NewMessages {
		if hm, ok := historyMessageFromTG(msg); ok {
			out.NewMessages = append(out.NewMessages, hm)
		}
	}
	for _, update := range diff.OtherUpdates {
		del, ok := update.(*tg.UpdateDeleteChannelMessages)
		if !ok {
			continue
		}
		for _, id := range del.Messages {
			out.DeletedIDs = append(out.DeletedIDs, int64(id))
		}
	}
	return out
}

// GetChannelPts returns the channel's current pts, used to bootstrap
// GetChannelDifference after a full history scan.
func (g *Gotd) GetChannelPts(ctx context.Context, peer InputPeer) (int64, error) {
	var pts int64
	err := g.run(ctx, func(ctx context.Context, api *tg.Client) error {
		full, err := api.ChannelsGetFullChannel(ctx, &tg.InputChannel{ChannelID: peer.ChannelID, AccessHash: peer.AccessHash})
		if err != nil {
			return err
		}
		channelFull, ok := full.FullChat.(*tg.ChannelFull)
		if !ok {
			return fmt.Errorf("tgclient: channels.getFullChannel: unexpected full chat type %T", full.FullChat)
		}
		pts = int64(channelFull.Pts)
		return nil
	})
	if err != nil {
		slog.Error("tgclient: ChannelsGetFullChannel failed", "channel_id", peer.ChannelID, "error", err)
		return 0, fmt.Errorf("tgclient: channels.getFullChannel: %w", err)
	}
	return pts, nil
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

// channelsGetMessagesLimit is how many ids one channels.getMessages accepts.
const channelsGetMessagesLimit = 100

// channelMessages fetches msgIDs from one channel in a single call. Telegram
// returns the messages it found in no particular order and substitutes an
// empty placeholder for deleted ones, so the result is keyed by id.
func channelMessages(ctx context.Context, api *tg.Client, peer InputPeer, msgIDs []int64) (map[int64]tg.MessageClass, error) {
	ids := make([]tg.InputMessageClass, 0, len(msgIDs))
	for _, id := range msgIDs {
		ids = append(ids, &tg.InputMessageID{ID: int(id)})
	}
	result, err := api.ChannelsGetMessages(ctx, &tg.ChannelsGetMessagesRequest{
		Channel: &tg.InputChannel{ChannelID: peer.ChannelID, AccessHash: peer.AccessHash},
		ID:      ids,
	})
	if err != nil {
		return nil, err
	}
	found := make(map[int64]tg.MessageClass, len(msgIDs))
	if messages, ok := result.(*tg.MessagesChannelMessages); ok {
		for _, msg := range messages.Messages {
			found[int64(msg.GetID())] = msg
		}
	}
	return found, nil
}

// documentOf extracts the document a channel message carries, plus its file
// name. Deleted messages arrive as empty placeholders, which read as missing.
func documentOf(msg tg.MessageClass) (*tg.Document, string, error) {
	full, ok := msg.(*tg.Message)
	if !ok {
		return nil, "", ErrMessageNotFound
	}
	docMedia, ok := full.Media.(*tg.MessageMediaDocument)
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

func getDocumentByMessageID(ctx context.Context, api *tg.Client, peer InputPeer, msgID int64) (*tg.Document, string, error) {
	messages, err := channelMessages(ctx, api, peer, []int64{msgID})
	if err != nil {
		return nil, "", err
	}
	msg, ok := messages[msgID]
	if !ok {
		return nil, "", ErrMessageNotFound
	}
	return documentOf(msg)
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
