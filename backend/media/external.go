package media

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"TDrive/backend/tgclient"
)

var (
	ErrExternalRestricted = errors.New("media: Telegram content is restricted; open it in Telegram")
	ErrExternalReplaced   = errors.New("media: Telegram media changed during playback")
)

// Rechecking each 1 MiB block would multiply Telegram RPCs during playback.
// A valid file reference can outlive a permission change, so cap how long
// uncached reads may rely on the open check.
const externalAccessCheckInterval = 30 * time.Second

// ExternalMedia is a read-only Telegram document. It never enters the TDrive
// projection or inherits the active drive's encryption and mutation paths.
type ExternalMedia struct {
	Peer        tgclient.InputPeer
	Client      tgclient.Client
	Message     tgclient.HistoryMessage
	AccountID   int64
	Generation  string
	Protected   bool
	ProbeAccess bool
	Validate    func(context.Context) error
}

// ExternalName normalizes a Telegram document name for the existing player.
// A recognized MIME type supplies an extension when a post has no filename.
func ExternalName(message tgclient.HistoryMessage) string {
	name := tgclient.MediaName(message)
	if streamKindForName(name) != StreamKindUnknown {
		return name
	}
	// Connected Telegram sources historically infer playable MIME even when a
	// sender supplied an unrelated extension. Drive listings preserve that name.
	message.DocumentName = ""
	name = tgclient.MediaName(message)
	if streamKindForName(name) == StreamKindUnknown {
		return ""
	}
	return name
}

func ExternalKind(message tgclient.HistoryMessage) StreamKind {
	if message.Paid || message.TTLSeconds > 0 || message.DocumentID == 0 || message.MediaSize <= 0 {
		return StreamKindUnknown
	}
	name := ExternalName(message)
	kind := streamKindForName(name)
	if kind != StreamKindVideo && kind != StreamKindAudio {
		return StreamKindUnknown
	}
	return kind
}

// OpenExternal publishes a normal tokenized session only after the caller's
// source-generation gate authorizes the final Add. The gate is mandatory.
func (s *Service) OpenExternal(ctx context.Context, source ExternalMedia, publish func(func() error) error) (OpenResult, error) {
	if s == nil || s.server == nil || s.ranges == nil || publish == nil {
		return OpenResult{}, ErrRangeClientNotReady
	}
	message := source.Message
	if source.Client == nil || source.AccountID <= 0 || source.Generation == "" || source.Peer.ChannelID <= 0 || message.MsgID <= 0 {
		return OpenResult{}, ErrExternalRestricted
	}
	if source.Protected || message.NoForwards || message.TTLSeconds > 0 || message.Paid || message.Restricted {
		return OpenResult{}, ErrExternalRestricted
	}
	name := ExternalName(message)
	kind := ExternalKind(message)
	if name == "" || kind == StreamKindUnknown {
		return OpenResult{}, ErrUnsupportedMediaType
	}
	ref, err := s.ranges.ResolveDocument(ctx, source.Peer, message.MsgID)
	if err != nil {
		return OpenResult{}, fmt.Errorf("media: resolve Telegram document: %w", err)
	}
	if ref.DocumentID != message.DocumentID || ref.Size != message.MediaSize || ref.AccessHash != message.DocumentAccessHash {
		return OpenResult{}, ErrExternalReplaced
	}
	if source.ProbeAccess {
		// A resolved public handle can identify a channel whose history or
		// document bytes Telegram withholds from nonmembers. Fail at open.
		var first [1]byte
		if _, err := s.ranges.ReadDocumentRange(ctx, ref, 0, first[:]); err != nil {
			return OpenResult{}, fmt.Errorf("media: public channel bytes unavailable: %w", err)
		}
	}
	sourceKind := "channel"
	if source.Peer.Kind != "" {
		sourceKind = "telegram"
	}
	file := LogicalFile{
		ChannelID: source.Peer.ChannelID, FileID: message.MsgID, Revision: 1,
		Name: name, StoredSize: ref.Size, PlaintextSize: ref.Size,
		Segments:   []Segment{{MsgID: message.MsgID, Size: ref.Size}},
		SourceKind: sourceKind, SourcePeerKind: string(source.Peer.PeerKind()),
		SourceAccountID: source.AccountID, SourceGeneration: source.Generation,
	}
	checked := &externalRangeClient{base: s.ranges, client: source.Client, peer: source.Peer, original: ref, validate: source.Validate}
	checked.lastRemoteCheck.Store(time.Now().UnixNano())
	session, err := newSession(file, []resolvedSegment{{size: ref.Size, ref: ref}}, checked, s.thumbs, s.thumbGen, SessionOptions{
		Context: ctx, EnableVideoThumbnails: kind == StreamKindVideo,
	})
	if err != nil {
		return OpenResult{}, err
	}
	if err := publish(func() error { return s.server.Add(session) }); err != nil {
		session.Close()
		return OpenResult{}, err
	}
	return OpenResult{
		Token: session.Token(), URL: session.URL(), ThumbnailURL: session.ThumbnailURL(),
		HLSURL: session.HLSURL(), Name: name, Kind: kind, MimeType: session.MimeType(),
		SupportsRange: true, Info: file,
	}, nil
}

type externalRangeClient struct {
	base            tgclient.RangeClient
	client          RawDriveSource
	peer            tgclient.InputPeer
	original        tgclient.DocumentRef
	validate        func(context.Context) error
	lastRemoteCheck atomic.Int64
	remoteCheckOnce sync.Once
	remoteCheckSlot chan struct{}
}

func (c *externalRangeClient) ResolveDocument(ctx context.Context, _ tgclient.InputPeer, msgID int64) (tgclient.DocumentRef, error) {
	if msgID != c.original.MsgID {
		return tgclient.DocumentRef{}, ErrExternalReplaced
	}
	if c.validate != nil {
		if err := c.validate(ctx); err != nil {
			return tgclient.DocumentRef{}, err
		}
	}
	if err := c.checkRemote(ctx); err != nil {
		return tgclient.DocumentRef{}, err
	}
	ref, err := c.base.ResolveDocument(ctx, c.peer, msgID)
	if err != nil {
		return tgclient.DocumentRef{}, err
	}
	if ref.MsgID != c.original.MsgID || ref.Peer.ChannelID != c.peer.ChannelID || ref.Peer.PeerKind() != c.peer.PeerKind() || ref.DocumentID != c.original.DocumentID || ref.Size != c.original.Size || ref.AccessHash != c.original.AccessHash || ref.PhotoSizeType != c.original.PhotoSizeType {
		return tgclient.DocumentRef{}, ErrExternalReplaced
	}
	return ref, nil
}

// checkRemote is also called on a still-valid file reference after the
// interval, so permission changes do not wait for FILE_REFERENCE_EXPIRED.
func (c *externalRangeClient) checkRemote(ctx context.Context) error {
	// A failed lookup is returned as it is: a flood wait or a dropped
	// connection is the reader's to retry, not a false restriction.
	channel, err := c.client.GetMediaSourcePeer(ctx, c.peer)
	if errors.Is(err, tgclient.ErrChannelUnavailable) {
		return ErrExternalRestricted
	}
	if err != nil {
		return err
	}
	if channel.ID != c.peer.ChannelID || channel.Kind != c.peer.PeerKind() || channel.Protected || channel.Restricted {
		return ErrExternalRestricted
	}
	message, err := c.client.GetChannelMessage(ctx, c.peer, c.original.MsgID)
	if err != nil {
		return err
	}
	if message.NoForwards || message.TTLSeconds > 0 || message.Paid || message.Restricted {
		return ErrExternalRestricted
	}
	if message.MsgID != c.original.MsgID || (message.ChannelID != 0 && message.ChannelID != c.peer.ChannelID) || (message.PeerKind != "" && message.PeerKind != c.peer.PeerKind()) || message.DocumentID != c.original.DocumentID || message.MediaSize != c.original.Size || message.DocumentAccessHash != c.original.AccessHash {
		return ErrExternalReplaced
	}
	c.lastRemoteCheck.Store(time.Now().UnixNano())
	return nil
}

func (c *externalRangeClient) ReadDocumentRange(ctx context.Context, ref tgclient.DocumentRef, offset int64, dst []byte) (int, error) {
	if c.validate != nil {
		if err := c.validate(ctx); err != nil {
			return 0, err
		}
	}
	if time.Since(time.Unix(0, c.lastRemoteCheck.Load())) >= externalAccessCheckInterval {
		// A canceled seek must not share its cancellation with a foreground
		// read. Waiters retry the check with their own context if it fails.
		c.remoteCheckOnce.Do(func() {
			c.remoteCheckSlot = make(chan struct{}, 1)
			c.remoteCheckSlot <- struct{}{}
		})
		select {
		case <-ctx.Done():
			return 0, ctx.Err()
		case <-c.remoteCheckSlot:
		}
		err := func() error {
			defer func() { c.remoteCheckSlot <- struct{}{} }()
			if err := ctx.Err(); err != nil {
				return err
			}
			if time.Since(time.Unix(0, c.lastRemoteCheck.Load())) >= externalAccessCheckInterval {
				return c.checkRemote(ctx)
			}
			return nil
		}()
		if err != nil {
			return 0, err
		}
	}
	return c.base.ReadDocumentRange(ctx, ref, offset, dst)
}

// CloseExternalSessions revokes every URL for this account and channel. The
// returned tokens let the app close attached native players too.
func (s *Service) CloseExternalSessions(accountID, channelID int64) []string {
	return s.closeExternalSessions(func(file LogicalFile) bool {
		return file.SourceKind == "channel" && file.SourceAccountID == accountID && file.ChannelID == channelID
	})
}

func (s *Service) CloseSourceSessions(accountID int64, kind string, peerID int64) []string {
	return s.closeExternalSessions(func(file LogicalFile) bool {
		return file.SourceKind != "" && file.SourceAccountID == accountID &&
			(file.SourcePeerKind == kind || (kind == string(tgclient.PeerChannel) && file.SourcePeerKind == "")) &&
			file.ChannelID == peerID
	})
}

func (s *Service) CloseAllExternalSessions() []string {
	return s.closeExternalSessions(func(file LogicalFile) bool { return file.SourceKind != "" })
}

func (s *Service) closeExternalSessions(match func(LogicalFile) bool) []string {
	if s == nil || s.server == nil {
		return nil
	}
	s.server.mu.Lock()
	var tokens []string
	var sessions []*Session
	for token, session := range s.server.sessions {
		file := session.file
		if match(file) {
			delete(s.server.sessions, token)
			tokens = append(tokens, token)
			sessions = append(sessions, session)
		}
	}
	s.server.mu.Unlock()
	for _, session := range sessions {
		session.Close()
	}
	return tokens
}
