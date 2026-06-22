package media

import (
	"context"
	"database/sql"
	"fmt"

	"TDrive/backend/tgclient"
	"TDrive/backend/thumbnail"
)

type PeerResolver interface {
	ResolvePeer(ctx context.Context, channelID int64) (tgclient.InputPeer, error)
}

type Config struct {
	DB             *sql.DB
	Peers          PeerResolver
	Ranges         tgclient.RangeClient
	Thumbs         *thumbnail.Cache
	ThumbGenerator VideoThumbnailGenerator
}

// Service is the app-facing media entry point. It owns logical file resolution
// and loopback sessions; UI-specific players sit above it.
type Service struct {
	peers    PeerResolver
	ranges   tgclient.RangeClient
	thumbs   *thumbnail.Cache
	thumbGen VideoThumbnailGenerator
	resolver *Resolver
	server   *Server
}

func NewService(cfg Config) *Service {
	s := &Service{
		peers:    cfg.Peers,
		ranges:   cfg.Ranges,
		thumbs:   cfg.Thumbs,
		thumbGen: cfg.ThumbGenerator,
		resolver: NewResolver(cfg.DB),
	}
	if s.thumbGen == nil {
		s.thumbGen = NewMPVThumbnailGenerator()
	}
	sweepStaleVideoThumbnailDirs()
	s.server = NewServer(s)
	return s
}

func (s *Service) Resolve(ctx context.Context, channelID, fileID int64) (LogicalFile, error) {
	if s == nil || s.resolver == nil {
		return LogicalFile{}, ErrDBNotReady
	}
	return s.resolver.Resolve(ctx, channelID, fileID)
}

func (s *Service) Open(ctx context.Context, channelID, fileID int64) (OpenResult, error) {
	if s == nil {
		return OpenResult{}, ErrDBNotReady
	}
	if s.peers == nil {
		return OpenResult{}, ErrPeerResolverNotReady
	}
	if s.ranges == nil {
		return OpenResult{}, ErrRangeClientNotReady
	}
	if s.server == nil {
		s.server = NewServer(s)
	}

	file, err := s.Resolve(ctx, channelID, fileID)
	if err != nil {
		return OpenResult{}, err
	}
	if file.Encrypted {
		return OpenResult{}, ErrEncryptedUnsupported
	}
	if !isSupportedMediaName(file.Name) {
		return OpenResult{}, ErrUnsupportedMediaType
	}

	peer, err := s.peers.ResolvePeer(ctx, channelID)
	if err != nil {
		return OpenResult{}, fmt.Errorf("media: resolve peer: %w", err)
	}
	segments := make([]resolvedSegment, 0, len(file.Segments))
	var start int64
	for _, seg := range file.Segments {
		ref, err := s.ranges.ResolveDocument(ctx, peer, seg.MsgID)
		if err != nil {
			return OpenResult{}, fmt.Errorf("media: resolve segment %d: %w", seg.MsgID, err)
		}
		if seg.Size > 0 && ref.Size != seg.Size {
			return OpenResult{}, fmt.Errorf("media: segment %d size mismatch: projection=%d telegram=%d", seg.MsgID, seg.Size, ref.Size)
		}
		segments = append(segments, resolvedSegment{
			start: start,
			size:  ref.Size,
			ref:   ref,
		})
		start += ref.Size
	}
	if start != file.StoredSize {
		return OpenResult{}, fmt.Errorf("media: logical size mismatch: segments=%d file=%d", start, file.StoredSize)
	}

	session, err := newSession(file, segments, s.ranges, s.thumbs, s.thumbGen)
	if err != nil {
		return OpenResult{}, err
	}
	if err := s.server.Add(session); err != nil {
		session.Close()
		return OpenResult{}, err
	}
	return OpenResult{
		Token:        session.Token(),
		URL:          session.URL(),
		ThumbnailURL: session.ThumbnailURL(),
		Name:         file.Name,
		Info:         file,
	}, nil
}

func (s *Service) CloseSession(token string) error {
	if s == nil || s.server == nil {
		return ErrSessionNotFound
	}
	return s.server.CloseSession(token)
}

func (s *Service) UpdatePlayback(update PlaybackUpdate) error {
	if s == nil || s.server == nil {
		return ErrSessionNotFound
	}
	return s.server.UpdatePlayback(update)
}

func (s *Service) Stats(token string) MediaStats {
	if s == nil || s.server == nil {
		return MediaStats{}
	}
	return s.server.Stats(token)
}

func (s *Service) Close() error {
	if s != nil && s.server != nil {
		return s.server.Close()
	}
	return nil
}
