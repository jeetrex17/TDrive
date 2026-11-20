package file

import (
	"context"
	"errors"

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
