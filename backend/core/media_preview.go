package core

import (
	"bytes"
	"context"
	"errors"

	"TDrive/backend/media"
	fileservice "TDrive/backend/services/file"
	"TDrive/backend/thumbnail"
)

// Prefer an existing revision-bound rendition over transferring the original.
// The file service owns sidecar selection, authorization, cache and coalescing.
func (e *Engine) loadImagePreview(ctx context.Context, source media.LogicalFile) ([]byte, error) {
	rendition, err := e.FileService().Rendition(ctx, source.ChannelID, source.FileID, source.Revision, "preview")
	if err != nil {
		if errors.Is(err, fileservice.ErrRenditionStale) {
			return nil, media.ErrStaleRevision
		}
		code, _ := fileservice.RenditionError(err)
		if code == "missing_rendition" || code == "unsupported" {
			return nil, media.ErrImagePreviewMissing
		}
		return nil, err
	}
	if rendition.MimeType != "image/jpeg" {
		// This input is already limited to a static 1600px rendition. Reuse the
		// bounded encoder so every viewer fallback has a single safe format.
		return thumbnail.Generate(rendition.Bytes, 1600)
	}
	// A rendition flight can share its result with another subscriber. The
	// viewing session owns and clears only its own copy at close/vault lock.
	return bytes.Clone(rendition.Bytes), nil
}
