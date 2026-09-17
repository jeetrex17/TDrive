package mountadapter

import (
	"context"
	"fmt"
	"io"
	"strconv"
	"strings"

	"TDrive/backend/mountwrite"
	"TDrive/backend/projection"
)

type storedRenditionPreparer interface {
	PrepareStoredRenditions(context.Context, projection.File, io.ReadSeeker) error
}

func (remote *TelegramRemote) PrepareCommittedRenditions(ctx context.Context, request mountwrite.HiddenUpload, result mountwrite.MutationResult, source io.ReadSeeker) error {
	preparer, ok := remote.files.(storedRenditionPreparer)
	if !ok {
		return nil
	}
	if request.DriveID != remote.driveID || !strings.HasPrefix(result.ObjectID, projection.FileIDPrefix) {
		return mountwrite.ErrInvalidRequest
	}
	id, err := strconv.ParseInt(strings.TrimPrefix(result.ObjectID, projection.FileIDPrefix), 10, 64)
	if err != nil || id <= 0 {
		return mountwrite.ErrInvalidRequest
	}
	current, found, err := projection.FileByID(remote.db, remote.driveID, id)
	if err != nil {
		return err
	}
	if !found {
		return nil
	}
	// The staged source belongs to this exact commit. A concurrent replacement
	// must never acquire previews generated from the previous writer's bytes.
	if current.Revision != int64(result.Revision) || current.Size != request.StoredSize || current.Encrypted != request.Encrypted {
		return fmt.Errorf("preview source was superseded")
	}
	return preparer.PrepareStoredRenditions(ctx, current, source)
}
