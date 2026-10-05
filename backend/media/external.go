package media

import (
	"context"
	"errors"
	"fmt"
	"path"
	"strings"

	"TDrive/backend/tgclient"
)

var (
	ErrExternalRestricted = errors.New("media: Telegram content is restricted; open it in Telegram")
	ErrExternalReplaced   = errors.New("media: Telegram media changed during playback")
)

// ExternalMedia is a read-only Telegram document. It never enters the TDrive
// projection or inherits the active drive's encryption and mutation paths.
type ExternalMedia struct {
	Peer       tgclient.InputPeer
	Client     tgclient.Client
	Message    tgclient.HistoryMessage
	AccountID  int64
	Generation string
	Protected  bool
}

// ExternalName normalizes a Telegram document name for the existing player.
// A recognized MIME type supplies an extension when a post has no filename.
func ExternalName(message tgclient.HistoryMessage) string {
	name := path.Base(strings.ReplaceAll(strings.TrimSpace(message.DocumentName), `\`, `/`))
	if name != "" && name != "." && name != "/" && len(name) <= 255 && streamKindForName(name) != StreamKindUnknown {
		return name
	}
	ext := map[string]string{
		"video/mp4": ".mp4", "video/quicktime": ".mov", "video/webm": ".webm",
		"video/x-matroska": ".mkv", "audio/mpeg": ".mp3", "audio/mp4": ".m4a",
		"audio/aac": ".aac", "audio/ogg": ".ogg", "audio/flac": ".flac",
		"audio/wav": ".wav", "audio/x-wav": ".wav",
	}[strings.ToLower(message.MimeType)]
	if ext == "" {
		return ""
	}
	return fmt.Sprintf("Telegram media %d%s", message.MsgID, ext)
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
	file := LogicalFile{
		ChannelID: source.Peer.ChannelID, FileID: message.MsgID, Revision: 1,
		Name: name, StoredSize: ref.Size, PlaintextSize: ref.Size,
		Segments:   []Segment{{MsgID: message.MsgID, Size: ref.Size}},
		SourceKind: "channel", SourceAccountID: source.AccountID, SourceGeneration: source.Generation,
	}
	checked := &externalRangeClient{base: s.ranges, client: source.Client, peer: source.Peer, original: ref}
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
	base     tgclient.RangeClient
	client   tgclient.Client
	peer     tgclient.InputPeer
	original tgclient.DocumentRef
}

func (c *externalRangeClient) ResolveDocument(ctx context.Context, _ tgclient.InputPeer, msgID int64) (tgclient.DocumentRef, error) {
	if msgID != c.original.MsgID {
		return tgclient.DocumentRef{}, ErrExternalReplaced
	}
	// A failed lookup is returned as it is: a flood wait or a dropped
	// connection is the reader's to retry, and calling it a restriction
	// ended playback on a hiccup.
	channel, err := c.client.GetBroadcastChannel(ctx, c.peer)
	if errors.Is(err, tgclient.ErrChannelUnavailable) {
		return tgclient.DocumentRef{}, ErrExternalRestricted
	}
	if err != nil {
		return tgclient.DocumentRef{}, err
	}
	if channel.Protected || channel.Restricted {
		return tgclient.DocumentRef{}, ErrExternalRestricted
	}
	message, err := c.client.GetChannelMessage(ctx, c.peer, msgID)
	if err != nil {
		return tgclient.DocumentRef{}, err
	}
	if message.NoForwards || message.TTLSeconds > 0 || message.Paid || message.Restricted {
		return tgclient.DocumentRef{}, ErrExternalRestricted
	}
	if message.DocumentID != c.original.DocumentID || message.MediaSize != c.original.Size || message.DocumentAccessHash != c.original.AccessHash {
		return tgclient.DocumentRef{}, ErrExternalReplaced
	}
	ref, err := c.base.ResolveDocument(ctx, c.peer, msgID)
	if err != nil {
		return tgclient.DocumentRef{}, err
	}
	if ref.DocumentID != c.original.DocumentID || ref.Size != c.original.Size || ref.AccessHash != c.original.AccessHash {
		return tgclient.DocumentRef{}, ErrExternalReplaced
	}
	return ref, nil
}

func (c *externalRangeClient) ReadDocumentRange(ctx context.Context, ref tgclient.DocumentRef, offset int64, dst []byte) (int, error) {
	return c.base.ReadDocumentRange(ctx, ref, offset, dst)
}

// CloseExternalSessions revokes every URL for this account and channel. The
// returned tokens let the app close attached native players too.
func (s *Service) CloseExternalSessions(accountID, channelID int64) []string {
	return s.closeExternalSessions(func(file LogicalFile) bool {
		return file.SourceKind == "channel" && file.SourceAccountID == accountID && file.ChannelID == channelID
	})
}

func (s *Service) CloseAllExternalSessions() []string {
	return s.closeExternalSessions(func(file LogicalFile) bool { return file.SourceKind == "channel" })
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
