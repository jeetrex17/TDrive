package file

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/subtle"

	tdcrypto "TDrive/backend/crypto"
)

const defaultThumbConcurrency = 3

var encryptedThumbCacheMagic = []byte("tdrive-thumb-cache-v2\x00")

const encryptedThumbCacheHashSize = sha256.Size

// Thumbnail preserves the legacy bridge contract while using the same bounded
// derivative pipeline as the binary gallery. Missing derivatives are explicit;
// browsing never downloads an original to construct a thumbnail.
func (s *Service) Thumbnail(ctx context.Context, channelID int64, msgID int) (PreviewPayload, error) {
	result, err := s.Rendition(ctx, channelID, int64(msgID), 0, "thumbnail")
	if err != nil {
		return PreviewPayload{}, err
	}
	return previewPayloadFromBytes(result.Bytes, result.MimeType)
}

// readThumbCache returns decoded JPEG bytes from the cache, decrypting first
// for encrypted drives. A corrupt or wrong-key entry is reported as a miss so
// the caller regenerates.
func (s *Service) readThumbCache(cacheKey string, encrypted bool, masterKey []byte) ([]byte, bool) {
	if s.Thumbs == nil {
		return nil, false
	}
	raw, ok := s.Thumbs.GetLimited(cacheKey, renditionPreviewLimit+(64<<10))
	if !ok {
		return nil, false
	}
	if !encrypted {
		return raw, true
	}
	plain := &renditionWriter{limit: renditionPreviewLimit + len(encryptedThumbCacheMagic) + encryptedThumbCacheHashSize}
	if _, err := tdcrypto.DecryptStream(bytes.NewReader(raw), plain, masterKey); err != nil {
		return nil, false
	}
	decoded := plain.Bytes()
	headerSize := len(encryptedThumbCacheMagic) + encryptedThumbCacheHashSize
	if len(decoded) <= headerSize || !bytes.Equal(decoded[:len(encryptedThumbCacheMagic)], encryptedThumbCacheMagic) {
		return nil, false
	}
	wantHash := sha256.Sum256([]byte(cacheKey))
	if subtle.ConstantTimeCompare(decoded[len(encryptedThumbCacheMagic):headerSize], wantHash[:]) != 1 {
		return nil, false
	}
	return bytes.Clone(decoded[headerSize:]), true
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
		cacheHash := sha256.Sum256([]byte(cacheKey))
		boundPlaintext := make([]byte, 0, len(encryptedThumbCacheMagic)+len(cacheHash)+len(jpegBytes))
		boundPlaintext = append(boundPlaintext, encryptedThumbCacheMagic...)
		boundPlaintext = append(boundPlaintext, cacheHash[:]...)
		boundPlaintext = append(boundPlaintext, jpegBytes...)
		var enc bytes.Buffer
		err := tdcrypto.EncryptStream(bytes.NewReader(boundPlaintext), &enc, masterKey, int64(len(boundPlaintext)))
		clear(boundPlaintext)
		if err != nil {
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
		s.thumbSem = make(chan struct{}, min(n, 4))
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
