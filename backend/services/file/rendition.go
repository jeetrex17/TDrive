package file

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"TDrive/backend/projection"
	"TDrive/backend/tgclient"
	"TDrive/backend/thumbnail"
)

const (
	renditionThumbLimit     = 1 << 20
	renditionPreviewLimit   = 4 << 20
	renditionOriginalLimit  = 16 << 20
	renditionOriginalPixels = 16_000_000
)

var (
	ErrRenditionMissing = errors.New("missing_rendition")
	ErrRenditionStale   = errors.New("stale_revision")
	ErrRenditionBusy    = errors.New("rendition_busy")
	// ErrEncryptedRenditionScope rejects encrypted rows outside My Drive before
	// any cache or Telegram access.
	ErrEncryptedRenditionScope = errors.New("encrypted_rendition_outside_personal_drive")
)

// Rendition is immutable compressed image data. Width and Height are checked
// before the browser receives bytes, allowing a shared decoded-pixel budget.
type Rendition struct {
	Bytes     []byte
	MimeType  string
	Width     int
	Height    int
	Encrypted bool
}

// Rendition reads a purpose-sized remote derivative. Grid and preview requests
// never download originals. Only the explicit original class may read one,
// and both compressed bytes and decoded dimensions are bounded.
func (s *Service) Rendition(ctx context.Context, channelID, msgID, revision int64, kind string) (Rendition, error) {
	if ctx == nil || channelID == 0 || msgID <= 0 {
		return Rendition{}, errPreviewNotFound
	}
	if kind != "thumbnail" && kind != "preview" && kind != "original" {
		return Rendition{}, errPreviewNotSupported
	}
	if s.TG == nil || s.Peers == nil {
		return Rendition{}, errPreviewDownloadFailed
	}
	f, found, err := projection.FileByID(s.DB, channelID, msgID)
	if err != nil {
		return Rendition{}, err
	}
	if !found {
		return Rendition{}, errPreviewNotFound
	}
	if revision > 0 && revision != f.Revision {
		return Rendition{}, ErrRenditionStale
	}
	if !thumbnail.IsImage(f.Name) && !(kind == "thumbnail" && isVideoDocument(f.Name)) {
		return Rendition{}, errPreviewNotSupported
	}
	namespace, err := s.renditionNamespace(ctx)
	if err != nil {
		return Rendition{}, err
	}
	// Thumbnail data is deliberately local-cache then Telegram's native
	// document thumb. Old durable sidecars remain readable for the legacy
	// preview class, but never outrank the source document's thumbnail.
	var ref *projection.FileRendition
	if kind != "thumbnail" {
		ref, err = s.renditionReference(ctx, f, kind)
		if err != nil {
			return Rendition{}, err
		}
	}
	derivativeID := int64(0)
	if ref != nil {
		derivativeID = ref.MsgID
	}
	key := renditionCacheKey(namespace, f, kind, derivativeID)
	// Authorize before all cache reads, including encrypted entries already on
	// disk. Each subscriber owns and clears its own short-lived key copy.
	masterKey, err := s.renditionKey(f.ChannelID, f.Encrypted)
	if err != nil {
		return Rendition{}, err
	}
	cached, ok := s.readThumbCache(key, f.Encrypted, masterKey)
	clearOwnedKey(masterKey)
	if ok {
		if result, err := checkedRendition(cached, kind, f.Encrypted); err == nil {
			return result, nil
		}
	}
	return s.renditionFlights.do(ctx, key, func(flightCtx context.Context) (Rendition, error) {
		return s.loadRendition(flightCtx, f, kind, ref, key)
	})
}

// Video gallery tiles request Telegram's bounded document thumbnail only. They
// never admit a video preview or original through the image rendition route.
func isVideoDocument(name string) bool {
	switch strings.TrimPrefix(strings.ToLower(filepath.Ext(strings.TrimSpace(name))), ".") {
	case "mp4", "m4v", "mov", "qt", "webm", "mkv", "mk3d", "avi", "ts", "m2ts", "mts", "flv", "wmv", "ogv", "mpeg", "mpg":
		return true
	default:
		return false
	}
}

// loadRendition owns its key and transfer slot for the complete shared flight.
// No caller-owned key can be zeroed while another subscriber still needs it.
func (s *Service) loadRendition(ctx context.Context, f projection.File, kind string, ref *projection.FileRendition, cacheKey string) (Rendition, error) {
	key, err := s.renditionKey(f.ChannelID, f.Encrypted)
	defer clearOwnedKey(key)
	if err != nil {
		return Rendition{}, err
	}
	if raw, ok := s.readThumbCache(cacheKey, f.Encrypted, key); ok {
		if result, err := checkedRendition(raw, kind, f.Encrypted); err == nil {
			return result, nil
		}
	}
	if err := s.acquireThumbSlot(ctx); err != nil {
		return Rendition{}, err
	}
	defer s.releaseThumbSlot()
	result, err := s.fetchRendition(ctx, f, kind, ref, key)
	if err != nil {
		return Rendition{}, err
	}
	if err := ctx.Err(); err != nil {
		return Rendition{}, err
	}
	// Re-check after I/O. A replacement racing this request must never publish
	// old pixels under the new logical gallery identity.
	current, found, err := projection.FileByID(s.DB, f.ChannelID, f.MsgID)
	if err != nil {
		return Rendition{}, err
	}
	if !found || current.Revision != f.Revision {
		return Rendition{}, ErrRenditionStale
	}
	if kind != "original" {
		s.writeThumbCache(cacheKey, result.Bytes, f.Encrypted, key)
	}
	return result, nil
}

// The database epoch isolates resets; the authenticated actor additionally
// isolates soft logout followed by signing into another account using that DB.
func (s *Service) renditionNamespace(ctx context.Context) (string, error) {
	namespace := s.CacheNamespace
	if namespace == "" {
		namespace = fmt.Sprintf("session-%p", s.DB)
	}
	if s.ActorID != nil {
		actor, err := s.ActorID(ctx)
		if err != nil {
			return "", err
		}
		if actor <= 0 {
			return "", errPreviewDownloadFailed
		}
		namespace += fmt.Sprintf(":actor:%d", actor)
	}
	return namespace, nil
}

func (s *Service) renditionKey(channelID int64, encrypted bool) ([]byte, error) {
	if !encrypted {
		return nil, nil
	}
	if s.PersonalChannelID != nil && (channelID == 0 || channelID != s.PersonalChannelID()) {
		return nil, ErrEncryptedRenditionScope
	}
	var (
		key []byte
		err error
	)
	if s.RequireEncryptionKeyForChannel != nil {
		key, err = s.RequireEncryptionKeyForChannel(channelID, true)
	} else {
		key, err = s.requireEncryptionKey(true)
	}
	if err != nil || len(key) == 0 {
		clearOwnedKey(key)
		return nil, errPreviewEncryptionPasswordRequired
	}
	return key, nil
}
func renditionCacheKey(namespace string, f projection.File, kind string, derivativeID int64) string {
	identity := fmt.Sprintf("%s/%d/%d/%d/%s/%s/%d/%t/%s/%d/v2", namespace, f.ChannelID, f.MsgID, f.ContentMsgID, f.ContentHash, f.UploadUUID, f.Revision, f.Encrypted, kind, derivativeID)
	return fmt.Sprintf("%x", sha256.Sum256([]byte(identity)))
}
func checkedRendition(raw []byte, kind string, encrypted bool) (Rendition, error) {
	limit, maxPixels := renditionThumbLimit, int64(512*512)
	switch kind {
	case "preview":
		limit = renditionPreviewLimit
		maxPixels = 1600 * 1600
	case "original":
		limit = renditionOriginalLimit
		maxPixels = renditionOriginalPixels
	}
	if len(raw) == 0 {
		return Rendition{}, ErrRenditionMissing
	}
	if len(raw) > limit {
		return Rendition{}, errPreviewTooLarge
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(raw))
	if err != nil {
		return Rendition{}, errPreviewNotSupported
	}
	maxEdge := 512
	if kind == "preview" {
		maxEdge = 1600
	}
	if cfg.Width <= 0 || cfg.Height <= 0 || int64(cfg.Width) > maxPixels/int64(cfg.Height) || (kind != "original" && (cfg.Width > maxEdge || cfg.Height > maxEdge)) {
		return Rendition{}, errPreviewTooLarge
	}
	// Animated originals can retain many decoded frames. Originals use the
	// existing explicit export path for these formats; derivatives are static.
	if (format != "jpeg" && format != "png" && format != "webp") || animatedRendition(raw, format) {
		return Rendition{}, errPreviewNotSupported
	}
	return Rendition{Bytes: raw, MimeType: http.DetectContentType(raw), Width: cfg.Width, Height: cfg.Height, Encrypted: encrypted}, nil
}
func (s *Service) fetchRendition(ctx context.Context, f projection.File, kind string, ref *projection.FileRendition, key []byte) (Rendition, error) {
	peer, err := s.Peers.ResolvePeer(ctx, f.ChannelID)
	if err != nil {
		return Rendition{}, err
	}
	if kind == "original" {
		return s.fetchOriginalRendition(ctx, peer, f, key)
	}
	if ref != nil {
		return s.fetchStoredRendition(ctx, peer, f, *ref, key)
	}
	if f.Encrypted || f.ContentMsgID <= 0 {
		return Rendition{}, ErrRenditionMissing
	}
	return s.fetchTelegramThumbnail(ctx, peer, f, kind)
}
func (s *Service) fetchTelegramThumbnail(ctx context.Context, peer tgclient.InputPeer, f projection.File, kind string) (Rendition, error) {
	var doc tgclient.FileDocument
	err := s.sendRetryPolicy().Do(ctx, func() error { var err error; doc, err = s.TG.GetFileDocument(ctx, peer, f.ContentMsgID); return err })
	if err != nil {
		return Rendition{}, err
	}
	// Prefer a complete inline thumbnail. Telegram stripped previews cannot be
	// decoded standalone and are ignored without falling back to the original.
	if inline, ok := inlineRendition(doc.Thumbs, kind); ok {
		return inline, nil
	}
	thumbType := remoteRenditionType(doc.Thumbs, kind)
	if thumbType == "" {
		return Rendition{}, ErrRenditionMissing
	}
	var raw []byte
	err = s.sendRetryPolicy().Do(ctx, func() error {
		limit := renditionThumbLimit
		if kind == "preview" {
			limit = renditionPreviewLimit
		}
		dst := &renditionWriter{limit: limit}
		if err := s.TG.DownloadFileThumbnail(ctx, peer, f.ContentMsgID, thumbType, dst); err != nil {
			return err
		}
		raw = dst.Bytes()
		return nil
	})
	if err != nil {
		return Rendition{}, err
	}
	return checkedRendition(raw, kind, false)
}

// Prefer the largest bounded complete thumbnail. Tiny inline placeholders
// should not win merely because Telegram listed them first.
func inlineRendition(thumbs []tgclient.FileThumb, kind string) (Rendition, bool) {
	var best Rendition
	for _, thumb := range thumbs {
		if len(thumb.Bytes) == 0 {
			continue
		}
		result, err := checkedRendition(thumb.Bytes, kind, false)
		if err == nil && result.Width*result.Height > best.Width*best.Height {
			best = result
		}
	}
	return best, len(best.Bytes) > 0
}
func remoteRenditionType(thumbs []tgclient.FileThumb, kind string) string {
	maxEdge := int64(512)
	if kind == "preview" {
		maxEdge = 1600
	}
	bestType, bestScore := "", int64(-1)
	for _, thumb := range thumbs {
		if len(thumb.Bytes) > 0 || thumb.Type == "" {
			continue
		}
		width, height := int64(thumb.Width), int64(thumb.Height)
		if width < 0 || height < 0 || width > maxEdge || height > maxEdge {
			continue
		}
		pixels := width * height
		if pixels > bestScore {
			bestType = thumb.Type
			bestScore = pixels
		}
	}
	return bestType
}

// renditionWriter enforces the limit during transfer, rather than after a
// bytes.Buffer has already grown to an attacker-controlled document size.
type renditionWriter struct {
	buffer bytes.Buffer
	limit  int
}

func (w *renditionWriter) Bytes() []byte { return w.buffer.Bytes() }
func (w *renditionWriter) Len() int      { return w.buffer.Len() }

func (w *renditionWriter) Write(p []byte) (int, error) {
	if len(p) > w.limit-w.Len() {
		return 0, errPreviewTooLarge
	}
	return w.buffer.Write(p)
}

// RenditionError exposes stable transport codes without leaking Telegram or
// database details to the embedded browser.
func RenditionError(err error) (string, int) {
	switch {
	case errors.Is(err, ErrRenditionMissing), errors.Is(err, errPreviewNotFound):
		return "missing_rendition", 404
	case errors.Is(err, ErrRenditionStale):
		return "stale_revision", 409
	case errors.Is(err, tgclient.ErrFloodWait):
		return "flood_wait", 429
	case errors.Is(err, ErrRenditionBusy):
		return "busy", 429
	case errors.Is(err, errPreviewEncryptionPasswordRequired):
		return "locked", 423
	case errors.Is(err, errPreviewTooLarge):
		return "too_large", 413
	case errors.Is(err, errPreviewNotSupported):
		return "unsupported", 415
	case errors.Is(err, context.Canceled):
		return "canceled", 410
	case errors.Is(err, context.DeadlineExceeded):
		return "timeout", 504
	default:
		return "unavailable", 503
	}
}

// RenditionRetryAfter preserves Telegram's complete server-requested deadline.
// Frontends must not shorten it to their ordinary transient retry backoff.
func RenditionRetryAfter(err error) time.Duration {
	if wait, ok := tgclient.FloodWaitDuration(err); ok {
		return wait
	}
	if errors.Is(err, ErrRenditionBusy) {
		return time.Second
	}
	return 0
}

// Browsers may retain every frame of APNG/WebP animations. Reject their
// animation chunks so a single image's pixel budget cannot multiply silently.
func animatedRendition(raw []byte, format string) bool {
	offset := 8
	if format == "webp" {
		offset = 12
	}
	if format != "png" && format != "webp" {
		return false
	}
	for offset+8 <= len(raw) {
		size := uint64(binary.BigEndian.Uint32(raw[offset:]))
		chunk := string(raw[offset+4 : offset+8])
		overhead := uint64(12)
		if format == "webp" {
			size = uint64(binary.LittleEndian.Uint32(raw[offset+4:]))
			chunk = string(raw[offset : offset+4])
			overhead = 8
			size += size % 2
		}
		if chunk == "acTL" || chunk == "ANIM" || chunk == "ANMF" {
			return true
		}
		next := uint64(offset) + size + overhead
		if next > uint64(len(raw)) {
			return false
		}
		offset = int(next)
	}
	return false
}
