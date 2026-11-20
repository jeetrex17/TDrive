package file

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"TDrive/backend/channelsource"
	"TDrive/backend/projection"
	"TDrive/backend/tgclient"
)

const sourceCopyReceiptPending = "Waiting to confirm the save in My Drive. Check again from Activity."

type sourceCopyWorker struct {
	*Service
	deps           SourceCopyDependencies
	sender         tgclient.ExistingMediaSender
	peer           tgclient.InputPeer
	accountID      int64
	sharedSyncDone bool
	sharedSyncErr  error
}

func (s *Service) newSourceCopyWorker(ctx context.Context, channelID, accountID int64, deps SourceCopyDependencies) (sourceCopyWorker, error) {
	if err := s.checkSourceCopyScope(ctx, channelID, accountID); err != nil {
		return sourceCopyWorker{}, err
	}
	sender, ok := s.TG.(tgclient.ExistingMediaSender)
	if !ok || !supportsIdempotentSends(s.TG) || s.Peers == nil || deps.Resolve == nil || deps.Sync == nil {
		return sourceCopyWorker{}, errors.New("Save to My Drive is unavailable")
	}
	peer, err := s.Peers.ResolvePeer(ctx, channelID)
	if err != nil || peer.ChannelID != channelID || peer.PeerKind() != tgclient.PeerChannel {
		return sourceCopyWorker{}, errors.New("Could not open My Drive on Telegram")
	}
	return sourceCopyWorker{Service: s, deps: deps, sender: sender, peer: peer, accountID: accountID}, nil
}

func (w sourceCopyWorker) sync(ctx context.Context) error {
	if err := w.deps.Sync(ctx, w.peer.ChannelID); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return errors.New("Could not synchronize My Drive")
	}
	return nil
}

func (w *sourceCopyWorker) run(ctx context.Context, job sourceCopyJob) (SourceCopyResult, error) {
	if ctx.Err() != nil && !job.startedBody() {
		return w.finish(ctx, job, sourceCopyCanceled, "Save canceled")
	}
	if job.BatchID == "" && job.Attempt > 0 {
		if err := w.repairLegacyCopyReplay(ctx, job.ChannelID); err != nil {
			return w.finish(ctx, job, sourceCopyPending, sourceCopyReceiptPending)
		}
	}
	if job.CleaningUp {
		return w.cleanup(ctx, job)
	}
	if !job.hasBodies() {
		resolved, stopped, err := w.prepareBodies(ctx, job)
		if err != nil || stopped {
			return resolved.result(), err
		}
		job = resolved
	}
	return w.publish(ctx, job)
}

func (w sourceCopyWorker) sourceUnavailable(ctx context.Context, job sourceCopyJob, mayHaveBody bool, err error) (sourceCopyJob, bool, error) {
	if mayHaveBody {
		return w.stop(ctx, job, sourceCopyPending, "Could not confirm the earlier save. The original source and attachment must still be available to retry.")
	}
	if ctx.Err() != nil {
		return w.stop(ctx, job, sourceCopyCanceled, "Save canceled")
	}
	if errors.Is(err, channelsource.ErrCopyChanged) || errors.Is(err, channelsource.ErrCopyNotAllowed) {
		return w.stop(ctx, job, sourceCopyFailed, err.Error())
	}
	return w.stop(ctx, job, sourceCopyFailed, "This attachment could not be copied. Check access and select it again.")
}

func (w sourceCopyWorker) cleanup(ctx context.Context, job sourceCopyJob) (SourceCopyResult, error) {
	if err := w.checkSourceCopyScope(ctx, job.ChannelID, w.accountID); err != nil {
		return w.finish(ctx, job, sourceCopyPending, sourceCopyReceiptPending)
	}
	// The persisted cleanup flag records intent. An interrupted cleanup may
	// have based that intent on a projection that has since been repaired.
	if job.Attempt > 0 {
		outcome, found, err := w.copyCommitOutcome(ctx, job)
		if err != nil || !found {
			return w.finish(ctx, job, sourceCopyPending, sourceCopyReceiptPending)
		}
		switch outcome.Outcome {
		case projection.OperationApplied:
			job.Result.FileID = outcome.MsgID
			return w.finish(ctx, job, sourceCopySaved, "")
		case projection.OperationRejected:
		default:
			return w.finish(ctx, job, sourceCopyPending, sourceCopyReceiptPending)
		}
	}
	ids, err := w.cleanableBodies(ctx, job)
	if err != nil {
		return w.finish(ctx, job, sourceCopyPending, "The save failed. Check again to finish cleanup.")
	}
	if len(ids) > 0 {
		if err := w.deleteMessagesChunked(ctx, w.peer, ids); err != nil {
			return w.finish(ctx, job, sourceCopyPending, "The save failed. Check again to finish cleanup.")
		}
	}
	if err := projection.DeleteFileParts(w.DB, job.ChannelID, job.bodyUUID()); err != nil {
		return SourceCopyResult{}, err
	}
	if !job.hasBodies() {
		return w.finish(ctx, job, sourceCopyFailed, "The copy could not finish. Check that the source file and destination folder still exist.")
	}
	if len(ids) < len(job.Parts) {
		return w.finish(ctx, job, sourceCopyFailed, "An attachment was removed from My Drive. Select the original file to copy it again.")
	}
	return w.finish(ctx, job, sourceCopyFailed, "The file could not be placed in that folder")
}

func (w sourceCopyWorker) cleanableBodies(ctx context.Context, job sourceCopyJob) ([]int64, error) {
	if _, err := w.projectedBodies(ctx, job); err != nil {
		return nil, err
	}
	knownIDs := job.bodyIDs()
	if err := w.confirmUnreferencedBodies(ctx, job, knownIDs); err != nil {
		return nil, err
	}
	messages, err := w.readBodyMessages(ctx, knownIDs)
	if err != nil {
		return nil, err
	}
	ids := make([]int64, 0, len(knownIDs))
	for index, part := range job.Parts {
		if part.BodyID <= 0 {
			continue
		}
		switch err := job.verifyBodyMessage(index, messages); {
		case errors.Is(err, tgclient.ErrMessageNotFound):
		case err != nil:
			return nil, err
		default:
			ids = append(ids, part.BodyID)
		}
	}
	return ids, nil
}

func (w sourceCopyWorker) confirmUnreferencedBodies(ctx context.Context, job sourceCopyJob, ids []int64) error {
	query := `SELECT EXISTS (
		SELECT 1 FROM file_revisions WHERE channel_id=? AND upload_uuid=? AND upload_uuid != ''`
	args := make([]any, 0, len(ids)+3)
	for _, id := range ids {
		args = append(args, id)
	}
	args = append(args, job.ChannelID, job.bodyUUID())
	if len(ids) > 0 {
		// SQLite can use both bounds of the partial content index when the
		// range starts and ends at each known message ID.
		query = "WITH known_bodies(msg_id) AS (VALUES " + strings.Repeat("(?),", len(ids)-1) + "(?)) " + query + `
			UNION ALL
			SELECT 1 FROM known_bodies CROSS JOIN file_revisions r
			WHERE r.channel_id=? AND r.content_msg_id > 0
				AND r.content_msg_id BETWEEN known_bodies.msg_id AND known_bodies.msg_id`
		args = append(args, job.ChannelID)
	}
	var referenced bool
	if err := w.DB.QueryRowContext(ctx, query+")", args...).Scan(&referenced); err != nil {
		return fmt.Errorf("check destination attachment ownership: %w", err)
	}
	if referenced {
		return errors.New("Destination attachment ownership could not be confirmed")
	}
	return nil
}

func (w sourceCopyWorker) finish(ctx context.Context, job sourceCopyJob, status, message string) (SourceCopyResult, error) {
	job, _, err := w.stop(ctx, job, status, message)
	return job.result(), err
}

func (w sourceCopyWorker) stop(ctx context.Context, job sourceCopyJob, status, message string) (sourceCopyJob, bool, error) {
	job.Result.Status, job.Result.Message = status, message
	return job, true, w.persistSourceCopyJobAfterAttempt(ctx, job)
}

// Only this local receipt update outlives a canceled call. Source-copy work
// drains before the owning account's database is erased on full logout.
func (w sourceCopyWorker) persistSourceCopyJobAfterAttempt(ctx context.Context, job sourceCopyJob) error {
	receiptCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
	defer cancel()
	if err := w.persistSourceCopyJob(receiptCtx, job); err != nil {
		return fmt.Errorf("save receipt: %w", err)
	}
	return nil
}
