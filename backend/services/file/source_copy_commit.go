package file

import (
	"context"
	"errors"
	"fmt"

	"TDrive/backend/projection"
	"TDrive/backend/tgclient"
)

const sourceCopyNameAttempts = 200

func (job sourceCopyJob) commitOp() projection.Op {
	return projection.Op{
		Type: projection.OpFileCommit, ProtocolVersion: 1,
		OpID:   job.commitOpID(job.Attempt),
		Parent: job.Result.ParentID, Name: job.Candidate,
		UploadUUID: job.bodyUUID(), PartCount: len(job.Parts), FileSize: job.size(), FileUploadTime: job.StartedAt,
	}
}

func (job sourceCopyJob) commitOpID(attempt int) string {
	return fmt.Sprintf("%s:%d", job.Result.ID, attempt)
}

func (job sourceCopyJob) copyCandidateName(attempt int) string {
	if attempt == 1 {
		return job.Result.Name
	}
	return projection.PortableFileName(job.Result.Name, attempt)
}

func (w sourceCopyWorker) publish(ctx context.Context, job sourceCopyJob) (SourceCopyResult, error) {
	for {
		attempt := job.Attempt
		if attempt > 0 {
			outcome, found, err := w.copyCommitOutcome(ctx, job)
			switch {
			case err != nil || (!found && job.CommitID > 0):
				return w.finish(ctx, job, sourceCopyPending, sourceCopyReceiptPending)
			case found && outcome.Outcome == projection.OperationApplied:
				job.Result.FileID = outcome.MsgID
				return w.finish(ctx, job, sourceCopySaved, "")
			case found && outcome.Outcome == projection.OperationRejected:
				if outcome.Error != projection.ErrNameConflict.Error() {
					return w.abandonBody(ctx, job)
				}
				attempt++
			case found:
				return w.finish(ctx, job, sourceCopyPending, sourceCopyReceiptPending)
			}
		}
		if err := w.verifyBodies(ctx, job); err != nil {
			// A confirmed missing attachment cannot be published. An attempted
			// commit can still own it until ordered replay certifies rejection.
			if errors.Is(err, tgclient.ErrMessageNotFound) && (job.Attempt == 0 || attempt > job.Attempt) {
				return w.abandonBody(ctx, job)
			}
			return w.finish(ctx, job, sourceCopyPending, "Could not verify the attachment in My Drive. Check again from Activity.")
		}
		if attempt != job.Attempt || job.Attempt == 0 {
			next, found, err := w.newCopyCommit(ctx, job, max(attempt, 1))
			if err != nil {
				return w.finish(ctx, job, sourceCopyPending, sourceCopyReceiptPending)
			}
			if !found {
				return w.abandonBody(ctx, job)
			}
			job = next
		}
		sent, err := w.sendCopyCommit(ctx, job)
		job = sent
		if err != nil || w.sync(ctx) != nil {
			return w.finish(ctx, job, sourceCopyPending, sourceCopyReceiptPending)
		}
	}
}

func (w sourceCopyWorker) copyCommitOutcome(ctx context.Context, job sourceCopyJob) (projection.ProjectionOperation, bool, error) {
	op := job.commitOp()
	snapshot, err := projection.ReadOperationSnapshot(ctx, w.DB, job.ChannelID, op.OpID)
	if err != nil {
		return projection.ProjectionOperation{}, false, err
	}
	outcome := snapshot.Operation
	if !snapshot.Ordered || snapshot.Watermark < max(outcome.MsgID, job.lastBodyID()) {
		return outcome, false, errors.New("Could not confirm ordered replay in My Drive")
	}
	if outcome.MsgID == 0 {
		return outcome, false, nil
	}
	if outcome.OpType != op.Type || outcome.MsgID <= job.lastBodyID() ||
		(job.CommitID > 0 && job.CommitID != outcome.MsgID) {
		return outcome, false, errors.New("Could not match the ordered My Drive commit")
	}
	msg, err := w.TG.GetChannelMessage(ctx, w.peer, outcome.MsgID)
	if err != nil || msg.HasMedia || !matchesCopyMessage(job, msg, outcome.MsgID, projection.Format(op)) {
		return outcome, false, errors.New("Could not verify the My Drive commit")
	}
	return outcome, true, nil
}

func (w sourceCopyWorker) newCopyCommit(ctx context.Context, job sourceCopyJob, start int) (sourceCopyJob, bool, error) {
	if _, err := w.validParent(job.ChannelID, job.Result.ParentID, "target"); err != nil {
		return job, false, nil
	}
	for attempt := start; attempt <= sourceCopyNameAttempts; attempt++ {
		name := job.copyCandidateName(attempt)
		_, found, err := projection.LiveDirentByName(w.DB, job.ChannelID, job.Result.ParentID, name)
		if err != nil {
			return job, false, err
		}
		if !found {
			job.Attempt, job.Candidate, job.CommitID = attempt, name, 0
			return job, true, w.persistSourceCopyJob(ctx, job)
		}
	}
	return job, false, nil
}

func (w sourceCopyWorker) sendCopyCommit(ctx context.Context, job sourceCopyJob) (sourceCopyJob, error) {
	if err := w.checkSourceCopyScope(ctx, job.ChannelID, w.accountID); err != nil {
		return job, err
	}
	op := job.commitOp()
	randomID, err := tgclient.StableRandomID(op.OpID, "source-copy:commit")
	if err != nil {
		return job, err
	}
	msgID, err := tgclient.SendControlIdempotent(ctx, w.TG, w.peer, projection.Format(op), true, randomID)
	if err != nil {
		return job, err
	}
	if msgID <= 0 {
		return job, tgclient.ErrSendOutcomeUnknown
	}
	job.CommitID = msgID
	return job, w.persistSourceCopyJobAfterAttempt(ctx, job)
}

func (w sourceCopyWorker) abandonBody(ctx context.Context, job sourceCopyJob) (SourceCopyResult, error) {
	job.CleaningUp = true
	if err := w.persistSourceCopyJobAfterAttempt(ctx, job); err != nil {
		return job.result(), err
	}
	return w.cleanup(ctx, job)
}
