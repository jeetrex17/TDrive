package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"TDrive/backend"
	"TDrive/backend/datadir"
	"TDrive/backend/media"
	"TDrive/backend/thumbnail"
)

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
func (a *App) ListMedia() ([]backend.FileMetaData, error) {
	svc, err := a.requireReadService()
	if err != nil {
		return nil, err
	}
	files, err := svc.MediaFiles(a.ActiveChannelID())
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
func (a *App) Thumbnail(msgID int) (PreviewPayload, error) {
	svc, err := a.requireFileService()
	if err != nil {
		return PreviewPayload{}, err
	}
	payload, err := svc.Thumbnail(a.ctx, a.ActiveChannelID(), msgID)
	if err != nil {
		return PreviewPayload{}, err
	}
	return PreviewPayload(payload), nil
}

// OpenMedia creates a short-lived, tokenized loopback URL for a plain media
// file in the active drive. The frontend player must call CloseMedia when the
// preview closes so the underlying range reader and cache are released.
func (a *App) OpenMedia(msgID int) (media.OpenResult, error) {
	if a.engine == nil {
		return media.OpenResult{}, fmt.Errorf("backend not ready")
	}
	return a.engine.MediaService().Open(a.ctx, a.ActiveChannelID(), int64(msgID))
}

// OpenStream creates a tokenized loopback byte stream for an in-app file
// opener. Unlike OpenMedia, it is not video-only; callers choose the viewer
// from the returned stream kind and must still call CloseMedia on close.
func (a *App) OpenStream(msgID int) (media.OpenResult, error) {
	if a.engine == nil {
		return media.OpenResult{}, fmt.Errorf("backend not ready")
	}
	return a.engine.MediaService().OpenStream(a.ctx, a.ActiveChannelID(), int64(msgID))
}

type originalImageOpener interface {
	OpenImage(context.Context, int64, int64, int64) (media.OpenResult, error)
}

func openOriginalImage(ctx context.Context, opener originalImageOpener, channelID, msgID, revision int64) (media.OpenResult, error) {
	if opener == nil {
		return media.OpenResult{}, fmt.Errorf("backend not ready")
	}
	return opener.OpenImage(ctx, channelID, msgID, revision)
}

// OpenOriginalImage returns one revision-bound capability for the original
// raster bytes. It shares the existing media range and CloseMedia lifecycle;
// lifecycle checks around the open prevent logout from publishing a new
// capability after session revocation has become terminal.
func (a *App) OpenOriginalImage(msgID int, revision int64) (media.OpenResult, error) {
	if a == nil || a.engine == nil {
		return media.OpenResult{}, fmt.Errorf("backend not ready")
	}
	// Reject immediately after terminal logout without holding the lifecycle
	// gate across Telegram I/O. A second check below closes a capability if
	// logout raced the open.
	release, err := a.acquireMountLifecycle(a.appContext())
	if err != nil {
		return media.OpenResult{}, err
	}
	release()
	channelID := a.ActiveChannelID()
	opened, err := openOriginalImage(
		a.appContext(),
		a.engine.MediaService(),
		channelID,
		int64(msgID),
		revision,
	)
	if err != nil {
		return media.OpenResult{}, err
	}
	release, err = a.acquireMountLifecycle(a.appContext())
	if err != nil {
		_ = a.engine.MediaService().CloseSession(opened.Token)
		return media.OpenResult{}, err
	}
	defer release()
	if channelID != a.ActiveChannelID() {
		_ = a.engine.MediaService().CloseSession(opened.Token)
		return media.OpenResult{}, fmt.Errorf("gallery drive changed")
	}
	return opened, nil
}

func (a *App) CloseMedia(token string) error {
	if a.engine == nil {
		return nil
	}
	return a.engine.MediaService().CloseSession(token)
}

func (a *App) UpdateMediaPlayback(update media.PlaybackUpdate) error {
	if a.engine == nil {
		return nil
	}
	return a.engine.MediaService().UpdatePlayback(update)
}

func (a *App) GetMediaStats(token string) (media.MediaStats, error) {
	if a.engine == nil {
		return media.MediaStats{}, nil
	}
	return a.engine.MediaService().Stats(token), nil
}
