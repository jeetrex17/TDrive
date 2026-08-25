package mountcontroller

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"TDrive/backend/mountfs"
	readservice "TDrive/backend/services/read"
)

type directorySource struct {
	reads *readservice.Service
}

func (source directorySource) ListDirectory(ctx context.Context, channelID int64, parentID string) ([]mountfs.SourceEntry, error) {
	if ctx == nil {
		return nil, directoryUnavailable()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if source.reads == nil || channelID <= 0 {
		return nil, directoryUnavailable()
	}

	projectionParentID, err := projectionFolderID(parentID)
	if err != nil {
		return nil, directoryUnavailable()
	}
	contents, err := source.reads.FolderContentsContext(ctx, channelID, projectionParentID)
	if err != nil {
		return nil, mapDirectoryError(ctx, err)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	entries := make([]mountfs.SourceEntry, 0, len(contents.Folders)+len(contents.Files))
	for _, folder := range contents.Folders {
		if folder.ID == "" {
			return nil, directoryUnavailable()
		}
		entries = append(entries, mountfs.SourceEntry{
			ID:       folder.ID,
			ParentID: parentID,
			Name:     folder.Name,
			Kind:     mountfs.KindDirectory,
		})
	}
	for _, file := range contents.Files {
		if file.MsgID <= 0 {
			return nil, directoryUnavailable()
		}
		logicalSize := file.Size
		if file.Encrypted {
			logicalSize = file.PlaintextSize
		}
		if logicalSize < 0 {
			return nil, directoryUnavailable()
		}
		modTime := time.Time{}
		if file.UploadTime > 0 {
			modTime = time.Unix(file.UploadTime, 0)
		}
		messageID := strconv.FormatInt(file.MsgID, 10)
		entries = append(entries, mountfs.SourceEntry{
			ID:         "f:" + messageID,
			ParentID:   parentID,
			Name:       file.Name,
			Kind:       mountfs.KindFile,
			Size:       logicalSize,
			ModTime:    modTime,
			Encrypted:  file.Encrypted,
			ContentRef: messageID,
		})
	}
	return entries, nil
}

func mapDirectoryError(ctx context.Context, err error) error {
	if err == nil {
		return nil
	}
	if ctx != nil {
		if contextErr := ctx.Err(); contextErr != nil {
			return contextErr
		}
	}
	switch {
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return err
	case errors.Is(err, mountfs.ErrAccessDenied), errors.Is(err, os.ErrPermission):
		return fmt.Errorf("%w: directory metadata access was denied", mountfs.ErrAccessDenied)
	default:
		return directoryUnavailable()
	}
}

func directoryUnavailable() error {
	return fmt.Errorf("%w: directory metadata could not be loaded", mountfs.ErrContentUnavailable)
}

func projectionFolderID(mountID string) (string, error) {
	if mountID == mountfs.RootID {
		return "", nil
	}
	if !strings.HasPrefix(mountID, "d:") || len(mountID) == len("d:") {
		return "", fmt.Errorf("mount: invalid directory id")
	}
	return mountID, nil
}
