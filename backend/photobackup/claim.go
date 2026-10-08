package photobackup

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

type claimedJob struct {
	source   Source
	asset    Asset
	attempts int
}

// Pending jobs use FIFO order without sorting the full queue. Failed jobs use
// the deadline index so a large future-backoff prefix is not scanned. The final
// merge sorts at most two candidates, preserving global discovery order. Media
// and disabled-source prefixes still require scanning; ready errors still sort.
const claimCandidateSQL = `SELECT rowid FROM (
SELECT rowid,created_at FROM (SELECT j.rowid,j.created_at FROM photo_backup_jobs j INDEXED BY photo_backup_jobs_ready_fifo
JOIN photo_backup_sources s USING(account_id,drive_id,source_id)
WHERE j.account_id=? AND j.drive_id=? AND s.enabled=1 AND j.status IN ('pending','error') AND j.status='pending' AND j.next_attempt_at<=?
AND ((j.media_type='photo' AND ?) OR (j.media_type='video' AND ?)) ORDER BY j.created_at,j.rowid LIMIT 1)
UNION ALL
SELECT rowid,created_at FROM (SELECT j.rowid,j.created_at FROM photo_backup_jobs j INDEXED BY photo_backup_jobs_ready
JOIN photo_backup_sources s USING(account_id,drive_id,source_id)
WHERE j.account_id=? AND j.drive_id=? AND s.enabled=1 AND j.status='error' AND j.next_attempt_at<=?
AND ((j.media_type='photo' AND ?) OR (j.media_type='video' AND ?)) ORDER BY j.created_at,j.rowid LIMIT 1)
) ORDER BY created_at,rowid LIMIT 1`

func (e *Engine) claimNext(ctx context.Context, scope Scope, settings Settings) (claimedJob, bool, error) {
	var job claimedJob
	tx, err := e.db.BeginTx(ctx, nil)
	if err != nil {
		return job, false, err
	}
	defer tx.Rollback()
	var modified, captured, added int64
	now := e.options.Now().UnixNano()
	// The write chooses the eligible row itself. A separate SELECT followed by
	// a status-only UPDATE could reclaim a failure after another worker gave it
	// a new backoff. RETURNING reads the attempts belonging to this claim.
	err = tx.QueryRowContext(ctx, `UPDATE photo_backup_jobs SET status=?,updated_at=?
WHERE rowid=(`+claimCandidateSQL+`)
RETURNING source_id,asset_id,version,path,name,media_type,resource_id,modified_at,captured_at,rel_dir,size,attempts`,
		Uploading, now, scope.AccountID, scope.DriveID, now, settings.Photos, settings.Videos,
		scope.AccountID, scope.DriveID, now, settings.Photos, settings.Videos).Scan(
		&job.source.ID, &job.asset.ID, &job.asset.Version, &job.asset.Path, &job.asset.Name, &job.asset.MediaType,
		&job.asset.ResourceID, &modified, &captured, &job.asset.RelDir, &job.asset.Size, &job.attempts)
	if errors.Is(err, sql.ErrNoRows) {
		return job, false, nil
	}
	if err != nil {
		return job, false, err
	}
	err = tx.QueryRowContext(ctx, `SELECT kind,root,name,enabled,added_at FROM photo_backup_sources WHERE account_id=? AND drive_id=? AND source_id=?`,
		scope.AccountID, scope.DriveID, job.source.ID).Scan(&job.source.Kind, &job.source.Root, &job.source.Name, &job.source.Enabled, &added)
	if err != nil {
		return job, false, err
	}
	job.source.Scope = scope
	job.source.AddedAt = time.Unix(0, added)
	job.asset.ModifiedAt = time.Unix(0, modified)
	job.asset.CapturedAt = timeOrZero(captured)
	if err := tx.Commit(); err != nil {
		return claimedJob{}, false, err
	}
	return job, true, nil
}
