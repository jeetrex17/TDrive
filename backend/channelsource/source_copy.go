package channelsource

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"math"

	"TDrive/backend/projection"
	"TDrive/backend/tgclient"
)

var (
	ErrCopyNotAllowed = errors.New("Telegram does not allow this attachment to be saved")
	ErrCopyChanged    = errors.New("This attachment changed. Refresh the source and choose the file again")
)

// CopySource carries the selected attachment between the UI and copy journal.
// Generation is a connection generation for a connected source, and the
// decimal file revision for a Shared Drive (zero for a loose attachment).
// The resolver compares Identity and rechecks live copy permission.
type CopySource struct {
	AccountID  int64  `json:"account_id"`
	PeerKind   string `json:"peer_kind"`
	PeerID     int64  `json:"peer_id"`
	Generation string `json:"generation"`
	MsgID      int64  `json:"msg_id"`
	Identity   string `json:"identity"`
}

func canCopyDocument(source tgclient.SourcePeer, message tgclient.HistoryMessage) bool {
	_, allowedCaption := projection.RawAttachmentCaption(message.Text, message.MediaSize)
	return message.HasMedia && message.MsgID > 0 && message.DocumentID > 0 && message.MediaSize > 0 &&
		!source.Protected && !source.Restricted && !message.NoForwards && !message.Restricted &&
		!message.Paid && !message.NativePhoto && message.TTLSeconds == 0 && message.AutoDeleteSeconds == 0 &&
		allowedCaption
}

func copyIdentity(source tgclient.SourcePeer, message tgclient.HistoryMessage) string {
	if !canCopyDocument(source, message) {
		return ""
	}
	identity := fmt.Appendf(nil, "%s/%d/%d/%d/%d", source.Kind, source.ID, message.MsgID, message.DocumentID, message.MediaSize)
	return fmt.Sprintf("%x", sha256.Sum256(identity))
}

// ResolveCopyMedia rechecks current peer access and the exact message's
// protection and document identity before supplying a Telegram resend handle.
func (s *Service) ResolveCopyMedia(ctx context.Context, request CopySource) (tgclient.DocumentRef, error) {
	if !validSource(request.PeerKind, request.PeerID) || request.AccountID <= 0 || request.Generation == "" ||
		request.MsgID <= 0 || request.MsgID > math.MaxInt32 || len(request.Identity) != sha256.Size*2 {
		return tgclient.DocumentRef{}, ErrCopyNotAllowed
	}
	connection := SourceInfo{AccountID: request.AccountID, PeerKind: request.PeerKind,
		PeerID: request.PeerID, Generation: request.Generation}
	if err := s.checkSourceScope(ctx, connection); err != nil {
		return tgclient.DocumentRef{}, err
	}
	stored, _, source, err := s.currentSource(ctx, connection.PeerKind, connection.PeerID)
	if err != nil {
		return tgclient.DocumentRef{}, err
	}
	if stored.AccountID != connection.AccountID || stored.Generation != connection.Generation {
		return tgclient.DocumentRef{}, ErrNotConnected
	}
	if source.Protected {
		return tgclient.DocumentRef{}, ErrCopyNotAllowed
	}
	ref, err := s.copyDocument(ctx, source, request.MsgID, request.Identity)
	if err != nil {
		return tgclient.DocumentRef{}, err
	}
	if err := s.checkSourceScope(ctx, stored); err != nil {
		return tgclient.DocumentRef{}, err
	}
	return ref, nil
}

func (s *Service) copyDocument(ctx context.Context, source tgclient.SourcePeer, msgID int64, expectedIdentity string) (tgclient.DocumentRef, error) {
	client, ok := s.tg.(tgclient.MessageDocumentResolver)
	if !ok {
		return tgclient.DocumentRef{}, ErrCopyNotAllowed
	}
	peer := tgclient.InputPeer{Kind: source.Kind, ChannelID: source.ID, AccessHash: source.AccessHash}
	message, ref, err := client.ResolveMessageDocument(ctx, peer, msgID)
	switch {
	case errors.Is(err, tgclient.ErrNotFile), errors.Is(err, tgclient.ErrEmptyDocument):
		return tgclient.DocumentRef{}, ErrCopyNotAllowed
	case errors.Is(err, tgclient.ErrMessageNotFound):
		return tgclient.DocumentRef{}, ErrCopyChanged
	case err != nil:
		return tgclient.DocumentRef{}, fmt.Errorf("media source: read selected attachment: %w", err)
	}
	identity := copyIdentity(source, message)
	if identity == "" {
		return tgclient.DocumentRef{}, ErrCopyNotAllowed
	}
	if identity != expectedIdentity || message.MsgID != msgID || ref.MsgID != msgID ||
		ref.Peer != peer || ref.DocumentID != message.DocumentID || ref.Size != message.MediaSize || ref.PhotoSizeType != "" {
		return tgclient.DocumentRef{}, ErrCopyChanged
	}
	ref.Name = tgclient.MediaName(message)
	return ref, nil
}
