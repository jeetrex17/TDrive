package media

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"TDrive/backend/projection"
	"TDrive/backend/tgclient"
	"github.com/gotd/td/tgerr"
)

// RawDriveSource is the read-only Telegram metadata needed to open loose drive
// attachments. Keeping it narrow avoids coupling playback to upload operations.
type RawDriveSource interface {
	GetChannelMessage(context.Context, tgclient.InputPeer, int64) (tgclient.HistoryMessage, error)
	GetMediaSourcePeer(context.Context, tgclient.InputPeer) (tgclient.SourcePeer, error)
}

type peerRefresher interface {
	RefreshPeer(context.Context, int64) (tgclient.InputPeer, error)
}

func stalePeerError(err error) bool {
	return errors.Is(err, tgclient.ErrChannelUnavailable) || tgerr.Is(err, "CHANNEL_INVALID", "CHANNEL_PRIVATE", "PEER_ID_INVALID", "ACCESS_HASH_INVALID")
}

type rawDriveFile struct {
	file      LogicalFile
	peer      tgclient.InputPeer
	message   tgclient.HistoryMessage
	refreshed bool
}

func imageRevisionMatches(file LogicalFile, revision int64) bool {
	// Loose Telegram files have no projected revision in the list. Revision zero
	// is valid solely for this read-only source; projected files still require CAS.
	return file.Revision == revision
}

func (s *Service) rawDrivePeer(ctx context.Context, channelID int64) (tgclient.InputPeer, error) {
	if s.rawDrive == nil || s.accountID == nil || s.peers == nil || s.resolver == nil || s.resolver.db == nil {
		return tgclient.InputPeer{}, ErrFileNotFound
	}
	var kind string
	if err := s.resolver.db.QueryRowContext(ctx, "SELECT kind FROM channels WHERE channel_id = ?", channelID).Scan(&kind); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return tgclient.InputPeer{}, ErrFileNotFound
		}
		return tgclient.InputPeer{}, fmt.Errorf("media: lookup raw drive: %w", err)
	}
	peer, err := s.peers.ResolvePeer(ctx, channelID)
	if err != nil {
		return tgclient.InputPeer{}, fmt.Errorf("media: resolve drive peer: %w", err)
	}
	if peer.ChannelID != channelID {
		return tgclient.InputPeer{}, ErrExternalRestricted
	}
	switch kind {
	case projection.KindShared:
		peer.Kind = tgclient.PeerSupergroup
	case projection.KindPersonal:
		peer.Kind = tgclient.PeerChannel
	default:
		return tgclient.InputPeer{}, ErrExternalRestricted
	}
	return peer, nil
}

func (s *Service) resolveRawDrive(ctx context.Context, channelID, fileID int64) (rawDriveFile, error) {
	if err := ctx.Err(); err != nil {
		return rawDriveFile{}, err
	}
	if s.accountID == nil || s.rawDrive == nil {
		return rawDriveFile{}, ErrFileNotFound
	}
	owned, err := projection.ManagedMsgIDsIn(ctx, s.resolver.db, channelID, []int64{fileID})
	if err != nil {
		return rawDriveFile{}, err
	}
	if _, exists := owned[fileID]; exists {
		return rawDriveFile{}, ErrFileNotFound
	}
	accountID, err := s.accountID(ctx)
	if err != nil {
		return rawDriveFile{}, err
	}
	if accountID <= 0 {
		return rawDriveFile{}, ErrExternalRestricted
	}
	peer, err := s.rawDrivePeer(ctx, channelID)
	if err != nil {
		return rawDriveFile{}, err
	}
	message, err := s.rawDriveMessage(ctx, peer, fileID)
	refreshedPeer := false
	if err != nil && stalePeerError(err) {
		if refresher, ok := s.peers.(peerRefresher); ok {
			refreshed, refreshErr := refresher.RefreshPeer(ctx, channelID)
			if refreshErr != nil {
				return rawDriveFile{}, refreshErr
			}
			if refreshed.ChannelID != peer.ChannelID {
				return rawDriveFile{}, ErrExternalRestricted
			}
			refreshed.Kind = peer.Kind
			peer = refreshed
			refreshedPeer = true
			message, err = s.rawDriveMessage(ctx, peer, fileID)
		}
	}
	if err != nil {
		return rawDriveFile{}, err
	}
	captionName, allowed := projection.RawAttachmentCaption(message.Text, message.MediaSize)
	if !allowed {
		return rawDriveFile{}, ErrFileNotFound
	}
	namedMessage := message
	if captionName != "" {
		namedMessage.DocumentName = captionName
	}
	name := tgclient.MediaName(namedMessage)
	file := LogicalFile{
		ChannelID: channelID, FileID: fileID, Revision: 0, Name: name,
		StoredSize: message.MediaSize, PlaintextSize: message.MediaSize,
		Segments:   []Segment{{MsgID: fileID, Size: message.MediaSize}},
		SourceKind: "drive", SourcePeerKind: string(peer.PeerKind()), SourceAccountID: accountID,
	}
	if err := s.validateRawDrive(ctx, file); err != nil {
		return rawDriveFile{}, err
	}
	return rawDriveFile{file: file, peer: peer, message: message, refreshed: refreshedPeer}, nil
}

func (s *Service) rawDriveMessage(ctx context.Context, peer tgclient.InputPeer, fileID int64) (tgclient.HistoryMessage, error) {
	var message tgclient.HistoryMessage
	err := s.resolveRetry.Do(ctx, func() error { var err error; message, err = s.rawDriveMessageOnce(ctx, peer, fileID); return err })
	return message, err
}

func (s *Service) rawDriveMessageOnce(ctx context.Context, peer tgclient.InputPeer, fileID int64) (tgclient.HistoryMessage, error) {
	source, err := s.rawDrive.GetMediaSourcePeer(ctx, peer)
	if err != nil {
		return tgclient.HistoryMessage{}, err
	}
	if source.ID != peer.ChannelID || source.Kind != peer.PeerKind() || source.Protected || source.Restricted {
		return tgclient.HistoryMessage{}, ErrExternalRestricted
	}
	message, err := s.rawDrive.GetChannelMessage(ctx, peer, fileID)
	if err != nil {
		return tgclient.HistoryMessage{}, err
	}
	if message.MsgID != fileID || (message.ChannelID != 0 && message.ChannelID != peer.ChannelID) || (message.PeerKind != "" && message.PeerKind != peer.PeerKind()) {
		return tgclient.HistoryMessage{}, ErrExternalRestricted
	}
	if message.NoForwards || message.TTLSeconds > 0 || message.Paid || message.Restricted {
		return tgclient.HistoryMessage{}, ErrExternalRestricted
	}
	if !message.HasMedia || message.DocumentID == 0 || message.MediaSize <= 0 {
		return tgclient.HistoryMessage{}, ErrFileNotFound
	}
	return message, nil
}

func (s *Service) validateRawDrive(ctx context.Context, file LogicalFile) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.server.mu.Lock()
	closed := s.server.closed
	s.server.mu.Unlock()
	if closed {
		return ErrSessionNotFound
	}
	accountID, err := s.accountID(ctx)
	if err != nil {
		return err
	}
	if accountID != file.SourceAccountID {
		return ErrExternalRestricted
	}
	var kind string
	if err := s.resolver.db.QueryRowContext(ctx, "SELECT kind FROM channels WHERE channel_id = ?", file.ChannelID).Scan(&kind); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrExternalRestricted
		}
		return fmt.Errorf("media: validate raw drive: %w", err)
	}
	if !(kind == projection.KindShared && file.SourcePeerKind == string(tgclient.PeerSupergroup)) && !(kind == projection.KindPersonal && file.SourcePeerKind == string(tgclient.PeerChannel)) {
		return ErrExternalRestricted
	}
	owned, err := projection.ManagedMsgIDsIn(ctx, s.resolver.db, file.ChannelID, []int64{file.FileID})
	if err != nil {
		return err
	}
	if _, exists := owned[file.FileID]; !exists {
		return nil
	}
	// A concurrent personal-drive adoption may safely start owning this exact
	// original. Deletion, encryption or replacement must revoke further reads.
	projected, err := s.resolver.Resolve(ctx, file.ChannelID, file.FileID)
	if err != nil {
		return err
	}
	if projected.Encrypted || len(projected.Segments) != 1 || projected.Segments[0].MsgID != file.FileID || projected.StoredSize != file.StoredSize {
		return ErrFileNotFound
	}
	return nil
}

func (s *Service) openRawDrive(ctx context.Context, channelID, fileID int64, requiredKind StreamKind) (OpenResult, error) {
	source, err := s.resolveRawDrive(ctx, channelID, fileID)
	if err != nil {
		return OpenResult{}, err
	}
	kind := streamKindForName(source.file.Name)
	if kind == StreamKindUnknown || (requiredKind != StreamKindUnknown && kind != requiredKind) {
		return OpenResult{}, ErrUnsupportedMediaType
	}
	source, ref, err := s.resolveRawDriveReference(ctx, source)
	if err != nil {
		return OpenResult{}, err
	}
	checked := &externalRangeClient{base: s.ranges, client: s.rawDrive, peer: source.peer, original: ref, validate: func(ctx context.Context) error { return s.validateRawDrive(ctx, source.file) }}
	checked.lastRemoteCheck.Store(time.Now().UnixNano())
	session, err := newSession(source.file, []resolvedSegment{{size: ref.Size, ref: ref}}, checked, s.thumbs, s.thumbGen, SessionOptions{Context: ctx, EnableVideoThumbnails: kind == StreamKindVideo})
	if err != nil {
		return OpenResult{}, err
	}
	if kind == StreamKindImage {
		mimeType, err := admitImage(ctx, session, source.file.Name, s.imageLimits)
		if err != nil {
			session.Close()
			return OpenResult{}, err
		}
		session.mimeType = mimeType
	}
	if err := checked.validate(ctx); err != nil {
		session.Close()
		return OpenResult{}, err
	}
	if err := s.server.Add(session); err != nil {
		session.Close()
		return OpenResult{}, err
	}
	// Recheck after publication as well: Close cannot resurrect the server, and
	// an account transition racing the final Add never returns a usable URL.
	if err := checked.validate(ctx); err != nil {
		_ = s.CloseSession(session.Token())
		return OpenResult{}, err
	}
	return OpenResult{Token: session.Token(), URL: session.URL(), ThumbnailURL: session.ThumbnailURL(), HLSURL: session.HLSURL(), Name: source.file.Name, Kind: kind, MimeType: session.MimeType(), SupportsRange: true, Info: source.file}, nil
}

// resolveRawDriveReference permits one access-hash refresh per open, including
// a rejection that arrives after metadata succeeded but before bytes resolve.
func (s *Service) resolveRawDriveReference(ctx context.Context, source rawDriveFile) (rawDriveFile, tgclient.DocumentRef, error) {
	resolve := func() (tgclient.DocumentRef, error) {
		var ref tgclient.DocumentRef
		err := s.resolveRetry.Do(ctx, func() error {
			var err error
			ref, err = s.ranges.ResolveDocument(ctx, source.peer, source.file.FileID)
			return err
		})
		return ref, err
	}
	ref, err := resolve()
	if err != nil && !source.refreshed && stalePeerError(err) {
		if refresher, ok := s.peers.(peerRefresher); ok {
			peer, refreshErr := refresher.RefreshPeer(ctx, source.file.ChannelID)
			if refreshErr != nil {
				return source, ref, refreshErr
			}
			if peer.ChannelID != source.peer.ChannelID {
				return source, ref, ErrExternalRestricted
			}
			peer.Kind = source.peer.Kind
			message, refreshErr := s.rawDriveMessage(ctx, peer, source.file.FileID)
			if refreshErr != nil {
				return source, ref, refreshErr
			}
			if message.DocumentID != source.message.DocumentID || message.DocumentAccessHash != source.message.DocumentAccessHash || message.MediaSize != source.message.MediaSize {
				return source, ref, ErrExternalReplaced
			}
			source.peer = peer
			source.refreshed = true
			ref, err = resolve()
		}
	}
	if err != nil {
		return source, ref, fmt.Errorf("media: resolve raw drive document: %w", err)
	}
	if ref.Peer.ChannelID != source.peer.ChannelID || ref.Peer.PeerKind() != source.peer.PeerKind() || ref.Peer.AccessHash != source.peer.AccessHash || ref.MsgID != source.file.FileID || ref.DocumentID != source.message.DocumentID || ref.Size != source.message.MediaSize || ref.AccessHash != source.message.DocumentAccessHash {
		return source, ref, ErrExternalReplaced
	}
	return source, ref, nil
}
