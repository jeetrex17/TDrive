package projection

import (
	"database/sql"
	"errors"
	"fmt"
)

// advanceActiveFileRevision re-keys the single active body reference when a
// metadata-only mutation advances the object's CAS revision. It does not copy
// content, so retention still counts bodies rather than renames or moves.
func advanceActiveFileRevision(tx *sql.Tx, channelID, fileMsgID, currentRevision, nextRevision int64) error {
	if nextRevision != currentRevision+1 {
		return fmt.Errorf("projection: non-sequential file revision %d -> %d", currentRevision, nextRevision)
	}

	var activeRevision int64
	err := tx.QueryRow(`
		SELECT revision FROM file_revisions
		WHERE channel_id=? AND file_msg_id=? AND retained_until=0
		ORDER BY revision DESC
		LIMIT 1
	`, channelID, fileMsgID).Scan(&activeRevision)
	if errors.Is(err, sql.ErrNoRows) {
		// Compatibility for legacy/directly-seeded rows: reconstruct the active
		// body from the already-advanced files row instead of leaving CAS and
		// content history out of sync.
		return ensureLegacyFileRevision(tx, channelID, fileMsgID, 0)
	}
	if err != nil {
		return fmt.Errorf("projection: read active file revision: %w", err)
	}
	if activeRevision != currentRevision {
		return fmt.Errorf(
			"projection: active body revision %d does not match object revision %d",
			activeRevision, currentRevision,
		)
	}

	result, err := tx.Exec(`
		UPDATE file_revisions SET revision=?
		WHERE channel_id=? AND file_msg_id=? AND revision=? AND retained_until=0
	`, nextRevision, channelID, fileMsgID, currentRevision)
	if err != nil {
		return fmt.Errorf("projection: advance active file revision: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("projection: count advanced file revision: %w", err)
	}
	if affected != 1 {
		return fmt.Errorf("projection: advanced active file revisions=%d, want 1", affected)
	}
	return nil
}

func liveFileRevision(tx *sql.Tx, channelID, fileMsgID int64) (int64, bool, error) {
	var revision int64
	err := tx.QueryRow(`
		SELECT revision FROM files
		WHERE channel_id=? AND msg_id=? AND tombstoned=0
	`, channelID, fileMsgID).Scan(&revision)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, fmt.Errorf("projection: read live file revision: %w", err)
	}
	return revision, true, nil
}

func applyLegacyFileRename(tx *sql.Tx, channelID, fileMsgID int64, name string) error {
	currentRevision, found, err := liveFileRevision(tx, channelID, fileMsgID)
	if err != nil || !found {
		return err
	}
	nextRevision := currentRevision + 1
	result, err := tx.Exec(`
		UPDATE files SET name=?, revision=?
		WHERE channel_id=? AND msg_id=? AND tombstoned=0 AND revision=?
	`, name, nextRevision, channelID, fileMsgID, currentRevision)
	if err != nil {
		return fmt.Errorf("projection: rename file: %w", err)
	}
	if err := requireOneUpdatedRow(result, "rename file"); err != nil {
		return err
	}
	if err := advanceActiveFileRevision(tx, channelID, fileMsgID, currentRevision, nextRevision); err != nil {
		return err
	}
	return syncLegacyFileDirent(tx, channelID, fileMsgID)
}

func applyLegacyFileMove(tx *sql.Tx, channelID, fileMsgID int64, parentID string) error {
	currentRevision, found, err := liveFileRevision(tx, channelID, fileMsgID)
	if err != nil || !found {
		return err
	}
	nextRevision := currentRevision + 1
	result, err := tx.Exec(`
		UPDATE files SET parent_id=?, revision=?
		WHERE channel_id=? AND msg_id=? AND tombstoned=0 AND revision=?
	`, parentID, nextRevision, channelID, fileMsgID, currentRevision)
	if err != nil {
		return fmt.Errorf("projection: move file: %w", err)
	}
	if err := requireOneUpdatedRow(result, "move file"); err != nil {
		return err
	}
	if err := advanceActiveFileRevision(tx, channelID, fileMsgID, currentRevision, nextRevision); err != nil {
		return err
	}
	return syncLegacyFileDirent(tx, channelID, fileMsgID)
}

func applyLegacyFileTomb(tx *sql.Tx, channelID, fileMsgID int64) error {
	currentRevision, found, err := liveFileRevision(tx, channelID, fileMsgID)
	if err != nil || !found {
		return err
	}
	nextRevision := currentRevision + 1
	result, err := tx.Exec(`
		UPDATE files SET tombstoned=1, revision=?
		WHERE channel_id=? AND msg_id=? AND tombstoned=0 AND revision=?
	`, nextRevision, channelID, fileMsgID, currentRevision)
	if err != nil {
		return fmt.Errorf("projection: tombstone file: %w", err)
	}
	if err := requireOneUpdatedRow(result, "tombstone file"); err != nil {
		return err
	}
	if err := advanceActiveFileRevision(tx, channelID, fileMsgID, currentRevision, nextRevision); err != nil {
		return err
	}
	return syncLegacyFileDirent(tx, channelID, fileMsgID)
}

func requireOneUpdatedRow(result sql.Result, operation string) error {
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("projection: count %s rows: %w", operation, err)
	}
	if affected != 1 {
		return fmt.Errorf("projection: %s rows=%d, want 1", operation, affected)
	}
	return nil
}
