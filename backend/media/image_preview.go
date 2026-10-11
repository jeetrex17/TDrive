package media

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"io"

	"TDrive/backend/thumbnail"
)

const (
	imagePreviewMaxEdge  = 1600
	imagePreviewMaxBytes = 4 << 20
)

// Only an explicit image-viewer open may substitute resized bytes. Other media
// streams retain their original-byte contract and direct-display admission.
func (s *Service) admitViewerImage(ctx context.Context, session *Session, allowPreview bool) error {
	mime, err := admitImage(ctx, session, session.Name(), s.imageLimits)
	if err == nil {
		session.mimeType = mime
		return nil
	}
	if !allowPreview || !errors.Is(err, ErrImageTooLarge) {
		return err
	}
	preview, err := s.loadImagePreview(ctx, session)
	if err != nil {
		return err
	}
	if err := validateImagePreview(preview); err != nil {
		clear(preview)
		return err
	}
	if err := ctx.Err(); err != nil {
		clear(preview)
		return err
	}
	// Keep original identity, size and encryption metadata for download and
	// revocation. Only the bytes served through this viewing session change.
	session.previewData = preview
	session.previewSize = int64(len(preview))
	session.mimeType = "image/jpeg"
	return nil
}

func (s *Service) loadImagePreview(ctx context.Context, session *Session) ([]byte, error) {
	if s.imagePreview != nil && session.file.SourceKind == "" {
		preview, err := s.imagePreview(ctx, session.file)
		if err == nil {
			return preview, nil
		}
		clear(preview)
		if !errors.Is(err, ErrImagePreviewMissing) {
			return nil, err
		}
	}
	if session.Size() > thumbnail.MaxLocalSourceBytes {
		return nil, fmt.Errorf("%w: source exceeds resized-preview transfer budget", ErrImageTooLarge)
	}
	// The existing decoder serializes work and samples camera JPEGs on native
	// hosts. Encrypted plaintext remains in memory and never reaches a temp file.
	source := io.NewSectionReader(imageSessionReader{ctx: ctx, session: session}, 0, session.Size())
	preview, err := thumbnail.GenerateLocal(ctx, source, imagePreviewMaxEdge)
	if err != nil {
		return nil, fmt.Errorf("media: could not create resized preview: %w", err)
	}
	return preview, nil
}

func validateImagePreview(raw []byte) error {
	if len(raw) == 0 || len(raw) > imagePreviewMaxBytes {
		return ErrImageTooLarge
	}
	config, format, err := image.DecodeConfig(bytes.NewReader(raw))
	if err != nil || format != "jpeg" || config.Width <= 0 || config.Height <= 0 {
		return ErrInvalidImage
	}
	if config.Width > imagePreviewMaxEdge || config.Height > imagePreviewMaxEdge {
		return ErrImageTooLarge
	}
	return nil
}

type imageSessionReader struct {
	ctx     context.Context
	session *Session
}

func (r imageSessionReader) ReadAt(p []byte, off int64) (int, error) {
	return r.session.ReadAt(r.ctx, p, off)
}
