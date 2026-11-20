package file

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
	"strings"

	"TDrive/backend/projection"
)

// Older sync builds could advance the watermark past a locally projected
// operation that Telegram omitted from history. Reconstruct the projection
// once for a channel with an interrupted legacy commit before trusting it.
func (w sourceCopyWorker) repairLegacyCopyReplay(ctx context.Context, channelID int64) error {
	tx, err := w.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS source_copy_replay_repairs (
			channel_id INTEGER PRIMARY KEY REFERENCES channels(channel_id) ON DELETE CASCADE
		)
	`); err != nil {
		return err
	}
	var repaired bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS (
		SELECT 1 FROM source_copy_replay_repairs WHERE channel_id=?
	)`, channelID).Scan(&repaired); err != nil || repaired {
		return err
	}
	snapshot, err := projection.ReadOperationSnapshot(ctx, tx, channelID, "")
	if err != nil {
		return err
	}
	if !snapshot.Ordered {
		return errors.New("could not repair My Drive before ordered sync")
	}
	if _, _, err := projection.RebuildProjectionTx(tx, channelID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO source_copy_replay_repairs (channel_id) VALUES (?)`, channelID); err != nil {
		return err
	}
	return tx.Commit()
}

// Old clients could advance to a new name after a stale rejection. Find an
// earlier applied operation by its job and attempt, then verify its receipt.
func (w sourceCopyWorker) recoverEarlierCopyCommit(ctx context.Context, job sourceCopyJob) (sourceCopyJob, bool, error) {
	priorAttempts := min(job.Attempt-1, sourceCopyNameAttempts)
	if priorAttempts <= 0 {
		return job, false, nil
	}
	args := make([]any, 0, priorAttempts+3)
	args = append(args, job.ChannelID, projection.OpFileCommit, projection.OperationApplied)
	for attempt := 1; attempt <= priorAttempts; attempt++ {
		args = append(args, job.commitOpID(attempt))
	}
	placeholders := strings.Repeat("?,", priorAttempts-1) + "?"
	var opID string
	err := w.DB.QueryRowContext(ctx, `
		SELECT op_id FROM projection_operations
		WHERE channel_id=? AND op_type=? AND outcome=? AND op_id IN (`+placeholders+`)
		ORDER BY msg_id LIMIT 1
	`, args...).Scan(&opID)
	if errors.Is(err, sql.ErrNoRows) {
		return job, false, nil
	}
	if err != nil {
		return job, false, err
	}
	suffix, _ := strings.CutPrefix(opID, job.Result.ID+":")
	attempt, err := strconv.Atoi(suffix)
	if err != nil || attempt <= 0 || attempt > priorAttempts || opID != job.commitOpID(attempt) {
		return job, false, errors.New("invalid earlier copy operation ID")
	}
	earlier := job
	earlier.Attempt, earlier.Candidate, earlier.CommitID = attempt, job.copyCandidateName(attempt), 0
	outcome, found, err := w.copyCommitOutcome(ctx, earlier)
	if err != nil {
		return job, false, err
	}
	if !found || outcome.Outcome != projection.OperationApplied {
		return job, false, errors.New("could not verify earlier copy commit")
	}
	earlier.CommitID, earlier.Result.FileID = outcome.MsgID, outcome.MsgID
	earlier.CleaningUp = false
	return earlier, true, nil
}
