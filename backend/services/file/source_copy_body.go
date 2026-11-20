package file

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"TDrive/backend/channelsource"
	"TDrive/backend/projection"
	"TDrive/backend/tgclient"
)

var errSourceCopyDestinationUnavailable = errors.New("The destination folder is no longer available")

func (job sourceCopyJob) bodyUUID() string {
	return hiddenUploadUUID(job.Result.ID)
}

func (job sourceCopyJob) size() int64 {
	var size int64
	for _, part := range job.Parts {
		size += part.Size
	}
	return size
}

func (job sourceCopyJob) lastBodyID() int64 {
	var result int64
	for _, part := range job.Parts {
		result = max(result, part.BodyID)
	}
	return result
}

func (job sourceCopyJob) bodyIDs() []int64 {
	ids := make([]int64, 0, len(job.Parts))
	for _, part := range job.Parts {
		if part.BodyID > 0 {
			ids = append(ids, part.BodyID)
		}
	}
	return ids
}

func (job sourceCopyJob) startedBody() bool {
	return slices.ContainsFunc(job.Parts, func(part sourceCopyPart) bool { return part.DocumentID != 0 })
}

func (job sourceCopyJob) hasBodies() bool {
	return len(job.Parts) > 0 && !slices.ContainsFunc(job.Parts, func(part sourceCopyPart) bool { return part.BodyID <= 0 })
}

func (job sourceCopyJob) hasUnknownBody() bool {
	return slices.ContainsFunc(job.Parts, func(part sourceCopyPart) bool {
		return part.DocumentID != 0 && part.BodyID <= 0
	})
}

func (job sourceCopyJob) withPart(index int, part sourceCopyPart) sourceCopyJob {
	job.Parts = slices.Clone(job.Parts)
	job.Parts[index] = part
	return job
}

func (job sourceCopyJob) bodyHeader(index int) string {
	return projection.Format(projection.Op{
		Type: projection.OpFilePart, UploadUUID: job.bodyUUID(), PartIndex: index, FileSize: job.Parts[index].Size,
	})
}

func (w *sourceCopyWorker) prepareBodies(ctx context.Context, job sourceCopyJob) (sourceCopyJob, bool, error) {
	if len(job.Parts) > 0 {
		reconciled, err := w.reconcileBodies(ctx, job)
		if err != nil {
			return w.stop(ctx, job, sourceCopyPending, sourceCopyReceiptPending)
		}
		job = reconciled
	}
	if !job.hasBodies() {
		prepared, stopped, err := w.sendMissingBodies(ctx, job)
		if stopped || err != nil {
			return prepared, stopped, err
		}
		job = prepared
	}
	if err := w.sync(ctx); err != nil {
		return w.stop(ctx, job, sourceCopyPending, sourceCopyReceiptPending)
	}
	return job, false, nil
}

func (w *sourceCopyWorker) sendMissingBodies(ctx context.Context, job sourceCopyJob) (sourceCopyJob, bool, error) {
	var shared sharedCopySelection
	if job.Source.PeerKind == SharedCopyPeerKind {
		selection, err := parseSharedCopySelection(job.Source)
		if err != nil {
			return w.stopBodyPreparation(ctx, job, err)
		}
		if err := w.ensureSharedProjection(ctx, selection.driveID); err != nil {
			return w.stopBodyPreparation(ctx, job, err)
		}
		shared = selection
	}
	if len(job.Parts) == 0 {
		planned, err := w.planBodies(ctx, job, shared)
		if err != nil {
			return w.stopBodyPreparation(ctx, job, err)
		}
		job = planned
	}
	for index := range job.Parts {
		if job.Parts[index].BodyID > 0 {
			continue
		}
		prepared, stopped, err := w.sendBody(ctx, job, index, shared)
		if stopped || err != nil {
			return prepared, stopped, err
		}
		job = prepared
	}
	return job, false, nil
}

func (w sourceCopyWorker) planBodies(ctx context.Context, job sourceCopyJob, shared sharedCopySelection) (sourceCopyJob, error) {
	if job.Source.PeerKind != SharedCopyPeerKind {
		job.Parts = []sourceCopyPart{{SourceID: job.Source.MsgID}}
		return job, nil
	}
	if shared.rawIdentity != "" {
		job.Parts = []sourceCopyPart{{SourceID: shared.fileID}}
		return job, nil
	}
	source, err := w.resolveSharedCopy(ctx, shared)
	if err != nil {
		return job, err
	}
	if len(source.Parts) == 0 || len(source.Parts) > MaxParts {
		return job, channelsource.ErrCopyNotAllowed
	}
	parts := make([]sourceCopyPart, 0, len(source.Parts))
	for _, id := range source.Parts {
		parts = append(parts, sourceCopyPart{SourceID: id})
	}
	job.Parts = parts
	if job.Result.Name == "" {
		job.Result.Name = NormalizeSourceCopyName(source.Name)
	}
	return job, nil
}

func (w *sourceCopyWorker) ensureSharedProjection(ctx context.Context, channelID int64) error {
	if !w.sharedSyncDone {
		w.sharedSyncDone = true
		channel, err := projection.GetChannel(w.DB, channelID)
		switch {
		case err != nil || channel.Kind != projection.KindShared:
			w.sharedSyncErr = channelsource.ErrCopyNotAllowed
		case w.deps.SyncShared != nil:
			w.sharedSyncErr = w.deps.SyncShared(ctx, channelID)
		}
	}
	return w.sharedSyncErr
}

func (w sourceCopyWorker) resolveBody(ctx context.Context, job sourceCopyJob, index int, shared sharedCopySelection) (tgclient.DocumentRef, error) {
	if err := w.checkSourceCopyScope(ctx, job.ChannelID, w.accountID); err != nil {
		return tgclient.DocumentRef{}, err
	}
	if _, err := w.validParent(job.ChannelID, job.Result.ParentID, "target"); err != nil {
		return tgclient.DocumentRef{}, errSourceCopyDestinationUnavailable
	}
	part := job.Parts[index]
	var ref tgclient.DocumentRef
	var err error
	if job.Source.PeerKind == SharedCopyPeerKind {
		ref, err = w.resolveSharedCopyPart(ctx, shared, part.SourceID)
	} else {
		ref, err = w.deps.Resolve(ctx, job.Source)
	}
	if err != nil {
		return tgclient.DocumentRef{}, err
	}
	if part.DocumentID != 0 && (ref.DocumentID != part.DocumentID || ref.Size != part.Size || ref.PhotoSizeType != "") {
		return tgclient.DocumentRef{}, channelsource.ErrCopyChanged
	}
	remainingBytes := LargeFileMaxBytes - (job.size() - part.Size)
	if ref.DocumentID <= 0 || ref.Size <= 0 || ref.Size > remainingBytes || ref.PhotoSizeType != "" {
		return tgclient.DocumentRef{}, channelsource.ErrCopyNotAllowed
	}
	return ref, nil
}

func (w sourceCopyWorker) sendBody(ctx context.Context, job sourceCopyJob, index int, shared sharedCopySelection) (sourceCopyJob, bool, error) {
	step := "source-copy:body"
	// Preserve the original send ID for single documents already journaled.
	if index > 0 {
		step = fmt.Sprintf("%s:%d", step, index)
	}
	randomID, err := tgclient.StableRandomID(job.Result.ID, step)
	if err != nil {
		return job, true, err
	}
	prepared := job
	var journalErr error
	result, err := w.sender.ResendMedia(ctx, w.peer, randomID, func(ctx context.Context) (tgclient.ExistingMedia, error) {
		ref, err := w.resolveBody(ctx, prepared, index, shared)
		if err != nil {
			return tgclient.ExistingMedia{}, err
		}
		prepared, journalErr = w.pinCopyBody(ctx, prepared, index, ref)
		if journalErr != nil {
			return tgclient.ExistingMedia{}, journalErr
		}
		return tgclient.ExistingMedia{Document: ref, Caption: prepared.bodyHeader(index)}, nil
	})
	if result.MsgID <= 0 {
		if err == nil || errors.Is(err, tgclient.ErrSendOutcomeUnknown) {
			return w.stop(ctx, prepared, sourceCopyPending, sourceCopyReceiptPending)
		}
		if journalErr != nil && ctx.Err() == nil {
			return prepared, true, journalErr
		}
		// An explicit rejection does not change the part's receipt state.
		// Preserve any older uncertainty while clearing this unaccepted attempt.
		job.Result.Name = prepared.Result.Name
		return w.stopBodyPreparation(ctx, job, err)
	}
	part := prepared.Parts[index]
	part.BodyID = result.MsgID
	job = prepared.withPart(index, part)
	if err := w.persistSourceCopyJobAfterAttempt(ctx, job); err != nil {
		return job, true, err
	}
	return job, false, nil
}

func (w sourceCopyWorker) pinCopyBody(ctx context.Context, job sourceCopyJob, index int, ref tgclient.DocumentRef) (sourceCopyJob, error) {
	part := job.Parts[index]
	if part.DocumentID != 0 && job.Result.Name != "" {
		return job, nil
	}
	part.DocumentID, part.Size = ref.DocumentID, ref.Size
	next := job.withPart(index, part)
	if next.Result.Name == "" {
		next.Result.Name = NormalizeSourceCopyName(ref.Name)
	}
	// Reconcile a lost send response against the exact document and caption
	// recorded before the write.
	if err := w.persistSourceCopyJob(ctx, next); err != nil {
		return job, err
	}
	return next, nil
}

func (w sourceCopyWorker) stopBodyPreparation(ctx context.Context, job sourceCopyJob, err error) (sourceCopyJob, bool, error) {
	destinationGone := errors.Is(err, errSourceCopyDestinationUnavailable)
	sourceInvalid := errors.Is(err, channelsource.ErrCopyNotAllowed) || errors.Is(err, channelsource.ErrCopyChanged)
	if (destinationGone || sourceInvalid) && job.Attempt == 0 && job.lastBodyID() > 0 && !job.hasUnknownBody() && ctx.Err() == nil {
		return w.abandonConfirmedPrefix(ctx, job)
	}
	if destinationGone {
		if job.startedBody() {
			return w.stop(ctx, job, sourceCopyPending, errSourceCopyDestinationUnavailable.Error())
		}
		return w.stop(ctx, job, sourceCopyFailed, errSourceCopyDestinationUnavailable.Error())
	}
	return w.sourceUnavailable(ctx, job, job.startedBody(), err)
}

func (w sourceCopyWorker) abandonConfirmedPrefix(ctx context.Context, job sourceCopyJob) (sourceCopyJob, bool, error) {
	if err := w.sync(ctx); err != nil {
		return w.stop(ctx, job, sourceCopyPending, sourceCopyReceiptPending)
	}
	snapshot, err := projection.ReadOperationSnapshot(ctx, w.DB, job.ChannelID, "")
	if err != nil || !snapshot.Ordered || snapshot.Watermark < job.lastBodyID() {
		return w.stop(ctx, job, sourceCopyPending, sourceCopyReceiptPending)
	}
	result, err := w.abandonBody(ctx, job)
	if err == nil {
		job.Result = result
	}
	return job, true, err
}

func (w sourceCopyWorker) reconcileBodies(ctx context.Context, job sourceCopyJob) (sourceCopyJob, error) {
	receipts, err := w.projectedBodies(ctx, job)
	if err != nil {
		return job, err
	}
	parts := slices.Clone(job.Parts)
	changed := false
	for index, id := range receipts {
		if id != 0 && parts[index].BodyID == 0 {
			parts[index].BodyID = id
			changed = true
		}
	}
	if changed {
		job.Parts = parts
		return job, w.persistSourceCopyJob(ctx, job)
	}
	return job, nil
}

func (w sourceCopyWorker) projectedBodies(ctx context.Context, job sourceCopyJob) ([]int64, error) {
	parts, err := projection.PartsForUUIDContext(ctx, w.DB, job.ChannelID, job.bodyUUID())
	if err != nil {
		return nil, err
	}
	receipts := make([]int64, len(job.Parts))
	for _, part := range parts {
		if part.PartIndex < 0 || part.PartIndex >= len(job.Parts) || part.MsgID <= 0 {
			return nil, errors.New("Unexpected destination attachment")
		}
		expected := job.Parts[part.PartIndex]
		if expected.DocumentID == 0 || expected.Size != part.Size || (expected.BodyID > 0 && expected.BodyID != part.MsgID) {
			return nil, errors.New("Destination attachment no longer matches the receipt")
		}
		receipts[part.PartIndex] = part.MsgID
	}
	return receipts, nil
}

func (w sourceCopyWorker) verifyBodies(ctx context.Context, job sourceCopyJob) error {
	receipts, err := w.projectedBodies(ctx, job)
	if err != nil {
		return err
	}
	ids := job.bodyIDs()
	if len(ids) == 0 || len(ids) != len(job.Parts) {
		return errors.New("Destination attachment has no receipt")
	}
	messages, err := w.readBodyMessages(ctx, ids)
	if err != nil {
		return err
	}
	for index, id := range ids {
		if err := job.verifyBodyMessage(index, messages); err != nil {
			return err
		}
		if receipts[index] != id {
			return errors.New("Destination attachment has not been projected")
		}
	}
	return nil
}

func (w sourceCopyWorker) readBodyMessages(ctx context.Context, ids []int64) (map[int64]tgclient.HistoryMessage, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	if reader, ok := w.TG.(tgclient.ChannelMessageBatchReader); ok {
		return reader.GetChannelMessages(ctx, w.peer, ids)
	}
	messages := make(map[int64]tgclient.HistoryMessage, len(ids))
	for _, id := range ids {
		msg, err := w.TG.GetChannelMessage(ctx, w.peer, id)
		switch {
		case errors.Is(err, tgclient.ErrMessageNotFound):
		case err != nil:
			return nil, err
		default:
			messages[id] = msg
		}
	}
	return messages, nil
}

func (job sourceCopyJob) verifyBodyMessage(index int, messages map[int64]tgclient.HistoryMessage) error {
	part := job.Parts[index]
	msg, present := messages[part.BodyID]
	if !present {
		return tgclient.ErrMessageNotFound
	}
	if !matchesCopyMessage(job, msg, part.BodyID, job.bodyHeader(index)) ||
		!msg.HasMedia || msg.NativePhoto || msg.DocumentID != part.DocumentID || msg.MediaSize != part.Size {
		return errors.New("The destination attachment could not be verified")
	}
	return nil
}

func matchesCopyMessage(job sourceCopyJob, msg tgclient.HistoryMessage, id int64, header string) bool {
	return msg.MsgID == id && (msg.ChannelID == 0 || msg.ChannelID == job.ChannelID) &&
		uploadMessageIsOwn(msg, job.Source.AccountID) && !msg.Placeholder && msg.Text == header
}
