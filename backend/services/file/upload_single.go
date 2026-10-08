package file

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"TDrive/backend/projection"
	"TDrive/backend/tgclient"
	"TDrive/backend/thumbnail"
)

func (s *Service) uploadSingle(ctx context.Context, uploadID int, filePath string, parentID string, channelID int64, wantEncrypted bool) (Metadata, projection.Op, string, error) {
	peer, err := s.Peers.ResolvePeer(ctx, channelID)
	if err != nil {
		return Metadata{}, projection.Op{}, "", err
	}
	return s.uploadSingleWithObserver(
		ctx,
		uploadID,
		filePath,
		parentID,
		channelID,
		wantEncrypted,
		peer,
		detailedUploadObserver{service: s},
		false,
	)
}

func (s *Service) uploadSingleWithObserver(ctx context.Context, uploadID int, filePath string, parentID string, channelID int64, wantEncrypted bool, peer tgclient.InputPeer, observer uploadObserver, resumable bool) (Metadata, projection.Op, string, error) {
	if channelID == 0 {
		return Metadata{}, projection.Op{}, "", fmt.Errorf("drive channel id not found")
	}

	filename := filepath.Base(filePath)
	sourceStarted := time.Now()

	plainFile, err := os.Open(filePath)
	if err != nil {
		slog.Error("file: upload open source failed", "channel_id", channelID, "name", filename, "error", err)
		return Metadata{}, projection.Op{}, "", err
	}
	defer plainFile.Close()

	info, err := plainFile.Stat()
	if err != nil {
		return Metadata{}, projection.Op{}, "", err
	}
	plaintextSize := info.Size()
	// A photo's original and derivatives must use one immutable source. The
	// user can replace the selected path while Telegram is uploading; native
	// decoding that path later would otherwise publish unrelated image pixels.
	var source io.ReadSeeker = plainFile
	var photoSnapshot []byte
	const maxPhotoSnapshot = 30 << 20
	if thumbnail.IsImage(filename) && plaintextSize <= maxPhotoSnapshot {
		snapshot, readErr := io.ReadAll(io.LimitReader(plainFile, maxPhotoSnapshot+1))
		if readErr != nil {
			return Metadata{}, projection.Op{}, "", readErr
		}
		defer clear(snapshot)
		if int64(len(snapshot)) != plaintextSize {
			return Metadata{}, projection.Op{}, "", fmt.Errorf("photo changed while preparing upload")
		}
		source = bytes.NewReader(snapshot)
		photoSnapshot = snapshot
	}
	reportUploadTiming(ctx, "source_prepare", sourceStarted)
	// Announce the operation once the local source is known, before validating
	// remote metadata. That keeps failed uploads visible to callers while
	// avoiding the duplicate start event that used to be emitted at two layers.
	observer.Started(uploadID, filename, uploadByteSize(plaintextSize, wantEncrypted), parentID)
	slog.Debug("file: uploading", "channel_id", channelID, "name", filename, "size", plaintextSize, "encrypt", wantEncrypted, "parent_id", parentID)
	meta, op, header, err := s.uploadVisibleSource(ctx, uploadID, source, filePath, filename, plaintextSize, parentID, channelID, wantEncrypted, peer, observer, resumable)
	if err != nil {
		slog.Error("file: upload failed", "channel_id", channelID, "name", filename, "size", plaintextSize, "error", err)
	} else {
		slog.Debug("file: upload succeeded", "channel_id", channelID, "name", filename, "msg_id", meta.MsgID, "stored_size", meta.Size)
	}
	// Optional derivative consumers must use the exact immutable bytes sent,
	// never reopen a path the user/native provider may have replaced.
	if meta.MsgID > 0 && wantEncrypted && len(photoSnapshot) > 0 {
		if receiver, ok := observer.(interface{ CapturePhotoSnapshot([]byte) }); ok {
			receiver.CapturePhotoSnapshot(photoSnapshot)
		}
	}
	return meta, op, header, err
}
