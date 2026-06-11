package file

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"time"

	tdcrypto "TDrive/backend/crypto"
	"TDrive/backend/projection"
	"TDrive/backend/tgclient"
	"TDrive/backend/thumbnail"
)

const (
	// thumbCacheVersion is baked into every cache key. Bump it to invalidate
	// all previously cached thumbnails when the generation parameters change.
	thumbCacheVersion = 1
	thumbMaxEdge      = thumbnail.DefaultMaxEdge

	// maxThumbSourceBytes caps how large an original we will pull just to make
	// a thumbnail. Bigger files fall back to a placeholder in the grid. Note
	// this bounds download volume only; peak decode memory is governed by
	// thumbnail.maxSourcePixels (a small file can still decode to many pixels).
	maxThumbSourceBytes int64 = 30 * 1024 * 1024

	// defaultThumbConcurrency bounds simultaneous generations. Each in-flight
	// generation can hold a full decoded source image in memory (worst case
	// ~96 MB for a 24 MP image at 4 bytes/px), so this is kept low to bound
	// peak memory while still overlapping network-bound downloads.
	defaultThumbConcurrency = 3

	// thumbGenTimeout bounds a single detached generation so an abandoned
	// flight cannot run forever.
	thumbGenTimeout = 60 * time.Second
)

// Thumbnail returns a small JPEG preview of an image file for the gallery
// grid. Results are cached on disk: encrypted-drive thumbnails are stored as
// ciphertext under the drive's master key, so a locked vault yields a
// "password required" error rather than a thumbnail.
//
// Generation runs off previewMu with bounded concurrency, so a scrolling grid
// fills in parallel without blocking single-file previews or downloads.
func (s *Service) Thumbnail(ctx context.Context, channelID int64, msgID int) (PreviewPayload, error) {
	if msgID <= 0 {
		return PreviewPayload{}, errPreviewNotFound
	}
	if s.TG == nil || s.Peers == nil {
		return PreviewPayload{}, errPreviewDownloadFailed
	}

	// Defensive: the gallery only asks for images, but a stray non-image
	// msgID should fail cleanly instead of downloading a whole document.
	if name := s.lookupStoredFilename(channelID, msgID, tgclient.FileDocument{}); name != "" && !thumbnail.IsImage(name) {
		return PreviewPayload{}, errPreviewNotSupported
	}

	encrypted := false
	if enc, _, _, err := projection.FileEncryptionMeta(s.DB, channelID, int64(msgID)); err == nil {
		encrypted = enc
	}

	var masterKey []byte
	if encrypted {
		key, err := s.requireEncryptionKey(true)
		if err != nil {
			return PreviewPayload{}, errPreviewEncryptionPasswordRequired
		}
		masterKey = key
	}

	cacheKey := thumbCacheKey(channelID, msgID)

	if jpegBytes, ok := s.readThumbCache(cacheKey, encrypted, masterKey); ok {
		return previewPayloadFromBytes(jpegBytes, "image/jpeg")
	}

	// Collapse duplicate concurrent requests for the same image into one
	// download+generate. The shared work runs under its own background-derived
	// context so one subscriber scrolling away (canceling its ctx) does not
	// fail the others still waiting on the same image; each caller still
	// observes its own ctx via the select below. An abandoned flight still
	// finishes and populates the cache for the next view.
	ch := s.thumbGroup.DoChan(cacheKey, func() (any, error) {
		if jpegBytes, ok := s.readThumbCache(cacheKey, encrypted, masterKey); ok {
			return jpegBytes, nil
		}
		genCtx, cancel := context.WithTimeout(context.Background(), thumbGenTimeout)
		defer cancel()
		return s.generateThumbnail(genCtx, channelID, msgID, cacheKey, encrypted, masterKey)
	})

	select {
	case <-ctx.Done():
		return PreviewPayload{}, normalizePreviewError(ctx.Err())
	case res := <-ch:
		if res.Err != nil {
			return PreviewPayload{}, normalizePreviewError(res.Err)
		}
		jpegBytes, _ := res.Val.([]byte)
		return previewPayloadFromBytes(jpegBytes, "image/jpeg")
	}
}

func (s *Service) generateThumbnail(ctx context.Context, channelID int64, msgID int, cacheKey string, encrypted bool, masterKey []byte) ([]byte, error) {
	if err := s.acquireThumbSlot(ctx); err != nil {
		return nil, err
	}
	defer s.releaseThumbSlot()

	peer, err := s.Peers.ResolvePeer(ctx, channelID)
	if err != nil {
		return nil, errPreviewDownloadFailed
	}

	doc, err := s.TG.GetFileDocument(ctx, peer, int64(msgID))
	if err != nil {
		if errors.Is(err, tgclient.ErrMessageNotFound) || errors.Is(err, tgclient.ErrNotFile) || errors.Is(err, tgclient.ErrEmptyDocument) {
			return nil, errPreviewNotFound
		}
		return nil, errPreviewDownloadFailed
	}
	// Re-check against the real document name. The cheap pre-check in Thumbnail
	// only sees the projection; a msgID that isn't projected yet reaches here,
	// and we must not download a whole non-image document.
	if name := s.lookupStoredFilename(channelID, msgID, doc); name == "" || !thumbnail.IsImage(name) {
		return nil, errPreviewNotSupported
	}
	if doc.Size <= 0 {
		return nil, errPreviewNotFound
	}
	if doc.Size > maxThumbSourceBytes {
		return nil, errPreviewTooLarge
	}

	var buf bytes.Buffer
	if err := s.TG.DownloadFile(ctx, peer, int64(msgID), &buf, nil); err != nil {
		return nil, errPreviewDownloadFailed
	}

	// Decode source. For encrypted drives the download is ciphertext that must
	// be decrypted first. Assign source per-branch so we never alias buf's
	// backing array after handing buf to DecryptStream.
	var source []byte
	if encrypted {
		var plain bytes.Buffer
		if _, err := tdcrypto.DecryptStream(&buf, &plain, masterKey); err != nil {
			return nil, errPreviewDownloadFailed
		}
		source = plain.Bytes()
	} else {
		source = buf.Bytes()
	}

	jpegBytes, err := thumbnail.Generate(source, thumbMaxEdge)
	if err != nil {
		if errors.Is(err, thumbnail.ErrTooLarge) {
			return nil, errPreviewTooLarge
		}
		return nil, errPreviewNotSupported
	}

	s.writeThumbCache(cacheKey, jpegBytes, encrypted, masterKey)
	return jpegBytes, nil
}

// readThumbCache returns decoded JPEG bytes from the cache, decrypting first
// for encrypted drives. A corrupt or wrong-key entry is reported as a miss so
// the caller regenerates.
func (s *Service) readThumbCache(cacheKey string, encrypted bool, masterKey []byte) ([]byte, bool) {
	if s.Thumbs == nil {
		return nil, false
	}
	raw, ok := s.Thumbs.Get(cacheKey)
	if !ok {
		return nil, false
	}
	if !encrypted {
		return raw, true
	}
	var plain bytes.Buffer
	if _, err := tdcrypto.DecryptStream(bytes.NewReader(raw), &plain, masterKey); err != nil {
		return nil, false
	}
	return plain.Bytes(), true
}

// writeThumbCache stores a generated thumbnail, encrypting it under the
// drive's master key first when the source is encrypted. Cache write failures
// are non-fatal: the thumbnail still returns, it just is not persisted.
func (s *Service) writeThumbCache(cacheKey string, jpegBytes []byte, encrypted bool, masterKey []byte) {
	if s.Thumbs == nil {
		return
	}
	value := jpegBytes
	if encrypted {
		var enc bytes.Buffer
		if err := tdcrypto.EncryptStream(bytes.NewReader(jpegBytes), &enc, masterKey, int64(len(jpegBytes))); err != nil {
			return
		}
		value = enc.Bytes()
	}
	_ = s.Thumbs.Put(cacheKey, value)
}

func (s *Service) acquireThumbSlot(ctx context.Context) error {
	s.thumbOnce.Do(func() {
		n := s.ThumbConcurrency
		if n <= 0 {
			n = defaultThumbConcurrency
		}
		s.thumbSem = make(chan struct{}, n)
	})
	select {
	case s.thumbSem <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *Service) releaseThumbSlot() {
	<-s.thumbSem
}

// thumbCacheKey identifies a cached thumbnail. channelID namespaces across
// drives and msgID identifies the file; Telegram never reuses a msg id within
// a channel, so a key always maps to the same image. The version suffix lets
// us invalidate every cached thumbnail at once when generation params change.
func thumbCacheKey(channelID int64, msgID int) string {
	return fmt.Sprintf("%d-%d-v%d", channelID, msgID, thumbCacheVersion)
}
