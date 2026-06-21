package main

import (
	"fmt"
	"os"
	"path/filepath"

	"TDrive/backend"
	"TDrive/backend/media"
	"TDrive/backend/thumbnail"
)

// thumbnailCacheMaxBytes caps the on-disk thumbnail cache. Thumbnails are
// regenerable, so this is a soft budget the cache evicts down to (LRU).
const thumbnailCacheMaxBytes int64 = 512 * 1024 * 1024

// thumbnailCacheDir is where generated thumbnails live. It sits under the OS
// cache directory because the contents are disposable; encrypted-drive
// thumbnails are stored as ciphertext regardless.
func thumbnailCacheDir() string {
	base, err := os.UserCacheDir()
	if err != nil || base == "" {
		base = os.TempDir()
	}
	return filepath.Join(base, "TDrive", "thumbnails")
}

func newThumbnailCache() *thumbnail.Cache {
	return thumbnail.NewCache(thumbnailCacheDir(), thumbnailCacheMaxBytes)
}

// ListMedia returns every image in the active drive, newest first, for the
// gallery view. Non-image files are filtered out in the read service.
func (a *App) ListMedia() ([]backend.FileMetaData, error) {
	files, err := a.readService().MediaFiles(a.ActiveChannelID())
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
		})
	}
	return out, nil
}

// Thumbnail returns a small JPEG preview for one image, base64-encoded for the
// frontend to turn into a data URL. Cheap on a cache hit; on a miss it pulls
// and downscales the original once.
func (a *App) Thumbnail(msgID int) (PreviewPayload, error) {
	payload, err := a.fileService().Thumbnail(a.ctx, a.ActiveChannelID(), msgID)
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

func (a *App) CloseMedia(token string) error {
	if a.engine == nil {
		return fmt.Errorf("backend not ready")
	}
	return a.engine.MediaService().CloseSession(token)
}
