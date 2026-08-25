package file

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"time"

	"TDrive/backend/projection"
	"TDrive/backend/tgclient"
)

const (
	hiddenCleanupLimit = 30 * time.Second
)

var ErrHiddenEncryptionUnsupported = errors.New("encrypted writable uploads are not supported yet")

// HiddenUploadRequest describes an immutable staged source. OperationID must
// already be durable in the caller's journal; it is used to derive Telegram's
// random_id so retries cannot publish duplicate bodies.
type HiddenUploadRequest struct {
	OperationID string
	Name        string
	Size        int64
	Encrypted   bool
}

// HiddenBody is the remote, still-invisible content uploaded ahead of a
// projection commit. Bodies are represented as an invisible OpFilePart group;
// a normal-sized file has PartCount 1. MessageIDs are the exact Telegram
// documents DiscardHidden owns.
type HiddenBody struct {
	UploadUUID    string
	PartCount     int
	StoredSize    int64
	PlaintextSize int64
	Encrypted     bool
	MessageIDs    []int64
}

// UploadHidden uploads a seekable staged source without creating a visible
// file row. The caller retains ownership of source and must not mutate it while
// this method is running.
func (s *Service) UploadHidden(ctx context.Context, channelID int64, request HiddenUploadRequest, source io.ReadSeeker) (HiddenBody, error) {
	if err := validateHiddenUpload(ctx, channelID, request, source); err != nil {
		return HiddenBody{}, err
	}
	if err := s.ready(); err != nil {
		return HiddenBody{}, err
	}
	if s.TG == nil {
		return HiddenBody{}, fmt.Errorf("tg client not ready")
	}
	if s.Peers == nil {
		return HiddenBody{}, fmt.Errorf("peer resolver not ready")
	}
	if request.Encrypted {
		return HiddenBody{}, ErrHiddenEncryptionUnsupported
	}
	if err := validateSeekableSize(source, request.Size); err != nil {
		return HiddenBody{}, err
	}

	storedSize, _, err := s.planUpload(request.Name, request.Size, false)
	if err != nil {
		return HiddenBody{}, err
	}
	peer, err := s.Peers.ResolvePeer(ctx, channelID)
	if err != nil {
		return HiddenBody{}, err
	}
	return s.uploadHiddenParts(ctx, channelID, request, source, peer, storedSize)
}

func (s *Service) uploadHiddenParts(ctx context.Context, channelID int64, request HiddenUploadRequest, source io.ReadSeeker, peer tgclient.InputPeer, storedSize int64) (HiddenBody, error) {
	if s.ActorID == nil {
		return HiddenBody{}, fmt.Errorf("actor resolver not ready")
	}
	actorID, err := s.ActorID(ctx)
	if err != nil {
		return HiddenBody{}, err
	}

	partSize := s.maxPartBytes()
	partCount := int((storedSize + partSize - 1) / partSize)
	if partCount == 0 {
		partCount = 1
	}
	if partCount <= 0 || partCount > MaxParts {
		return HiddenBody{}, fmt.Errorf("invalid multipart part count %d", partCount)
	}
	uploadUUID := hiddenUploadUUID(request.OperationID)
	remote := HiddenBody{
		UploadUUID:    uploadUUID,
		PartCount:     partCount,
		StoredSize:    storedSize,
		PlaintextSize: request.Size,
		MessageIDs:    make([]int64, 0, partCount),
	}
	abort := func(cause error) (HiddenBody, error) {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), hiddenCleanupLimit)
		defer cancel()
		if cleanupErr := s.discardHidden(cleanupCtx, channelID, peer, remote); cleanupErr != nil {
			s.warnf("warn: hidden multipart cleanup failed: %v\n", cleanupErr)
		}
		return HiddenBody{}, cause
	}

	var offset int64
	for partIndex := 0; partIndex < partCount; partIndex++ {
		if err := ctx.Err(); err != nil {
			return abort(err)
		}
		partLength := min(partSize, storedSize-offset)
		partOp := projection.Op{
			Type:       projection.OpFilePart,
			UploadUUID: uploadUUID,
			PartIndex:  partIndex,
			FileSize:   partLength,
		}
		caption := projection.Format(partOp)
		result, err := s.sendHiddenFile(
			ctx,
			request.OperationID,
			fmt.Sprintf("part:%d", partIndex),
			peer,
			source,
			offset,
			partLength,
			fmt.Sprintf("part-%05d", partIndex),
			caption,
			nil,
		)
		if err != nil {
			return abort(err)
		}
		if result.MsgID <= 0 {
			return abort(fmt.Errorf("hidden upload part %d returned no message id", partIndex))
		}
		remote.MessageIDs = append(remote.MessageIDs, result.MsgID)
		if _, err := projection.ProjectFromOp(s.DB, channelID, result.MsgID, partOp, actorID, caption); err != nil {
			return abort(fmt.Errorf("project hidden upload part %d: %w", partIndex, err))
		}
		offset += partLength
	}
	return remote, nil
}

func (s *Service) sendHiddenFile(
	ctx context.Context,
	operationID string,
	step string,
	peer tgclient.InputPeer,
	source io.ReadSeeker,
	offset int64,
	length int64,
	name string,
	caption string,
	onProgress func(sent, total int64),
) (tgclient.SendFileResult, error) {
	randomID, err := tgclient.StableRandomID(operationID, step)
	if err != nil {
		return tgclient.SendFileResult{}, err
	}
	policy := s.FloodWaitRetry
	if policy.MaxRetries == 0 && policy.MaxWait == 0 && policy.MaxTotalWait == 0 && policy.Sleep == nil {
		policy = tgclient.DefaultWriteFloodWaitRetryPolicy()
	}

	var result tgclient.SendFileResult
	err = policy.Do(ctx, func() error {
		if _, err := source.Seek(offset, io.SeekStart); err != nil {
			return fmt.Errorf("seek staged upload: %w", err)
		}
		result, err = tgclient.SendFileIdempotent(
			ctx,
			s.TG,
			peer,
			io.LimitReader(source, length),
			name,
			caption,
			length,
			onProgress,
			randomID,
		)
		return err
	})
	return result, err
}

// DiscardHidden deletes only the bodies owned by body. It is safe to call more
// than once; successful multipart cleanup also removes local part pointers.
func (s *Service) DiscardHidden(ctx context.Context, channelID int64, body HiddenBody) error {
	if ctx == nil {
		return fmt.Errorf("context is required")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if channelID == 0 {
		return fmt.Errorf("drive channel id not found")
	}
	if len(body.MessageIDs) == 0 {
		return nil
	}
	if s.TG == nil || s.Peers == nil {
		return fmt.Errorf("telegram cleanup is not ready")
	}
	peer, err := s.Peers.ResolvePeer(ctx, channelID)
	if err != nil {
		return err
	}
	return s.discardHidden(ctx, channelID, peer, body)
}

// DiscardHiddenOperation cleans an upload whose process crashed before its
// HiddenBody could be persisted in the write journal. OperationID is mapped to
// the same deterministic upload UUID used by UploadHidden; only projected
// parts owned by that operation are eligible for deletion.
func (s *Service) DiscardHiddenOperation(ctx context.Context, channelID int64, operationID string) error {
	if ctx == nil {
		return fmt.Errorf("context is required")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if channelID == 0 {
		return fmt.Errorf("drive channel id not found")
	}
	if _, err := tgclient.StableRandomID(operationID, "validate"); err != nil {
		return err
	}
	if s == nil || s.DB == nil {
		return fmt.Errorf("local projection is not ready")
	}
	uploadUUID := hiddenUploadUUID(operationID)
	parts, err := projection.PartsForUUIDContext(ctx, s.DB, channelID, uploadUUID)
	if err != nil {
		return err
	}
	if len(parts) == 0 {
		return nil
	}
	messageIDs := make([]int64, len(parts))
	for index, part := range parts {
		messageIDs[index] = part.MsgID
	}
	return s.DiscardHidden(ctx, channelID, HiddenBody{
		UploadUUID: uploadUUID,
		PartCount:  len(parts),
		MessageIDs: messageIDs,
	})
}

func (s *Service) discardHidden(ctx context.Context, channelID int64, peer tgclient.InputPeer, body HiddenBody) error {
	if err := s.deleteMessagesChunked(ctx, peer, body.MessageIDs); err != nil {
		if s.DB != nil {
			_ = projection.QueuePartCleanup(s.DB, channelID, body.MessageIDs)
		}
		return err
	}
	if body.UploadUUID != "" && s.DB != nil {
		if err := projection.DeleteFileParts(s.DB, channelID, body.UploadUUID); err != nil {
			return fmt.Errorf("remove hidden part pointers: %w", err)
		}
	}
	return nil
}

func validateHiddenUpload(ctx context.Context, channelID int64, request HiddenUploadRequest, source io.ReadSeeker) error {
	if ctx == nil {
		return fmt.Errorf("context is required")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if channelID == 0 {
		return fmt.Errorf("drive channel id not found")
	}
	if _, err := tgclient.StableRandomID(request.OperationID, "validate"); err != nil {
		return err
	}
	if request.Size < 0 {
		return fmt.Errorf("upload size must be non-negative")
	}
	if _, err := projection.CanonicalNameKey(request.Name); err != nil {
		return fmt.Errorf("upload name is not portable: %w", err)
	}
	if source == nil {
		return fmt.Errorf("staged upload source is required")
	}
	return nil
}

func validateSeekableSize(source io.ReadSeeker, expected int64) error {
	actual, err := source.Seek(0, io.SeekEnd)
	if err != nil {
		return fmt.Errorf("measure staged upload: %w", err)
	}
	if _, seekErr := source.Seek(0, io.SeekStart); seekErr != nil {
		return fmt.Errorf("rewind staged upload: %w", seekErr)
	}
	if actual != expected {
		return fmt.Errorf("staged upload size is %d bytes, expected %d", actual, expected)
	}
	return nil
}

func hiddenUploadUUID(operationID string) string {
	digest := sha256.Sum256([]byte("tdrive.hidden-upload.v1\x00" + operationID))
	return "hu-" + hex.EncodeToString(digest[:16])
}
