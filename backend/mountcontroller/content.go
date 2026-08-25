package mountcontroller

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"TDrive/backend/media"
	"TDrive/backend/mountcontent"
	"TDrive/backend/mountfs"
	"TDrive/backend/tgclient"
)

type contentAdapter struct {
	opener *mountcontent.Opener
}

func (adapter contentAdapter) OpenContent(ctx context.Context, channelID int64, entry mountfs.SourceEntry) (mountfs.RandomAccessContent, error) {
	if ctx == nil {
		return nil, fmt.Errorf("%w: content context is missing", mountfs.ErrContentUnavailable)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if adapter.opener == nil {
		return nil, fmt.Errorf("%w: content reader is not ready", mountfs.ErrContentUnavailable)
	}
	messageID, err := strconv.ParseInt(entry.ContentRef, 10, 64)
	if err != nil || messageID <= 0 {
		return nil, fmt.Errorf("%w: invalid content reference", mountfs.ErrContentUnavailable)
	}
	reader, err := adapter.opener.Open(ctx, channelID, messageID)
	if err != nil {
		return nil, mapContentOpenError(err)
	}
	if reader.Size() != entry.Size {
		_ = reader.Close()
		return nil, fmt.Errorf("%w: projected content size does not match the resolved body", mountfs.ErrContentUnavailable)
	}
	return reader, nil
}

func mapContentOpenError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return err
	case errors.Is(err, mountcontent.ErrEncryptedUnsupported), errors.Is(err, media.ErrEncryptedUnsupported):
		return fmt.Errorf("%w: encrypted file format is unsupported", mountfs.ErrAccessDenied)
	case errors.Is(err, mountcontent.ErrKeyUnavailable):
		return fmt.Errorf("%w: unlock encryption before opening this file", mountfs.ErrAccessDenied)
	case errors.Is(err, media.ErrFileNotFound),
		errors.Is(err, tgclient.ErrMessageNotFound),
		errors.Is(err, tgclient.ErrNotFile),
		errors.Is(err, tgclient.ErrEmptyDocument):
		return fmt.Errorf("%w: content body was not found", mountfs.ErrNotFound)
	default:
		return fmt.Errorf("%w: content could not be opened", mountfs.ErrContentUnavailable)
	}
}
