package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"

	"TDrive/backend"
	"TDrive/backend/core"
	"TDrive/backend/datadir"
	"TDrive/backend/media"
	"TDrive/backend/thumbnail"
)

// MediaService owns everything that turns a file in a drive into something the
// user can look at: the media library listing, thumbnails, the tokenized
// loopback streams the webview plays from, and the out-of-webview native
// players that take over when the webview cannot decode a file.
//
// Those last two are why this is one service and not two. A native player and a
// loopback session are halves of the same resource: the player reads bytes
// through the session, and closing either one without the other leaks a range
// reader, an mpv process, or both. Keeping them on one type means the pairing
// is enforced by a single mutex-guarded table rather than by two services
// remembering to call each other in the right order.
//
// It also means only this service ever has to be asked to shut players down --
// which the updater and the vault lock both need, and both do through the
// narrow nativeMediaCloser rather than by reaching in here.
type MediaService struct {
	host serviceHost
	// mount is held around OpenOriginalImage, the one capability here that
	// outlives the call that created it and so must not be published after a
	// logout has become terminal.
	mount mountLifecycleGate

	// nativeMedia owns out-of-webview player processes tied to media loopback
	// sessions. Each token must be closed before the backend shuts down so the
	// range reader and native surface do not outlive the app.
	nativeMediaMu sync.Mutex
	nativeMedia   map[string]*nativeMediaSession
}

func newMediaService(host serviceHost, mount mountLifecycleGate) *MediaService {
	return &MediaService{host: host, mount: mount}
}

// mediaSessions is the engine's loopback session table, or nil when the backend
// is not up. Every caller has to check: a player can still be closing while
// shutdown has already torn the engine down, and that has to be a no-op rather
// than a panic.
func (s *MediaService) mediaSessions() *media.Service {
	if engine := s.engine(); engine != nil {
		return engine.MediaService()
	}
	return nil
}

func (s *MediaService) engine() *core.Engine {
	return s.host.coreEngine()
}

// Renditions are disposable and shared across the gallery and viewer. Keep a
// smaller disk working set on mobile; neither budget depends on library size.
func thumbnailCacheBudget() int64 {
	if runtime.GOOS == "ios" || runtime.GOOS == "android" {
		return 256 << 20
	}
	return 1 << 30
}

// thumbnailCacheDir is where generated thumbnails live. It sits under the OS
// cache directory because the contents are disposable; encrypted-drive
// thumbnails are stored as ciphertext regardless.
func thumbnailCacheDir() string {
	base, err := datadir.CacheDir()
	if err != nil || base == "" {
		if runtime.GOOS == "ios" || runtime.GOOS == "android" {
			// A missing mobile cache root must disable the disposable cache. The
			// process-wide temp directory may be shared or unwritable there.
			return ""
		}
		base = filepath.Join(os.TempDir(), "TDrive")
	}
	return filepath.Join(base, "thumbnails")
}

func newThumbnailCache() *thumbnail.Cache {
	return thumbnail.NewCache(thumbnailCacheDir(), thumbnailCacheBudget())
}

// ListMedia is the legacy bulk API. The gallery uses GetMediaTimeline and
// ListMediaPage so metadata memory stays bounded as the library grows.
func (s *MediaService) ListMedia() ([]backend.FileMetaData, error) {
	svc, err := engineReadService(s.engine())
	if err != nil {
		return nil, err
	}
	files, err := svc.MediaFiles(s.host.activeChannelID())
	if err != nil {
		return nil, err
	}
	out := make([]backend.FileMetaData, 0, len(files))
	for _, f := range files {
		out = append(out, backend.FileMetaData{
			TgMsgID:       int(f.MsgID),
			Name:          f.Name,
			Size:          f.Size,
			ParentID:      f.ParentID,
			UploadTime:    f.UploadTime,
			UploaderID:    f.UploaderID,
			Encrypted:     f.Encrypted,
			PlaintextSize: f.PlaintextSize,
			Revision:      f.Revision,
		})
	}
	return out, nil
}

// Thumbnail is the legacy base64 wrapper around the bounded rendition path.
// Cache misses fetch an existing small derivative, never the original photo.
func (s *MediaService) Thumbnail(msgID int) (PreviewPayload, error) {
	svc, err := engineFileService(s.engine())
	if err != nil {
		return PreviewPayload{}, err
	}
	payload, err := svc.Thumbnail(s.host.appContext(), s.host.activeChannelID(), msgID)
	if err != nil {
		return PreviewPayload{}, err
	}
	return PreviewPayload(payload), nil
}

// OpenMedia creates a short-lived, tokenized loopback URL for a plain media
// file in the active drive. The frontend player must call CloseMedia when the
// preview closes so the underlying range reader and cache are released.
func (s *MediaService) OpenMedia(msgID int) (media.OpenResult, error) {
	sessions := s.mediaSessions()
	if sessions == nil {
		return media.OpenResult{}, fmt.Errorf("backend not ready")
	}
	return sessions.Open(s.host.appContext(), s.host.activeChannelID(), int64(msgID))
}

// OpenStream creates a tokenized loopback byte stream for an in-app file
// opener. Unlike OpenMedia, it is not video-only; callers choose the viewer
// from the returned stream kind and must still call CloseMedia on close.
func (s *MediaService) OpenStream(msgID int) (media.OpenResult, error) {
	sessions := s.mediaSessions()
	if sessions == nil {
		return media.OpenResult{}, fmt.Errorf("backend not ready")
	}
	return sessions.OpenStream(s.host.appContext(), s.host.activeChannelID(), int64(msgID))
}

type originalImageOpener interface {
	OpenImage(context.Context, int64, int64, int64) (media.OpenResult, error)
	Resolve(context.Context, int64, int64) (media.LogicalFile, error)
}

func openOriginalImage(ctx context.Context, opener originalImageOpener, channelID, msgID, revision int64) (media.OpenResult, error) {
	if opener == nil {
		return media.OpenResult{}, fmt.Errorf("backend not ready")
	}
	opened, err := opener.OpenImage(ctx, channelID, msgID, revision)
	if !errors.Is(err, media.ErrStaleRevision) {
		return opened, err
	}
	current, resolveErr := opener.Resolve(ctx, channelID, msgID)
	if resolveErr != nil {
		return media.OpenResult{}, resolveErr
	}
	return opener.OpenImage(ctx, channelID, msgID, current.Revision)
}

// OpenOriginalImage returns one revision-bound capability for the original
// raster bytes. It shares the existing media range and CloseMedia lifecycle;
// lifecycle checks around the open prevent logout from publishing a new
// capability after session revocation has become terminal.
func (s *MediaService) OpenOriginalImage(msgID int, revision int64) (media.OpenResult, error) {
	sessions := s.mediaSessions()
	if sessions == nil {
		return media.OpenResult{}, fmt.Errorf("backend not ready")
	}
	// Reject immediately after terminal logout without holding the lifecycle
	// gate across Telegram I/O. A second check below closes a capability if
	// logout raced the open.
	release, err := s.mount.acquireMountLifecycle(s.host.appContext())
	if err != nil {
		return media.OpenResult{}, err
	}
	release()
	channelID := s.host.activeChannelID()
	opened, err := openOriginalImage(
		s.host.appContext(),
		sessions,
		channelID,
		int64(msgID),
		revision,
	)
	if err != nil {
		return media.OpenResult{}, err
	}
	release, err = s.mount.acquireMountLifecycle(s.host.appContext())
	if err != nil {
		_ = sessions.CloseSession(opened.Token)
		return media.OpenResult{}, err
	}
	defer release()
	if channelID != s.host.activeChannelID() {
		_ = sessions.CloseSession(opened.Token)
		return media.OpenResult{}, fmt.Errorf("gallery drive changed")
	}
	return opened, nil
}

func (s *MediaService) CloseMedia(token string) error {
	sessions := s.mediaSessions()
	if sessions == nil {
		return nil
	}
	return sessions.CloseSession(token)
}

func (s *MediaService) UpdateMediaPlayback(update media.PlaybackUpdate) error {
	sessions := s.mediaSessions()
	if sessions == nil {
		return nil
	}
	return sessions.UpdatePlayback(update)
}

func (s *MediaService) GetMediaStats(token string) (media.MediaStats, error) {
	sessions := s.mediaSessions()
	if sessions == nil {
		return media.MediaStats{}, nil
	}
	return sessions.Stats(token), nil
}
