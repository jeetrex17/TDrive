package mountwrite

import (
	"context"
	"io"
	"log/slog"
)

// RenditionPreparer is an optional post-commit capability. It receives the
// immutable stored source while the journal still owns staging, so mounted
// replacements can publish previews without downloading their just-sent body.
// Errors cannot change the already committed write's outcome.
type RenditionPreparer interface {
	PrepareCommittedRenditions(context.Context, HiddenUpload, MutationResult, io.ReadSeeker) error
}

func (c *Coordinator) prepareCommittedRenditions(ctx context.Context, record JournalRecord, result MutationResult) {
	preparer, ok := c.remote.(RenditionPreparer)
	if !ok || record.Staged == nil || record.Mutation.Kind != MutationPut {
		return
	}
	source, err := c.staging.Open(*record.Staged)
	if err != nil {
		slog.Warn("mountwrite: preview preparation deferred", "error", err)
		return
	}
	defer source.Close()
	if err := preparer.PrepareCommittedRenditions(ctx, hiddenUploadFromRecord(record), result, source); err != nil {
		slog.Warn("mountwrite: preview preparation deferred", "error", err)
	}
}
