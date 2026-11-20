package file

import (
	"cmp"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"strconv"

	"TDrive/backend/channelsource"
	"TDrive/backend/projection"
	"TDrive/backend/tgclient"
)

// SharedCopyPeerKind identifies Shared Drive selections in the copy journal.
const SharedCopyPeerKind = "shared-drive"

// SharedCopyFile describes a selected file and its body messages in file order.
type SharedCopyFile struct {
	Name  string
	Parts []int64
}

// ResolveSharedCopy captures the live file's ordered source message IDs. A
// managed file pins the selected logical revision. A raw file pins its message.
func (s *Service) ResolveSharedCopy(ctx context.Context, source channelsource.CopySource) (SharedCopyFile, error) {
	if tgclient.IsSharedRawCopyIdentity(source.Identity) {
		ref, err := s.ResolveSharedCopyPart(ctx, source, source.MsgID)
		if err != nil {
			return SharedCopyFile{}, err
		}
		return SharedCopyFile{Name: ref.Name, Parts: []int64{source.MsgID}}, nil
	}
	revision, err := sharedCopyRevision(source)
	if err != nil {
		return SharedCopyFile{}, err
	}
	if _, err := s.sharedCopyPeer(ctx, source); err != nil {
		return SharedCopyFile{}, err
	}
	file, err := s.sharedCopyManagedFile(ctx, source, revision)
	if err != nil {
		return SharedCopyFile{}, err
	}
	parts := make([]int64, 0, len(file.Parts))
	for _, part := range file.Parts {
		parts = append(parts, part.MsgID)
	}
	return SharedCopyFile{Name: file.Name, Parts: parts}, nil
}

// ResolveSharedCopyPart rechecks membership, current revision, and the policy
// on the exact attachment when a copy prepares or attempts a Telegram send.
func (s *Service) ResolveSharedCopyPart(ctx context.Context, source channelsource.CopySource, msgID int64) (tgclient.DocumentRef, error) {
	revision, err := sharedCopyRevision(source)
	if err != nil {
		return tgclient.DocumentRef{}, err
	}
	if msgID <= 0 || msgID > math.MaxInt32 {
		return tgclient.DocumentRef{}, channelsource.ErrCopyChanged
	}
	peer, err := s.sharedCopyPeer(ctx, source)
	if err != nil {
		return tgclient.DocumentRef{}, err
	}
	expected, err := s.sharedCopyPart(ctx, source, revision, msgID)
	if err != nil {
		return tgclient.DocumentRef{}, err
	}
	raw := revision == 0
	var identity string
	if raw {
		identity = source.Identity
	}
	ref, err := s.readSharedCopyDocument(ctx, peer, msgID, identity)
	if err != nil {
		return tgclient.DocumentRef{}, err
	}
	if !raw && expected.Size != ref.Size {
		return tgclient.DocumentRef{}, channelsource.ErrCopyChanged
	}
	if err := s.checkSharedCopyScope(ctx, source); err != nil {
		return tgclient.DocumentRef{}, err
	}
	part, err := s.sharedCopyPart(ctx, source, revision, msgID)
	if err != nil {
		return tgclient.DocumentRef{}, err
	}
	if part != expected {
		return tgclient.DocumentRef{}, channelsource.ErrCopyChanged
	}
	if !raw {
		ref.Name = part.Name
	}
	return ref, nil
}

func sharedCopyRevision(source channelsource.CopySource) (int64, error) {
	if source.PeerKind != SharedCopyPeerKind || source.AccountID <= 0 || source.PeerID <= 0 ||
		source.MsgID <= 0 || source.MsgID > math.MaxInt32 {
		return 0, channelsource.ErrCopyNotAllowed
	}
	revision, err := strconv.ParseInt(source.Generation, 10, 64)
	if err != nil || strconv.FormatInt(revision, 10) != source.Generation {
		return 0, channelsource.ErrCopyNotAllowed
	}
	if (source.Identity == "managed" && revision > 0) || (tgclient.IsSharedRawCopyIdentity(source.Identity) && revision == 0) {
		return revision, nil
	}
	return 0, channelsource.ErrCopyNotAllowed
}

// ValidateSharedCopySource checks the persisted copy selection's shape without
// contacting Telegram. Resolver calls still verify access and content.
func ValidateSharedCopySource(source channelsource.CopySource) error {
	_, err := sharedCopyRevision(source)
	return err
}

func (s *Service) checkSharedCopyScope(ctx context.Context, source channelsource.CopySource) error {
	if ctx == nil || s.DB == nil || s.TG == nil || s.Peers == nil || s.ActorID == nil {
		return channelsource.ErrCopyNotAllowed
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	account, err := s.ActorID(ctx)
	if err != nil {
		return err
	}
	if account != source.AccountID {
		return channelsource.ErrCopyNotAllowed
	}
	var kind string
	err = s.DB.QueryRowContext(ctx, "SELECT kind FROM channels WHERE channel_id=?", source.PeerID).Scan(&kind)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && kind != projection.KindShared) {
		return channelsource.ErrCopyNotAllowed
	}
	return err
}

func (s *Service) sharedCopyPeer(ctx context.Context, source channelsource.CopySource) (tgclient.InputPeer, error) {
	if err := s.checkSharedCopyScope(ctx, source); err != nil {
		return tgclient.InputPeer{}, err
	}
	peer, err := s.Peers.ResolvePeer(ctx, source.PeerID)
	if err != nil {
		return tgclient.InputPeer{}, err
	}
	if peer.ChannelID != source.PeerID {
		return tgclient.InputPeer{}, channelsource.ErrCopyNotAllowed
	}
	peer.Kind = tgclient.PeerSupergroup
	live, err := s.TG.GetMediaSourcePeer(ctx, peer)
	if errors.Is(err, tgclient.ErrChannelUnavailable) {
		return tgclient.InputPeer{}, channelsource.ErrCopyNotAllowed
	}
	if err != nil {
		return tgclient.InputPeer{}, err
	}
	if live.ID != peer.ChannelID || live.Kind != peer.Kind || live.Protected || live.Restricted {
		return tgclient.InputPeer{}, channelsource.ErrCopyNotAllowed
	}
	peer.AccessHash = cmp.Or(live.AccessHash, peer.AccessHash)
	return peer, nil
}

func (s *Service) sharedCopyManagedFile(ctx context.Context, source channelsource.CopySource, revision int64) (projection.DownloadFile, error) {
	file, found, err := projection.FileDownloadRefContext(ctx, s.DB, source.PeerID, source.MsgID)
	if err != nil {
		return projection.DownloadFile{}, err
	}
	if !found || file.Revision != revision {
		return projection.DownloadFile{}, channelsource.ErrCopyChanged
	}
	if file.Encrypted || file.EncryptionVersion != 0 || file.StoredSize <= 0 {
		return projection.DownloadFile{}, channelsource.ErrCopyNotAllowed
	}
	if len(file.Parts) == 0 {
		bodyID := file.ContentMsgID
		if bodyID == 0 {
			bodyID = file.LogicalMsgID
		}
		file.Parts = []projection.FilePart{{MsgID: bodyID, Size: file.StoredSize}}
	}
	seen := make(map[int64]struct{}, len(file.Parts))
	for _, part := range file.Parts {
		_, duplicate := seen[part.MsgID]
		if part.MsgID <= 0 || part.MsgID > math.MaxInt32 || part.Size <= 0 || duplicate {
			return projection.DownloadFile{}, channelsource.ErrCopyChanged
		}
		seen[part.MsgID] = struct{}{}
	}
	return file, nil
}

type sharedCopyPart struct {
	Name string
	Size int64
}

func (s *Service) sharedCopyPart(ctx context.Context, source channelsource.CopySource, revision, msgID int64) (sharedCopyPart, error) {
	if revision == 0 {
		if msgID != source.MsgID {
			return sharedCopyPart{}, channelsource.ErrCopyChanged
		}
		owned, err := projection.ManagedMsgIDsIn(ctx, s.DB, source.PeerID, []int64{msgID})
		if err != nil {
			return sharedCopyPart{}, err
		}
		if _, managed := owned[msgID]; managed {
			return sharedCopyPart{}, channelsource.ErrCopyChanged
		}
		return sharedCopyPart{}, nil
	}
	file, err := s.sharedCopyManagedFile(ctx, source, revision)
	if err != nil {
		return sharedCopyPart{}, err
	}
	for _, part := range file.Parts {
		if part.MsgID == msgID {
			return sharedCopyPart{Name: file.Name, Size: part.Size}, nil
		}
	}
	return sharedCopyPart{}, channelsource.ErrCopyChanged
}

func (s *Service) readSharedCopyDocument(ctx context.Context, peer tgclient.InputPeer, msgID int64, rawIdentity string) (tgclient.DocumentRef, error) {
	resolver, ok := s.TG.(tgclient.MessageDocumentResolver)
	if !ok {
		return tgclient.DocumentRef{}, channelsource.ErrCopyNotAllowed
	}
	message, ref, err := resolver.ResolveMessageDocument(ctx, peer, msgID)
	switch {
	case errors.Is(err, tgclient.ErrMessageNotFound):
		return tgclient.DocumentRef{}, channelsource.ErrCopyChanged
	case errors.Is(err, tgclient.ErrNotFile), errors.Is(err, tgclient.ErrEmptyDocument):
		return tgclient.DocumentRef{}, channelsource.ErrCopyNotAllowed
	case err != nil:
		return tgclient.DocumentRef{}, fmt.Errorf("file: read Shared Drive document: %w", err)
	}
	if message.NoForwards || message.TTLSeconds != 0 || message.AutoDeleteSeconds != 0 ||
		message.Paid || message.Restricted || message.NativePhoto || !message.HasMedia || ref.PhotoSizeType != "" {
		return tgclient.DocumentRef{}, channelsource.ErrCopyNotAllowed
	}
	if message.Placeholder || message.MsgID != msgID || (message.ChannelID != 0 && message.ChannelID != peer.ChannelID) ||
		(message.PeerKind != "" && message.PeerKind != peer.Kind) || ref.MsgID != msgID || ref.Peer != peer ||
		ref.DocumentID <= 0 || ref.DocumentID != message.DocumentID || ref.Size <= 0 || ref.Size != message.MediaSize {
		return tgclient.DocumentRef{}, channelsource.ErrCopyChanged
	}
	if rawIdentity != "" {
		if tgclient.SharedRawCopyIdentity(message) != rawIdentity {
			return tgclient.DocumentRef{}, channelsource.ErrCopyChanged
		}
		name, allowed := projection.RawAttachmentCaption(message.Text, message.MediaSize)
		if !allowed {
			return tgclient.DocumentRef{}, channelsource.ErrCopyNotAllowed
		}
		if name != "" {
			message.DocumentName = name
		}
		ref.Name = tgclient.MediaName(message)
	}
	return ref, nil
}
