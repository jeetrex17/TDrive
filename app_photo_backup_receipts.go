package main

import (
	"context"
	"database/sql"
	"log/slog"
	"strings"

	"TDrive/backend/photobackup"
	"TDrive/backend/projection"
)

// photoBackupReceiptBudget caps how many receipts one run compares with the
// drive. The ledger keeps a durable cursor, so a library far larger than this
// is walked across consecutive runs instead of spending a launch on it: every
// database in the app shares one SQLite connection, and a sweep that outran
// this budget would stall the file list and the gallery behind it.
const photoBackupReceiptBudget = 2048

// reconcilePhotoBackupReceipts brings the ledger back in line with the drive
// before a run queues anything. Doing it here rather than inside the upload
// loop keeps the per-file path free of drive lookups and still covers every
// way a run can start: launch, the periodic desktop pass, and the panel's own
// "Back up now".
func (a *App) reconcilePhotoBackupReceipts(ctx context.Context, engine *photobackup.Engine, scope photobackup.Scope) {
	sweep, err := engine.ReconcileReceipts(ctx, scope, a.photoBackupLostFiles, photoBackupReceiptBudget)
	if err != nil {
		if ctx.Err() == nil {
			slog.Warn("photo backup: receipt reconciliation failed", "drive_id", scope.DriveID, "error", err)
		}
		return
	}
	if sweep.Gone > 0 || sweep.Restored > 0 {
		a.emit("photo-backup:state")
	}
}

func (a *App) photoBackupLostFiles(ctx context.Context, driveID int64, messageIDs []int64) ([]int64, error) {
	service, err := a.requireFolderService()
	if err != nil {
		return nil, err
	}
	if service.DB == nil {
		return nil, errBackendUnavailable
	}
	return photoBackupLostFiles(ctx, service.DB, driveID, messageIDs)
}

// photoBackupLostFiles is the drive's half of the comparison: it reports which
// of one batch's uploads the drive can no longer give back.
//
// Two things deliberately do not count as lost. A file in the trash is still
// the user's to restore, so a delete they may undo leaves its receipt alone and
// the purge, when it comes, is what demotes it. A message id above everything
// the projection has seen has not been indexed yet -- on a fresh install that
// is the whole library, and calling it lost would offer to upload a second copy
// of every photo already in the drive.
func photoBackupLostFiles(ctx context.Context, db *sql.DB, driveID int64, messageIDs []int64) ([]int64, error) {
	if db == nil || len(messageIDs) == 0 {
		return nil, nil
	}
	var frontier int64
	if err := db.QueryRowContext(ctx, `SELECT COALESCE(MAX(msg_id),0) FROM files WHERE channel_id=?`, driveID).Scan(&frontier); err != nil {
		return nil, err
	}
	if frontier == 0 {
		return nil, nil
	}
	candidates := make([]int64, 0, len(messageIDs))
	for _, id := range messageIDs {
		if id <= frontier {
			candidates = append(candidates, id)
		}
	}
	if len(candidates) == 0 {
		return nil, nil
	}
	held, err := photoBackupHeldFiles(ctx, db, driveID, candidates)
	if err != nil || len(held) == len(candidates) {
		return nil, err
	}
	lost := make([]int64, 0, len(candidates)-len(held))
	for _, id := range candidates {
		if _, ok := held[id]; !ok {
			lost = append(lost, id)
		}
	}
	return lost, nil
}

// photoBackupHeldFiles resolves a whole batch in one statement. Both sides of
// it are primary-key work -- files by (channel, message), trash by object id --
// so a batch costs one lookup per receipt in a single round trip rather than a
// query per receipt on the connection the rest of the app is waiting for.
func photoBackupHeldFiles(ctx context.Context, db *sql.DB, driveID int64, ids []int64) (map[int64]struct{}, error) {
	var query strings.Builder
	query.Grow(230 + 2*len(ids))
	query.WriteString(`SELECT f.msg_id FROM files f WHERE f.channel_id=? AND f.msg_id IN (`)
	args := make([]any, 0, len(ids)+2)
	args = append(args, driveID)
	for i, id := range ids {
		if i > 0 {
			query.WriteByte(',')
		}
		query.WriteByte('?')
		args = append(args, id)
	}
	query.WriteString(`) AND (f.tombstoned=0 OR EXISTS(SELECT 1 FROM trash_entries t WHERE t.channel_id=f.channel_id AND t.object_id=?||CAST(f.msg_id AS TEXT)))`)
	args = append(args, projection.FileIDPrefix)
	rows, err := db.QueryContext(ctx, query.String(), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	held := make(map[int64]struct{}, len(ids))
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		held[id] = struct{}{}
	}
	return held, rows.Err()
}
