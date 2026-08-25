package file

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"

	tdcrypto "TDrive/backend/crypto"
	"TDrive/backend/projection"
	"TDrive/backend/services/servicecontext"
	"TDrive/backend/tgclient"
)

// partAttachmentName names a part's Telegram document attachment after the
// original file it belongs to, rather than a meaningless-outside-TDrive
// "part-NNNNN" label. The part index TDrive itself relies on for
// reconstruction lives entirely in the TDX1 caption text (pix=), never in
// this filename, so this is purely cosmetic -- it exists so a file browsed
// directly in the Telegram channel, or downloaded straight from it, shows
// its real name. A single-part upload (the common case) keeps the exact
// original name; a multi-part upload suffixes the part index so parts stay
// distinguishable and orderable when browsed that way, since the channel
// view has no other way to show they belong together. An empty name (should
// not happen; callers validate this upstream) falls back to the old scheme
// rather than uploading a document with a blank filename.
func partAttachmentName(originalName string, partIndex, partCount int) string {
	if originalName == "" {
		return fmt.Sprintf("part-%05d", partIndex)
	}
	if partCount <= 1 {
		return originalName
	}
	return fmt.Sprintf("%s.part%d", originalName, partIndex)
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
	peer, err := s.prepareHiddenUpload(ctx, channelID, request, source)
	if err != nil {
		return HiddenBody{}, err
	}
	release, err := s.acquireUploadSlot(ctx)
	if err != nil {
		return HiddenBody{}, err
	}
	defer release()
	return s.uploadHiddenParts(ctx, channelID, request, source, peer)
}

func (s *Service) prepareHiddenUpload(
	ctx context.Context,
	channelID int64,
	request HiddenUploadRequest,
	source io.ReadSeeker,
) (tgclient.InputPeer, error) {
	if err := validateHiddenUpload(ctx, channelID, request, source); err != nil {
		return tgclient.InputPeer{}, err
	}
	if err := validateHiddenStoredMetadata(request); err != nil {
		return tgclient.InputPeer{}, err
	}
	if _, _, err := s.planUpload(request.Name, request.PlaintextSize, request.Encrypted); err != nil {
		return tgclient.InputPeer{}, err
	}
	if err := validateSeekableSize(source, request.StoredSize); err != nil {
		return tgclient.InputPeer{}, err
	}
	if err := s.ready(); err != nil {
		return tgclient.InputPeer{}, err
	}
	if s.TG == nil {
		return tgclient.InputPeer{}, fmt.Errorf("tg client not ready")
	}
	if s.Peers == nil {
		return tgclient.InputPeer{}, fmt.Errorf("peer resolver not ready")
	}
	return s.Peers.ResolvePeer(ctx, channelID)
}

func (s *Service) uploadHiddenParts(ctx context.Context, channelID int64, request HiddenUploadRequest, source io.ReadSeeker, peer tgclient.InputPeer) (HiddenBody, error) {
	if s.ActorID == nil {
		return HiddenBody{}, fmt.Errorf("actor resolver not ready")
	}
	actorID, err := s.ActorID(ctx)
	if err != nil {
		return HiddenBody{}, err
	}

	plan, err := s.buildUploadPartPlan(request.StoredSize)
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
		partOffset, partLength, err := plan.window(request.StoredSize, partIndex)
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
			partAttachmentName(request.Name, partIndex, plan.partCount),
			caption,
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
	peer, err := s.prepareHiddenUpload(ctx, channelID, request, source)
	if err != nil {
		return HiddenBody{}, err
	}
	release, err := s.acquireUploadSlot(ctx)
	if err != nil {
		return HiddenBody{}, err
	}
	defer release()
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
	plan, err := s.buildUploadPartPlan(request.StoredSize)
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
		offset, length, err := plan.window(request.StoredSize, uncertainPart)
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
			partAttachmentName(request.Name, uncertainPart, plan.partCount),
			projection.Format(partOp),
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

func validateHiddenPartPrefix(
	parts []projection.FilePart,
	plan uploadPartPlan,
	storedSize int64,
) ([]int64, error) {
	if len(parts) > plan.partCount {
		return nil, fmt.Errorf("hidden upload has %d receipts for %d parts", len(parts), plan.partCount)
	}
	messageIDs := make([]int64, 0, len(parts)+1)
	seen := make(map[int64]struct{}, len(parts)+1)
	for index, part := range parts {
		_, expectedSize, err := plan.window(storedSize, index)
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
) (tgclient.SendFileResult, error) {
	randomID, err := tgclient.StableRandomID(operationID, step)
	if err != nil {
		return tgclient.SendFileResult{}, err
	}

	var result tgclient.SendFileResult
	err = s.sendRetryPolicy().Do(ctx, func() error {
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
			nil,
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
	if err := servicecontext.Check(ctx, "file: discard hidden body"); err != nil {
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
	if err := servicecontext.Check(ctx, "file: discard hidden receipt"); err != nil {
		return err
	}
	if channelID == 0 {
		return fmt.Errorf("%w: drive channel id not found", ErrHiddenReceiptInvalid)
	}
	if _, err := tgclient.StableRandomID(operationID, "validate"); err != nil {
		return fmt.Errorf("%w: operation identity", ErrHiddenReceiptInvalid)
	}
	if s == nil || s.DB == nil {
		return fmt.Errorf("local projection is not ready")
	}
	expectedUploadUUID := hiddenUploadUUID(operationID)
	if body.UploadUUID != expectedUploadUUID {
		return fmt.Errorf("%w: receipt does not belong to operation", ErrHiddenReceiptInvalid)
	}
	if err := validateHiddenStoredMetadata(HiddenUploadRequest{
		StoredSize:    body.StoredSize,
		PlaintextSize: body.PlaintextSize,
		Encrypted:     body.Encrypted,
	}); err != nil {
		return fmt.Errorf("%w: metadata: %v", ErrHiddenReceiptInvalid, err)
	}
	plan, err := s.buildUploadPartPlan(body.StoredSize)
	if err != nil {
		return fmt.Errorf("%w: part plan: %v", ErrHiddenReceiptInvalid, err)
	}
	if body.PartCount != plan.partCount || len(body.MessageIDs) > body.PartCount {
		return fmt.Errorf("%w: part count", ErrHiddenReceiptInvalid)
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
		return fmt.Errorf("%w: projected prefix: %v", ErrHiddenReceiptInvalid, err)
	}
	if !sameMessageIDs(projectedIDs, body.MessageIDs) {
		return fmt.Errorf("%w: projected ownership mismatch", ErrHiddenReceiptInvalid)
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
	if err := servicecontext.Check(ctx, "file: discard hidden operation"); err != nil {
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
	if err := servicecontext.Check(ctx, "file: upload hidden"); err != nil {
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
