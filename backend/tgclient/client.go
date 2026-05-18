// Package tgclient is a small adapter over Telegram. It exists so the rest
// of the app — sync, backfill, app.go local actions — can be written and
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
	"time"
)

var ErrFloodWait = errors.New("tgclient: flood wait")

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
}

// HistoryMessage is the subset of a tg.Message that sync/backfill/read paths care about.
// We deliberately avoid leaking the gotd types so the fake stays cheap.
type HistoryMessage struct {
	MsgID              int64
	Date               int64
	FromID             int64
	Text               string // caption for media messages, body for text messages
	HasMedia           bool
	MediaSize          int64
	DocumentName       string
	DocumentAccessHash int64
}

// SendFileResult is what SendFile returns. We split it from a bare msgID
// because the upload progress callback is configurable and we may want to
// extend with more fields (e.g. document size confirmation).
type SendFileResult struct {
	MsgID int64
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

// Client is the surface sync, backfill, and local-action paths use to talk
// to Telegram. Both the real (gotd-backed) and fake test implementations
// implement this.
type Client interface {
	// SelfID returns the logged-in user's Telegram user ID. Used by
	// ProjectFromOp's actorID field.
	SelfID(ctx context.Context) (int64, error)

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
	GetHistory(ctx context.Context, peer InputPeer, minID, offsetID int64, limit int) ([]HistoryMessage, error)

	// DeleteMessages removes file bodies from the channel. Used by tomb
	// follow-up and folder hard-delete. Best effort.
	DeleteMessages(ctx context.Context, peer InputPeer, msgIDs []int64) error

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
}
