package media

import (
	"context"
	"errors"
	"fmt"
	"image"
	"io"

	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"

	_ "golang.org/x/image/bmp"
	_ "golang.org/x/image/webp"
)

const (
	defaultMaxImageStreamBytes  int64 = 256 << 20
	defaultMaxImageStreamPixels int64 = 32_000_000
	defaultMaxImageHeaderBytes  int64 = 4 << 20
	imageAnimationProbeBytes          = 32
)

// ImageAdmissionLimits bounds the resources a webview may spend decoding an
// original image for direct display. These are not upload, source, or download
// limits: a rejected original remains available through the download flow.
// DecodeConfig reads through the random-access session, so the check works for
// multipart and encrypted files without materializing them. Zero fields use
// conservative defaults.
type ImageAdmissionLimits struct {
	MaxBytes       int64
	MaxPixels      int64
	MaxHeaderBytes int64
}

func (limits ImageAdmissionLimits) normalized() ImageAdmissionLimits {
	if limits.MaxBytes <= 0 {
		limits.MaxBytes = defaultMaxImageStreamBytes
	}
	if limits.MaxPixels <= 0 {
		limits.MaxPixels = defaultMaxImageStreamPixels
	}
	if limits.MaxHeaderBytes <= 0 {
		limits.MaxHeaderBytes = defaultMaxImageHeaderBytes
	}
	return limits
}

func validateImageMetadata(file LogicalFile, limits ImageAdmissionLimits) error {
	limits = limits.normalized()
	if file.PlaintextSize <= 0 {
		return ErrInvalidImage
	}
	if file.PlaintextSize > limits.MaxBytes {
		return fmt.Errorf("%w: %d bytes exceeds %d", ErrImageTooLarge, file.PlaintextSize, limits.MaxBytes)
	}
	return nil
}

func admitImage(ctx context.Context, session *Session, name string, limits ImageAdmissionLimits) error {
	if session == nil {
		return ErrSessionNotFound
	}
	limits = limits.normalized()
	if err := validateImageMetadata(session.file, limits); err != nil {
		return err
	}
	info, ok := streamTypeForName(name)
	if !ok || info.kind != StreamKindImage || info.imageFormat == "" {
		return ErrUnsupportedMediaType
	}

	reader := &sessionImageHeaderReader{
		ctx:       ctx,
		session:   session,
		remaining: min(limits.MaxHeaderBytes, session.Size()),
	}
	config, format, err := image.DecodeConfig(reader)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return err
		}
		return fmt.Errorf("%w: decode header", ErrInvalidImage)
	}
	if format != info.imageFormat {
		return fmt.Errorf("%w: extension expects %s, content is %s", ErrInvalidImage, info.imageFormat, format)
	}
	if err := rejectUnsafeAnimation(ctx, session, format); err != nil {
		return err
	}
	if config.Width <= 0 || config.Height <= 0 {
		return ErrInvalidImage
	}
	if int64(config.Width) > limits.MaxPixels/int64(config.Height) {
		return fmt.Errorf("%w: %dx%d pixels exceeds %d", ErrImageTooLarge, config.Width, config.Height, limits.MaxPixels)
	}
	return nil
}

// rejectUnsafeAnimation keeps the WebView from accepting inputs whose decoded
// memory scales with attacker-controlled frame counts. GIF does not expose a
// trustworthy bounded frame declaration near the header, so all GIF originals
// stay downloadable but are excluded from direct display. Extended WebP has an
// authoritative animation feature bit in its fixed prefix.
func rejectUnsafeAnimation(ctx context.Context, session *Session, format string) error {
	if format == "gif" {
		return ErrAnimatedImageUnsafe
	}
	if format != "webp" {
		return nil
	}
	prefixSize := min(int64(imageAnimationProbeBytes), session.Size())
	prefix := make([]byte, int(prefixSize))
	n, err := session.ReadAt(ctx, prefix, 0)
	if err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	prefix = prefix[:n]
	if animatedWebP(prefix) {
		return ErrAnimatedImageUnsafe
	}
	return nil
}

func animatedWebP(prefix []byte) bool {
	return len(prefix) > 20 &&
		string(prefix[0:4]) == "RIFF" &&
		string(prefix[8:12]) == "WEBP" &&
		string(prefix[12:16]) == "VP8X" &&
		prefix[20]&0x02 != 0
}

type sessionImageHeaderReader struct {
	ctx       context.Context
	session   *Session
	offset    int64
	remaining int64
}

func (reader *sessionImageHeaderReader) Read(dst []byte) (int, error) {
	if err := reader.ctx.Err(); err != nil {
		return 0, err
	}
	if reader.remaining <= 0 {
		return 0, io.EOF
	}
	if int64(len(dst)) > reader.remaining {
		dst = dst[:reader.remaining]
	}
	n, err := reader.session.ReadAt(reader.ctx, dst, reader.offset)
	reader.offset += int64(n)
	reader.remaining -= int64(n)
	if n > 0 && errors.Is(err, io.EOF) {
		err = nil
	}
	return n, err
}
