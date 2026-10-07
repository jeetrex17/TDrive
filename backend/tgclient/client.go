// Package tgclient is a small adapter over Telegram. It exists so the rest
// of the app — sync, backfill, internal/app local actions — can be written and
// tested without depending on the full gotd surface.
//
// The interface stays minimal on purpose. Add a method only when a real
// caller needs it.
package tgclient

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"time"
)

var ErrFloodWait = errors.New("tgclient: flood wait")

var (
	ErrMessageNotFound = errors.New("tgclient: message not found")
	ErrNotFile         = errors.New("tgclient: message is not a file")
	ErrEmptyDocument   = errors.New("tgclient: empty document")
	// ErrChannelUnavailable means the account can no longer access a peer,
	// as opposed to a lookup that merely failed this time.
	ErrChannelUnavailable = errors.New("tgclient: peer unavailable to this account")
	// ErrSendOutcomeUnknown means Telegram may have accepted an idempotent
	// write even though the client did not receive a usable receipt. Callers
	// must retry with the same random_id before abandoning remote artifacts.
	ErrSendOutcomeUnknown = errors.New("tgclient: send outcome unknown")
)

type FloodWaitError struct {
	Duration time.Duration
}

func (e FloodWaitError) Error() string {
	if e.Duration <= 0 {
		return ErrFloodWait.Error()
	}
	return fmt.Sprintf("%s: %s", ErrFloodWait, e.Duration)
}

func (e FloodWaitError) Is(target error) bool {
	return target == ErrFloodWait
}

func NewFloodWaitError(duration time.Duration) error {
	return FloodWaitError{Duration: duration}
}

func FloodWaitDuration(err error) (time.Duration, bool) {
	var floodErr FloodWaitError
	if errors.As(err, &floodErr) {
		return floodErr.Duration, true
	}
	if errors.Is(err, ErrFloodWait) {
		return 2 * time.Second, true
	}
	return 0, false
}

// InputPeer is the channel handle TDrive needs to send / read from. Real impl
// resolves it via auth.ResolveDriveChannel; fake stores it as-is.
type InputPeer struct {
	ChannelID  int64
	AccessHash int64
	// Kind is empty for the historical channel-only drive path.
	Kind PeerKind
}

type PeerKind string

const (
	PeerChannel    PeerKind = "channel"
	PeerSupergroup PeerKind = "supergroup"
	PeerGroup      PeerKind = "group"
	PeerUser       PeerKind = "user"
	PeerBot        PeerKind = "bot"
	PeerSelf       PeerKind = "self"
)

func (p InputPeer) PeerKind() PeerKind {
	if p.Kind == "" {
		return PeerChannel
	}
	return p.Kind
}

func (k PeerKind) Valid() bool {
	switch k {
	case PeerChannel, PeerSupergroup, PeerGroup, PeerUser, PeerBot, PeerSelf:
		return true
	default:
		return false
	}
}

// HistoryMessage is the subset of a tg.Message that sync/backfill/read paths care about.
// We deliberately avoid leaking the gotd types so the fake stays cheap.
type HistoryMessage struct {
	PeerKind           PeerKind // empty for channel-only drive history
	ChannelID          int64    // optional in fakes; production history is scoped by the request peer
	MsgID              int64
	Date               int64
	FromID             int64
	Outgoing           bool   // Telegram marks messages sent by the current account.
	Text               string // caption for media messages, body for text messages
	HasMedia           bool
	MediaSize          int64
	DocumentName       string
	DocumentAccessHash int64
	DocumentID         int64
	MimeType           string
	Duration           float64 // seconds, from the video or audio attribute; 0 when unknown
	NoForwards         bool
	TTLSeconds         int
	// Paid marks paid media. It holds no document this account can read, so
	// HasMedia stays false and drive sync never adopts it as a file.
	Paid       bool
	Restricted bool // Telegram withholds the post on this platform
	Thumbs     []FileThumb
	// Placeholder marks an entry that occupies a message id but carries no
	// content: a service event, or the stub left where a message was deleted.
	// It is reported so page lengths match what Telegram sent, and callers
	// looking for real content must skip it.
	Placeholder bool
}

// ChannelDifference is one page of changes since a stored pts.
type ChannelDifference struct {
	Pts         int64            // pts to store after applying this page
	Final       bool             // false: call again with Pts to get more
	TooLong     bool             // Telegram refused a diff; caller must rescan history, then store Pts
	NewMessages []HistoryMessage // new messages, same shape GetHistory returns
	DeletedIDs  []int64          // message ids deleted in the channel
}

// SendFileResult is what SendFile returns. We split it from a bare msgID
// because the upload progress callback is configurable and we may want to
// extend with more fields (e.g. document size confirmation).
type SendFileResult struct {
	MsgID int64
}

type FileDocument struct {
	MsgID  int64
	Name   string
	Size   int64
	Thumbs []FileThumb
}

type FileThumb struct {
	Type   string
	Bytes  []byte
	Width  int
	Height int
	Size   int
}

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

type UserProfile struct {
	ID         int64
	FirstName  string
	LastName   string
	Username   string
	Premium    bool
	PhotoBytes []byte
}

type UserMessageRef struct {
	UserID int64
	MsgID  int64
}

// OwnedBroadcastChannel is the minimum metadata needed to let a user recover
// a personal drive. AccessHash is intentionally kept behind the backend API;
// frontend and daemon DTOs must expose only the stable channel ID.
type OwnedBroadcastChannel struct {
	ID          int64
	AccessHash  int64
	Title       string
	CreatedAt   int64
	HasActivity bool
}

// JoinedBroadcastChannel is a channel visible to this account, including
// archived dialogs. AccessHash stays entirely behind the backend boundary.
type JoinedBroadcastChannel struct {
	Kind       PeerKind
	ID         int64
	AccessHash int64
	Title      string
	Username   string
	Protected  bool
	Restricted bool  // Telegram withholds the channel on this platform
	PhotoID    int64 // the current profile photo; 0 when the channel has none
	Truncated  bool  // dialog picker reached its bounded scan budget
}

// SourcePeer is metadata for a readable dialog or public broadcast channel.
// AccessHash is never exposed through the Wails binding.
type SourcePeer = JoinedBroadcastChannel

// Client is the surface sync, backfill, and local-action paths use to talk
// to Telegram. Both the real (gotd-backed) and fake test implementations
// implement this.
type Client interface {
	// SelfID returns the logged-in user's Telegram user ID. Used by
	// ProjectFromOp's actorID field.
	SelfID(ctx context.Context) (int64, error)

	// SelfProfile returns the logged-in user's display fields and, when
	// available, a small avatar photo. Photo fetch is best-effort.
	SelfProfile(ctx context.Context) (UserProfile, error)

	// ResolveUsersFromMessages resolves channel members through message refs.
	// Telegram requires InputUserFromMessage for users not already in contacts.
	ResolveUsersFromMessages(ctx context.Context, peer InputPeer, refs []UserMessageRef) ([]UserProfile, error)

	// SendControl posts a text-only message into the channel. The text is
	// expected to be a TDX1 header (possibly with a human-readable comment
	// after a newline). Returns the new msgID.
	SendControl(ctx context.Context, peer InputPeer, text string, silent bool) (msgID int64, err error)

	// SendFile uploads and sends a document with the given caption. The
	// caption is expected to start with a TDX1|t=f|... header. onProgress is
	// optional; pass nil to skip progress callbacks.
	SendFile(ctx context.Context, peer InputPeer, r io.Reader, name, caption string, totalSize int64, onProgress func(sent, total int64)) (SendFileResult, error)

	// GetHistory returns up to limit messages from the channel in Telegram's
	// default history order: newest first. minID filters out messages at or
	// below that watermark; offsetID pages older than a previous page.
	// Callers must sort before applying projection ops.
	//
	// Every message Telegram returns is reported, including service messages
	// and the empty placeholders left by deletions. Those carry no projectable
	// payload, but they do occupy message ids, so silently dropping them would
	// make a full page look short and mislead callers paginating on page size.
	GetHistory(ctx context.Context, peer InputPeer, minID, offsetID int64, limit int) ([]HistoryMessage, error)
	SearchChannelMessages(ctx context.Context, peer InputPeer, query string, offsetID int64, limit int) ([]HistoryMessage, error)
	GetChannelMessage(ctx context.Context, peer InputPeer, msgID int64) (HistoryMessage, error)
	// GetBroadcastChannel looks up one joined broadcast channel, returning
	// ErrChannelUnavailable once the account cannot see it.
	GetBroadcastChannel(ctx context.Context, peer InputPeer) (JoinedBroadcastChannel, error)
	ListMediaSourcePeers(ctx context.Context) ([]SourcePeer, error)
	GetMediaSourcePeer(ctx context.Context, peer InputPeer) (SourcePeer, error)
	ResolvePublicChannel(ctx context.Context, username string) (SourcePeer, error)
	// DownloadChannelPhoto returns the small (160px) JPEG of a channel's
	// profile photo.
	DownloadChannelPhoto(ctx context.Context, peer InputPeer, photoID int64) ([]byte, error)

	// GetFileDocument resolves one Telegram message into a downloadable
	// document descriptor without downloading the bytes.
	GetFileDocument(ctx context.Context, peer InputPeer, msgID int64) (FileDocument, error)

	// DownloadFile streams a Telegram document message into w. onProgress is
	// optional; callers can use it to surface transfer progress.
	DownloadFile(ctx context.Context, peer InputPeer, msgID int64, w io.Writer, onProgress func(done, total int64)) error

	// DownloadFileAt downloads a Telegram document into w using random-access
	// writes starting at baseOffset. It may fetch chunks concurrently; callers
	// that need append/order semantics must use DownloadFile instead.
	DownloadFileAt(ctx context.Context, peer InputPeer, msgID int64, w io.WriterAt, baseOffset int64, onProgress func(done, total int64)) error

	// DownloadFileThumbnail streams a Telegram document thumbnail into w.
	DownloadFileThumbnail(ctx context.Context, peer InputPeer, msgID int64, thumbType string, w io.Writer) error

	// DeleteMessages removes file bodies from the channel. Used by tomb
	// follow-up and folder hard-delete. Best effort.
	DeleteMessages(ctx context.Context, peer InputPeer, msgIDs []int64) error

	// MissingMessages checks which of the given message IDs no longer exist
	// in the channel (deleted by any client, not just TDrive). Used by
	// external-delete reconciliation to detect a file whose backing message
	// vanished from Telegram directly.
	MissingMessages(ctx context.Context, peer InputPeer, msgIDs []int64) ([]int64, error)

	// GetChannelDifference returns one page of changes to the channel since
	// the given pts, so the sync engine can ask "what changed" instead of
	// rescanning history and probing every message's existence. Call again
	// with the returned Pts when Final is false to page through the rest.
	GetChannelDifference(ctx context.Context, peer InputPeer, pts int64, limit int) (ChannelDifference, error)

	// GetChannelPts returns the channel's current pts, for bootstrapping
	// GetChannelDifference after a full history scan.
	GetChannelPts(ctx context.Context, peer InputPeer) (int64, error)

	ListOwnedBroadcastChannels(ctx context.Context) ([]OwnedBroadcastChannel, error)
	ListJoinedBroadcastChannels(ctx context.Context) ([]JoinedBroadcastChannel, error)
	CreateBroadcastChannel(ctx context.Context, title, about string) (OwnedBroadcastChannel, error)
	CreateMegagroup(ctx context.Context, title, about string) (InputPeer, error)
	ExportInviteLink(ctx context.Context, peer InputPeer, requestNeeded bool) (string, error)
	CheckInvite(ctx context.Context, hash string) (InviteInfo, error)
	RequestJoin(ctx context.Context, hash string) error
	JoinByInvite(ctx context.Context, hash string) (InputPeer, error)
	LookupChannelTitle(ctx context.Context, peer InputPeer) (string, error)
	ListJoinRequests(ctx context.Context, peer InputPeer) ([]JoinRequest, error)
	HideJoinRequest(ctx context.Context, peer InputPeer, userID, accessHash int64, approved bool) error
	ResolveDriveChannel(ctx context.Context, channelID int64) (InputPeer, error)
	LeaveChannel(ctx context.Context, peer InputPeer) error

	// Close releases the shared connection. Called once at app shutdown.
	Close()
}

// IdempotentSender is the write extension used by durable mutation journals.
// Retrying a call with the same positive randomID returns the original Telegram
// message instead of creating a duplicate.
type IdempotentSender interface {
	SendControlWithRandomID(ctx context.Context, peer InputPeer, text string, silent bool, randomID int64) (msgID int64, err error)
	SendFileWithRandomID(ctx context.Context, peer InputPeer, r io.Reader, name, caption string, totalSize int64, onProgress func(sent, total int64), randomID int64) (SendFileResult, error)
}

func SendControlIdempotent(ctx context.Context, client Client, peer InputPeer, text string, silent bool, randomID int64) (int64, error) {
	if randomID <= 0 {
		return 0, fmt.Errorf("tgclient: random id must be positive")
	}
	sender, ok := client.(IdempotentSender)
	if !ok {
		return 0, fmt.Errorf("tgclient: idempotent sends are not supported")
	}
	slog.Debug("tgclient: sending control message", "channel_id", peer.ChannelID, "silent", silent, "text_len", len(text))
	msgID, err := sender.SendControlWithRandomID(ctx, peer, text, silent, randomID)
	if err != nil {
		slog.Error("tgclient: send control message failed", "channel_id", peer.ChannelID, "error", err)
		return 0, err
	}
	slog.Debug("tgclient: control message sent", "channel_id", peer.ChannelID, "msg_id", msgID)
	return msgID, nil
}

func SendFileIdempotent(ctx context.Context, client Client, peer InputPeer, r io.Reader, name, caption string, totalSize int64, onProgress func(sent, total int64), randomID int64) (SendFileResult, error) {
	if randomID <= 0 {
		return SendFileResult{}, fmt.Errorf("tgclient: random id must be positive")
	}
	sender, ok := client.(IdempotentSender)
	if !ok {
		return SendFileResult{}, fmt.Errorf("tgclient: idempotent sends are not supported")
	}
	slog.Debug("tgclient: sending file", "channel_id", peer.ChannelID, "name", name, "total_size", totalSize)
	result, err := sender.SendFileWithRandomID(ctx, peer, r, name, caption, totalSize, onProgress, randomID)
	if err != nil {
		slog.Error("tgclient: send file failed", "channel_id", peer.ChannelID, "name", name, "error", err)
		return SendFileResult{}, err
	}
	slog.Debug("tgclient: file sent", "channel_id", peer.ChannelID, "msg_id", result.MsgID, "total_size", totalSize)
	return result, nil
}
