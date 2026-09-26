package photobackup

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"slices"
	"strings"
)

// receiptBatch bounds one round of reconciliation. The ledger read, the
// caller's drive lookup and the two status updates all carry at most this many
// message ids, which keeps every parameter list far inside SQLite's limit and
// never holds the single shared connection for long.
const receiptBatch = 256

// ReceiptGoneReason is stored on a receipt whose uploaded file has left the
// drive. It reaches the backup panel's status line verbatim, so it is written
// for the user and names the action that fixes it.
const ReceiptGoneReason = "Some backed-up files are no longer in the drive. Retry to upload them again."

// ReceiptSweep reports what one ReconcileReceipts call did.
type ReceiptSweep struct {
	// Examined counts receipts compared against the drive this call.
	Examined int
	// Gone counts completed receipts demoted because their uploaded file is no
	// longer in the drive, Restored counts the reverse.
	Gone, Restored int
	// Complete is true when the walk reached the end of the ledger and rewound,
	// so the next call starts a fresh cycle.
	Complete bool
}

// ReconcileReceipts compares one bounded slice of the ledger's receipts with
// the drive and moves each affected job between Complete and Missing.
//
// It exists because a receipt only ever proved that an upload once succeeded.
// The ledger and the drive are separate databases and the drive can lose a
// file — a delete the user later purges, a message removed in Telegram — after
// which "backed up" is a lie no local change can correct, and neither a scan
// (the job is already Complete) nor RetryErrors (the job is not an error)
// would ever send the file again.
//
// Reconciliation is deliberately not an upload trigger. A demoted job lands in
// Missing, which RunOnce never picks up, so a deliberate delete stays deleted
// until the user asks for it back; only the explicit Retry action promotes it.
// The reverse move makes that safe to run while a delete is still recoverable:
// if the file comes back, so does its receipt, with nothing re-sent.
//
// budget caps the receipts examined per call and the durable cursor carries the
// walk across calls, so a large ledger costs a bounded slice of work per run
// rather than one drive lookup per receipt at every launch.
func (e *Engine) ReconcileReceipts(ctx context.Context, scope Scope, index RemoteIndex, budget int) (ReceiptSweep, error) {
	if !scope.valid() || index == nil {
		return ReceiptSweep{}, ErrInvalid
	}
	if budget <= 0 {
		budget = receiptBatch
	}
	cursor, err := e.receiptCursor(ctx, scope)
	if err != nil {
		return ReceiptSweep{}, err
	}
	missing, err := e.statusCount(ctx, scope, Missing)
	if err != nil {
		return ReceiptSweep{}, err
	}
	var sweep ReceiptSweep
	for sweep.Examined < budget {
		size := min(receiptBatch, budget-sweep.Examined)
		receipts, err := e.receiptPage(ctx, scope, cursor, size)
		if err != nil {
			return sweep, err
		}
		if len(receipts) == 0 {
			sweep.Complete = true
			break
		}
		gone, err := index(ctx, scope.DriveID, receipts)
		if err != nil {
			return sweep, err
		}
		demoted, err := e.moveReceipts(ctx, scope, Complete, Missing, gone, ReceiptGoneReason)
		if err != nil {
			return sweep, err
		}
		sweep.Gone += demoted
		// Promoting costs a statement only where a Missing job can exist. Every
		// other sweep is a read plus, at most, the demotion above.
		if missing > 0 {
			restored, err := e.moveReceipts(ctx, scope, Missing, Complete, exclude(receipts, gone), "")
			if err != nil {
				return sweep, err
			}
			sweep.Restored += restored
			missing -= int64(restored)
		}
		sweep.Examined += len(receipts)
		cursor = receipts[len(receipts)-1]
		if len(receipts) < size {
			sweep.Complete = true
			break
		}
		if err := e.setReceiptCursor(ctx, scope, cursor); err != nil {
			return sweep, err
		}
	}
	// Rewinding on completion is what makes a file that comes back weeks later
	// reachable again; a cursor parked at the end would never revisit it.
	if sweep.Complete {
		cursor = 0
	}
	if err := e.setReceiptCursor(ctx, scope, cursor); err != nil {
		return sweep, err
	}
	if sweep.Gone > 0 || sweep.Restored > 0 {
		slog.Info("photobackup: receipts reconciled with the drive", "drive_id", scope.DriveID, "examined", sweep.Examined, "gone", sweep.Gone, "restored", sweep.Restored)
	} else {
		slog.Debug("photobackup: receipts match the drive", "drive_id", scope.DriveID, "examined", sweep.Examined, "cycle_complete", sweep.Complete)
	}
	return sweep, nil
}

// receiptPage reads the next ascending run of receipt ids. DISTINCT costs
// nothing on the ordered index and keeps a message id that somehow backs two
// jobs from consuming two slots of the batch.
func (e *Engine) receiptPage(ctx context.Context, scope Scope, after int64, limit int) ([]int64, error) {
	rows, err := e.db.QueryContext(ctx, `SELECT DISTINCT remote_message_id FROM photo_backup_jobs WHERE account_id=? AND drive_id=? AND remote_message_id>? AND status IN (?,?) ORDER BY remote_message_id LIMIT ?`, scope.AccountID, scope.DriveID, after, Complete, Missing, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]int64, 0, limit)
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// moveReceipts retitles the jobs behind ids that are still in the from status.
// The status predicate is what makes a repeated sweep a no-op and keeps a job
// the user paused, retried or re-uploaded meanwhile out of the way; the
// maintained counters follow from the triggers on the status column.
func (e *Engine) moveReceipts(ctx context.Context, scope Scope, from, to JobStatus, ids []int64, reason string) (int, error) {
	if len(ids) == 0 {
		return 0, nil
	}
	var query strings.Builder
	query.Grow(160 + 2*len(ids))
	query.WriteString(`UPDATE photo_backup_jobs SET status=?,last_error=?,updated_at=? WHERE account_id=? AND drive_id=? AND status=? AND remote_message_id IN (`)
	args := make([]any, 0, 6+len(ids))
	args = append(args, to, reason, e.options.Now().UnixNano(), scope.AccountID, scope.DriveID, from)
	for i, id := range ids {
		if i > 0 {
			query.WriteByte(',')
		}
		query.WriteByte('?')
		args = append(args, id)
	}
	query.WriteByte(')')
	result, err := e.db.ExecContext(ctx, query.String(), args...)
	if err != nil {
		return 0, err
	}
	changed, err := result.RowsAffected()
	return int(changed), err
}

// exclude returns the members of sorted that are not in drop. Both come from
// the same batch, so drop is short and a linear membership scan beats building
// a map for the handful of ids a healthy drive reports.
func exclude(sorted, drop []int64) []int64 {
	if len(drop) == 0 {
		return sorted
	}
	out := make([]int64, 0, len(sorted)-len(drop))
	for _, id := range sorted {
		if !slices.Contains(drop, id) {
			out = append(out, id)
		}
	}
	return out
}

func (e *Engine) receiptCursor(ctx context.Context, scope Scope) (int64, error) {
	var cursor int64
	err := e.db.QueryRowContext(ctx, `SELECT receipt_cursor FROM photo_backup_settings WHERE account_id=? AND drive_id=?`, scope.AccountID, scope.DriveID).Scan(&cursor)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	return cursor, err
}

func (e *Engine) setReceiptCursor(ctx context.Context, scope Scope, cursor int64) error {
	_, err := e.db.ExecContext(ctx, `UPDATE photo_backup_settings SET receipt_cursor=? WHERE account_id=? AND drive_id=?`, cursor, scope.AccountID, scope.DriveID)
	return err
}

// statusCount reads one maintained counter rather than counting jobs, so it
// stays constant-time on a ledger with a million receipts.
func (e *Engine) statusCount(ctx context.Context, scope Scope, status JobStatus) (int64, error) {
	var count int64
	err := e.db.QueryRowContext(ctx, `SELECT count FROM photo_backup_counts WHERE account_id=? AND drive_id=? AND status=?`, scope.AccountID, scope.DriveID, status).Scan(&count)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	return count, err
}
