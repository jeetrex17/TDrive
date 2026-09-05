package media

import (
	"context"
	"database/sql"
	"fmt"
	"sync"
	"time"

	tdcrypto "TDrive/backend/crypto"
	"TDrive/backend/tgclient"
	"TDrive/backend/thumbnail"
)

type PeerResolver interface {
	ResolvePeer(ctx context.Context, channelID int64) (tgclient.InputPeer, error)
}

type Config struct {
	DB     *sql.DB
	Peers  PeerResolver
	Ranges tgclient.RangeClient
	Keys   MasterKeyProvider
	// EncryptionOpenGate is held only while publishing an encrypted session.
	// EncryptionOpenGeneration changes across each vault-lock transition, which
	// lets slow network authentication happen outside the gate without allowing
	// a session derived before the transition to be published afterward.
	EncryptionOpenGate       sync.Locker
	EncryptionOpenGeneration func() uint64
	Thumbs                   *thumbnail.Cache
	ThumbGenerator           VideoThumbnailGenerator
}

// Service is the app-facing media entry point. It owns logical file resolution
// and loopback sessions; UI-specific players sit above it.
type Service struct {
	peers         PeerResolver
	ranges        tgclient.RangeClient
	resolveRetry  tgclient.FloodWaitRetryPolicy
	keys          MasterKeyProvider
	encGate       sync.Locker
	encGeneration func() uint64
	thumbs        *thumbnail.Cache
	thumbGen      VideoThumbnailGenerator
	resolver      *Resolver
	server        *Server
}

func NewService(cfg Config) *Service {
	s := &Service{
		peers:  cfg.Peers,
		ranges: cfg.Ranges,
		resolveRetry: tgclient.FloodWaitRetryPolicy{
			MaxRetries: 2, MaxWait: 30 * time.Second, MaxTotalWait: time.Minute,
		},
		keys:          cfg.Keys,
		encGate:       cfg.EncryptionOpenGate,
		encGeneration: cfg.EncryptionOpenGeneration,
		thumbs:        cfg.Thumbs,
		thumbGen:      cfg.ThumbGenerator,
		resolver:      NewResolver(cfg.DB),
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
	return s.open(ctx, channelID, fileID, StreamKindVideo)
}

func (s *Service) OpenStream(ctx context.Context, channelID, fileID int64) (OpenResult, error) {
	return s.open(ctx, channelID, fileID, StreamKindUnknown)
}

func (s *Service) open(ctx context.Context, channelID, fileID int64, requiredKind StreamKind) (OpenResult, error) {
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
		if file.EncryptionVersion != 1 {
			return OpenResult{}, fmt.Errorf("%w: version %d", ErrEncryptedUnsupported, file.EncryptionVersion)
		}
		if err := tdcrypto.ValidatePlaintextSize(file.PlaintextSize); err != nil {
			return OpenResult{}, fmt.Errorf("media: invalid encrypted plaintext size: %w", err)
		}
		if tdcrypto.CiphertextSize(file.PlaintextSize) != file.StoredSize {
			return OpenResult{}, fmt.Errorf("media: encrypted stored size mismatch: %w", tdcrypto.ErrCiphertextSize)
		}
	}
	kind := streamKindForName(file.Name)
	if kind == StreamKindUnknown || (requiredKind != StreamKindUnknown && kind != requiredKind) {
		return OpenResult{}, ErrUnsupportedMediaType
	}

	// Metadata RPCs can be rate limited before a range reader exists. Honor
	// Telegram's full wait here too, with a small, cancellable open budget.
	var peer tgclient.InputPeer
	err = s.resolveRetry.Do(ctx, func() error {
		var resolveErr error
		peer, resolveErr = s.peers.ResolvePeer(ctx, channelID)
		return resolveErr
	})
	if err != nil {
		return OpenResult{}, fmt.Errorf("media: resolve peer: %w", err)
	}
	segments := make([]resolvedSegment, 0, len(file.Segments))
	var start int64
	for _, seg := range file.Segments {
		var ref tgclient.DocumentRef
		err := s.resolveRetry.Do(ctx, func() error {
			var resolveErr error
			ref, resolveErr = s.ranges.ResolveDocument(ctx, peer, seg.MsgID)
			return resolveErr
		})
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

	var encryptionGeneration uint64
	if file.Encrypted && s.encGeneration != nil {
		encryptionGeneration = s.encGeneration()
		if encryptionGeneration%2 != 0 {
			return OpenResult{}, ErrKeyUnavailable
		}
	}

	var masterKey []byte
	if file.Encrypted {
		if s.keys == nil {
			return OpenResult{}, ErrKeyUnavailable
		}
		masterKey, err = s.keys.MasterKey(ctx, channelID)
		if len(masterKey) > 0 {
			defer clear(masterKey)
		}
		if err != nil {
			return OpenResult{}, fmt.Errorf("%w: %w", ErrKeyUnavailable, err)
		}
		if len(masterKey) == 0 {
			return OpenResult{}, ErrKeyUnavailable
		}
	}

	session, err := newSession(file, segments, s.ranges, s.thumbs, s.thumbGen, SessionOptions{
		Context:               ctx,
		EnableVideoThumbnails: kind == StreamKindVideo,
		MasterKey:             masterKey,
	})
	if err != nil {
		return OpenResult{}, err
	}
	if file.Encrypted && s.encGate != nil && s.encGeneration != nil {
		s.encGate.Lock()
		currentGeneration := s.encGeneration()
		if currentGeneration != encryptionGeneration || currentGeneration%2 != 0 {
			s.encGate.Unlock()
			session.Close()
			return OpenResult{}, ErrKeyUnavailable
		}
		err = s.server.Add(session)
		s.encGate.Unlock()
	} else {
		err = s.server.Add(session)
	}
	if err != nil {
		session.Close()
		return OpenResult{}, err
	}
	return OpenResult{
		Token:         session.Token(),
		URL:           session.URL(),
		ThumbnailURL:  session.ThumbnailURL(),
		Name:          file.Name,
		Kind:          kind,
		MimeType:      contentTypeFor(file.Name),
		SupportsRange: true,
		Info:          file,
	}, nil
}

// OpenResultForToken safely snapshots an active loopback session without
// transferring ownership or exposing the mutable Session.
func (s *Service) OpenResultForToken(token string) (OpenResult, error) {
	if s == nil || s.server == nil || token == "" {
		return OpenResult{}, ErrSessionNotFound
	}
	session := s.server.session(token)
	if session == nil {
		return OpenResult{}, ErrSessionNotFound
	}
	token, url, thumbnailURL, file, ok := session.openSnapshot()
	if !ok {
		return OpenResult{}, ErrSessionNotFound
	}
	kind := streamKindForName(file.Name)
	return OpenResult{
		Token:         token,
		URL:           url,
		ThumbnailURL:  thumbnailURL,
		Name:          file.Name,
		Kind:          kind,
		MimeType:      contentTypeFor(file.Name),
		SupportsRange: true,
		Info:          file,
	}, nil
}

func (s *Service) CloseSession(token string) error {
	if s == nil || s.server == nil {
		return ErrSessionNotFound
	}
	return s.server.CloseSession(token)
}

// CloseEncryptedSessions closes only active encrypted sessions. It is used when
// the vault locks so decryptors wipe retained derived keys and loopback URLs
// stop serving private plaintext.
func (s *Service) CloseEncryptedSessions() {
	if s == nil || s.server == nil {
		return
	}
	s.server.mu.Lock()
	sessions := make([]*Session, 0)
	for token, session := range s.server.sessions {
		if session == nil || !session.Encrypted() {
			continue
		}
		delete(s.server.sessions, token)
		sessions = append(sessions, session)
	}
	s.server.mu.Unlock()

	for _, session := range sessions {
		session.Close()
	}
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
