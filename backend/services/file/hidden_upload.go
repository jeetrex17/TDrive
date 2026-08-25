package file

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"

	tdcrypto "TDrive/backend/crypto"
	"TDrive/backend/projection"
	"TDrive/backend/tgclient"
)

type hiddenPartPlan struct {
	partSize  int64
	partCount int
}

// HiddenUploadRequest describes an immutable, already-staged stored
// representation. OperationID must already be durable in the caller's journal;
// it is used to derive Telegram's random_id so retries cannot publish duplicate
// bodies. Encrypted sources must already contain a complete TDrive ciphertext;
// this boundary validates sizes but never encrypts or persists plaintext.
type HiddenUploadRequest struct {
	OperationID   string
	Name          string
	StoredSize    int64
	PlaintextSize int64
	Encrypted     bool
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
	if err := validateHiddenStoredMetadata(request); err != nil {
		return HiddenBody{}, err
	}
	if _, _, err := s.planUpload(request.Name, request.PlaintextSize, request.Encrypted); err != nil {
		return HiddenBody{}, err
	}
	if err := validateSeekableSize(source, request.StoredSize); err != nil {
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
	peer, err := s.Peers.ResolvePeer(ctx, channelID)
	if err != nil {
		return HiddenBody{}, err
	}
	return s.uploadHiddenParts(ctx, channelID, request, source, peer)
}

func (s *Service) uploadHiddenParts(ctx context.Context, channelID int64, request HiddenUploadRequest, source io.ReadSeeker, peer tgclient.InputPeer) (HiddenBody, error) {
	if s.ActorID == nil {
		return HiddenBody{}, fmt.Errorf("actor resolver not ready")
	}
	actorID, err := s.ActorID(ctx)
	if err != nil {
		return HiddenBody{}, err
	}

	plan, err := s.hiddenUploadPartPlan(request.StoredSize)
	if err != nil {
		return HiddenBody{}, err
	}
	uploadUUID := hiddenUploadUUID(request.OperationID)
	remote := HiddenBody{
		UploadUUID:    uploadUUID,
		PartCount:     plan.partCount,
		StoredSize:    request.StoredSize,
		PlaintextSize: request.PlaintextSize,
		Encrypted:     request.Encrypted,
		MessageIDs:    make([]int64, 0, plan.partCount),
	}
	for partIndex := 0; partIndex < plan.partCount; partIndex++ {
		if err := ctx.Err(); err != nil {
			return remote, err
		}
		partOffset, partLength, err := hiddenPartWindow(plan, request.StoredSize, partIndex)
		if err != nil {
			return remote, err
		}
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
			partOffset,
			partLength,
			fmt.Sprintf("part-%05d", partIndex),
			caption,
			nil,
		)
		if err != nil {
			// The coordinator still owns the staged source and is in
			// StateUploading. It exclusively persists this exact partial body (or
			// reconciles an unknown outcome) before any delete or stage removal.
			return remote, err
		}
		if result.MsgID <= 0 {
			return remote, fmt.Errorf(
				"%w: hidden upload part %d returned no message id",
				tgclient.ErrSendOutcomeUnknown,
				partIndex,
			)
		}
		if s.afterHiddenPartSend != nil {
			s.afterHiddenPartSend(partIndex, result.MsgID)
		}
		remote.MessageIDs = append(remote.MessageIDs, result.MsgID)
		if _, err := projection.ProjectFromOp(s.DB, channelID, result.MsgID, partOp, actorID, caption); err != nil {
			return remote, fmt.Errorf(
				"%w: project hidden upload part %d: %w",
				ErrHiddenReceiptRecoveryRequired,
				partIndex,
				err,
			)
		}
	}
	return remote, nil
}

// RecoverHiddenUpload closes the only network/local durability gap in a
// sequential hidden upload. Projected parts form a durable contiguous receipt
// prefix. At most the first unprojected part could have reached Telegram, so
// this method resends exactly that part with the same random_id, obtains and
// projects the original receipt, then returns the exact owned body. It does not
// delete anything: callers must durably persist the returned body first.
// source must be the unchanged staged stored representation.
func (s *Service) RecoverHiddenUpload(
	ctx context.Context,
	channelID int64,
	request HiddenUploadRequest,
	source io.ReadSeeker,
) (HiddenBody, error) {
	if err := validateHiddenUpload(ctx, channelID, request, source); err != nil {
		return HiddenBody{}, err
	}
	if err := validateHiddenStoredMetadata(request); err != nil {
		return HiddenBody{}, err
	}
	if _, _, err := s.planUpload(request.Name, request.PlaintextSize, request.Encrypted); err != nil {
		return HiddenBody{}, err
	}
	if err := validateSeekableSize(source, request.StoredSize); err != nil {
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
	peer, err := s.Peers.ResolvePeer(ctx, channelID)
	if err != nil {
		return HiddenBody{}, err
	}
	return s.recoverHiddenParts(ctx, channelID, request, source, peer)
}

// RecoverAndDiscardHiddenUpload is the direct-service convenience path. The
// mount coordinator uses RecoverHiddenUpload and persists its receipt before
// invoking the validated receipt discard, so staging can be removed
// crash-safely.
func (s *Service) RecoverAndDiscardHiddenUpload(
	ctx context.Context,
	channelID int64,
	request HiddenUploadRequest,
	source io.ReadSeeker,
) error {
	body, err := s.RecoverHiddenUpload(ctx, channelID, request, source)
	if err != nil {
		return err
	}
	return s.DiscardHiddenReceipt(ctx, channelID, request.OperationID, body)
}

func (s *Service) recoverHiddenParts(
	ctx context.Context,
	channelID int64,
	request HiddenUploadRequest,
	source io.ReadSeeker,
	peer tgclient.InputPeer,
) (HiddenBody, error) {
	plan, err := s.hiddenUploadPartPlan(request.StoredSize)
	if err != nil {
		return HiddenBody{}, err
	}
	uploadUUID := hiddenUploadUUID(request.OperationID)
	parts, err := projection.PartsForUUIDContext(ctx, s.DB, channelID, uploadUUID)
	if err != nil {
		return HiddenBody{}, err
	}
	messageIDs, err := validateHiddenPartPrefix(parts, plan, request.StoredSize)
	if err != nil {
		return HiddenBody{}, err
	}

	// UploadHidden is strictly sequential and durably projects each accepted
	// part before beginning the next. Therefore only this one part can have an
	// accepted-but-unprojected receipt; later parts must never be resent here.
	uncertainPart := len(parts)
	if uncertainPart < plan.partCount {
		if s.ActorID == nil {
			return HiddenBody{}, fmt.Errorf("actor resolver not ready")
		}
		actorID, err := s.ActorID(ctx)
		if err != nil {
			return HiddenBody{}, err
		}
		offset, length, err := hiddenPartWindow(plan, request.StoredSize, uncertainPart)
		if err != nil {
			return HiddenBody{}, err
		}
		partOp := projection.Op{
			Type:       projection.OpFilePart,
			UploadUUID: uploadUUID,
			PartIndex:  uncertainPart,
			FileSize:   length,
		}
		result, err := s.sendHiddenFile(
			ctx,
			request.OperationID,
			fmt.Sprintf("part:%d", uncertainPart),
			peer,
			source,
			offset,
			length,
			fmt.Sprintf("part-%05d", uncertainPart),
			projection.Format(partOp),
			nil,
		)
		if err != nil {
			return HiddenBody{}, fmt.Errorf("recover hidden upload part %d receipt: %w", uncertainPart, err)
		}
		if result.MsgID <= 0 {
			return HiddenBody{}, fmt.Errorf("recover hidden upload part %d returned no message id", uncertainPart)
		}
		for _, ownedID := range messageIDs {
			if ownedID == result.MsgID {
				return HiddenBody{}, fmt.Errorf("hidden upload receipt reused message id %d across parts", result.MsgID)
			}
		}
		caption := projection.Format(partOp)
		if _, err := projection.ProjectFromOp(s.DB, channelID, result.MsgID, partOp, actorID, caption); err != nil {
			return HiddenBody{}, fmt.Errorf("project recovered hidden upload part %d: %w", uncertainPart, err)
		}
		messageIDs = append(messageIDs, result.MsgID)
	}

	return HiddenBody{
		UploadUUID:    uploadUUID,
		PartCount:     plan.partCount,
		StoredSize:    request.StoredSize,
		PlaintextSize: request.PlaintextSize,
		Encrypted:     request.Encrypted,
		MessageIDs:    messageIDs,
	}, nil
}

func (s *Service) hiddenUploadPartPlan(storedSize int64) (hiddenPartPlan, error) {
	partSize := s.maxPartBytes()
	if storedSize < 0 || partSize <= 0 {
		return hiddenPartPlan{}, fmt.Errorf("invalid hidden upload part sizing")
	}
	partCount := storedSize / partSize
	if storedSize%partSize != 0 {
		partCount++
	}
	if partCount == 0 {
		partCount = 1
	}
	if partCount > MaxParts {
		return hiddenPartPlan{}, fmt.Errorf("invalid multipart part count %d", partCount)
	}
	return hiddenPartPlan{partSize: partSize, partCount: int(partCount)}, nil
}

func hiddenPartWindow(plan hiddenPartPlan, storedSize int64, partIndex int) (int64, int64, error) {
	if plan.partSize <= 0 || plan.partCount <= 0 || partIndex < 0 || partIndex >= plan.partCount {
		return 0, 0, fmt.Errorf("invalid hidden upload part %d", partIndex)
	}
	offset := int64(partIndex) * plan.partSize
	if offset < 0 || offset > storedSize {
		return 0, 0, fmt.Errorf("invalid hidden upload part %d offset", partIndex)
	}
	return offset, min(plan.partSize, storedSize-offset), nil
}

func validateHiddenPartPrefix(
	parts []projection.FilePart,
	plan hiddenPartPlan,
	storedSize int64,
) ([]int64, error) {
	if len(parts) > plan.partCount {
		return nil, fmt.Errorf("hidden upload has %d receipts for %d parts", len(parts), plan.partCount)
	}
	messageIDs := make([]int64, 0, len(parts)+1)
	seen := make(map[int64]struct{}, len(parts)+1)
	for index, part := range parts {
		_, expectedSize, err := hiddenPartWindow(plan, storedSize, index)
		if err != nil {
			return nil, err
		}
		if part.PartIndex != index || part.MsgID <= 0 || part.Size != expectedSize {
			return nil, fmt.Errorf("hidden upload receipt prefix is invalid at part %d", index)
		}
		if _, duplicate := seen[part.MsgID]; duplicate {
			return nil, fmt.Errorf("hidden upload receipt message %d is duplicated", part.MsgID)
		}
		seen[part.MsgID] = struct{}{}
		messageIDs = append(messageIDs, part.MsgID)
	}
	return messageIDs, nil
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

// discardHiddenBody deletes the exact IDs in a body that a trusted internal
// caller has already bound to an operation. Public callers must use
// DiscardHiddenReceipt or DiscardHiddenOperation so ownership is validated.
func (s *Service) discardHiddenBody(ctx context.Context, channelID int64, body HiddenBody) error {
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

// DiscardHiddenReceipt validates a journaled cleanup receipt against the
// operation's deterministic upload identity and its durable projected prefix
// before any Telegram message ID can reach deletion. A missing prefix means a
// prior attempt already deleted the messages and their pointers, so retries are
// an idempotent no-op.
func (s *Service) DiscardHiddenReceipt(
	ctx context.Context,
	channelID int64,
	operationID string,
	body HiddenBody,
) error {
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
	expectedUploadUUID := hiddenUploadUUID(operationID)
	if body.UploadUUID != expectedUploadUUID {
		return fmt.Errorf("hidden cleanup receipt does not belong to operation")
	}
	if err := validateHiddenStoredMetadata(HiddenUploadRequest{
		StoredSize:    body.StoredSize,
		PlaintextSize: body.PlaintextSize,
		Encrypted:     body.Encrypted,
	}); err != nil {
		return fmt.Errorf("validate hidden cleanup metadata: %w", err)
	}
	plan, err := s.hiddenUploadPartPlan(body.StoredSize)
	if err != nil {
		return err
	}
	if body.PartCount != plan.partCount || len(body.MessageIDs) > body.PartCount {
		return fmt.Errorf("hidden cleanup receipt part count is invalid")
	}
	parts, err := projection.PartsForUUIDContext(ctx, s.DB, channelID, expectedUploadUUID)
	if err != nil {
		return err
	}
	if len(parts) == 0 {
		return nil
	}
	projectedIDs, err := validateHiddenPartPrefix(parts, plan, body.StoredSize)
	if err != nil {
		return err
	}
	if !sameMessageIDs(projectedIDs, body.MessageIDs) {
		return fmt.Errorf("hidden cleanup receipt does not match projected ownership")
	}
	return s.discardHiddenBody(ctx, channelID, body)
}

func sameMessageIDs(left, right []int64) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
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
	return s.discardHiddenBody(ctx, channelID, HiddenBody{
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
	if request.StoredSize < 0 {
		return fmt.Errorf("stored upload size must be non-negative")
	}
	if request.PlaintextSize < 0 {
		return fmt.Errorf("plaintext upload size must be non-negative")
	}
	if _, err := projection.CanonicalNameKey(request.Name); err != nil {
		return fmt.Errorf("upload name is not portable: %w", err)
	}
	if source == nil {
		return fmt.Errorf("staged upload source is required")
	}
	return nil
}

func validateHiddenStoredMetadata(request HiddenUploadRequest) error {
	expectedStoredSize := request.PlaintextSize
	if request.Encrypted {
		if err := tdcrypto.ValidatePlaintextSize(request.PlaintextSize); err != nil {
			return fmt.Errorf("validate encrypted plaintext size: %w", err)
		}
		expectedStoredSize = tdcrypto.CiphertextSize(request.PlaintextSize)
	}
	if request.StoredSize != expectedStoredSize {
		kind := "plaintext"
		if request.Encrypted {
			kind = "encrypted"
		}
		return fmt.Errorf(
			"%s staged upload size is %d bytes, expected %d bytes for %d plaintext bytes",
			kind,
			request.StoredSize,
			expectedStoredSize,
			request.PlaintextSize,
		)
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
