package photobackup

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"
	"time"
)

type Engine struct {
	db      *sql.DB
	options Options
}

func Open(db *sql.DB, options Options) (*Engine, error) {
	if db == nil {
		return nil, ErrInvalid
	}
	if options.PageSize <= 0 {
		options.PageSize = 128
	}
	if options.MaxAttempts <= 0 {
		options.MaxAttempts = 8
	}
	if options.BaseBackoff <= 0 {
		options.BaseBackoff = time.Minute
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	return &Engine{db: db, options: options}, nil
}

// schemaVersion is what PRAGMA user_version reads on a current ledger. Bump it
// with a new entry in migrationSteps; never edit an existing step, because a
// database in the wild may be sitting on any of the earlier versions.
const schemaVersion = 6

// migrationSteps upgrades one version at a time. from is the version a step
// starts at, so a ledger at any earlier release replays every later step in
// one transaction.
var migrationSteps = []struct {
	from int
	what string
	sql  string
}{
	{1, "pause state", `ALTER TABLE photo_backup_settings ADD COLUMN manual_paused INTEGER NOT NULL DEFAULT 0`},
	{2, "charging policy", `ALTER TABLE photo_backup_settings DROP COLUMN charging_only`},
	{3, "capture time", `ALTER TABLE photo_backup_jobs ADD COLUMN captured_at INTEGER NOT NULL DEFAULT 0`},
	{4, "receipt cursor", `ALTER TABLE photo_backup_settings ADD COLUMN receipt_cursor INTEGER NOT NULL DEFAULT 0`},
	{4, "receipt index", receiptIndexDDL},
	{5, "source relative dir", `ALTER TABLE photo_backup_jobs ADD COLUMN rel_dir TEXT NOT NULL DEFAULT ''`},
}

// receiptIndexDDL covers the receipt walk end to end: the partial predicate
// holds only jobs that ever produced a file, and status rides along so a page
// of the walk is answered from the index without touching the table.
const receiptIndexDDL = `CREATE INDEX IF NOT EXISTS photo_backup_jobs_receipts ON photo_backup_jobs(account_id,drive_id,remote_message_id,status) WHERE remote_message_id>0`

func (e *Engine) Migrate(ctx context.Context) error {
	var version int
	if err := e.db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		return err
	}
	switch {
	case version == schemaVersion:
		return nil
	case version > schemaVersion:
		return fmt.Errorf("photobackup: unsupported database version %d", version)
	case version == 0:
		return e.createSchema(ctx)
	}
	tx, err := e.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, step := range migrationSteps {
		if version > step.from {
			continue
		}
		if _, err := tx.ExecContext(ctx, step.sql); err != nil {
			return fmt.Errorf("photobackup migrate %s: %w", step.what, err)
		}
	}
	if _, err := tx.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version=%d", schemaVersion)); err != nil {
		return err
	}
	return tx.Commit()
}

// createSchema lays down a current ledger in one go. The counter backfill in
// it is a one-time cost on an empty database, not a million-row scan on every
// launch. This package owns its separate SQLite file.
func (e *Engine) createSchema(ctx context.Context) error {
	_, err := e.db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS photo_backup_settings(account_id TEXT NOT NULL,drive_id INTEGER NOT NULL,enabled INTEGER NOT NULL,photos INTEGER NOT NULL,videos INTEGER NOT NULL,future_only INTEGER NOT NULL,wifi_only INTEGER NOT NULL,destination_parent_id TEXT NOT NULL,encrypt INTEGER NOT NULL,manual_paused INTEGER NOT NULL DEFAULT 0,receipt_cursor INTEGER NOT NULL DEFAULT 0,PRIMARY KEY(account_id,drive_id));
CREATE TABLE IF NOT EXISTS photo_backup_sources(account_id TEXT NOT NULL,drive_id INTEGER NOT NULL,source_id TEXT NOT NULL,kind TEXT NOT NULL,root TEXT NOT NULL,name TEXT NOT NULL,enabled INTEGER NOT NULL,added_at INTEGER NOT NULL,scan_cursor TEXT NOT NULL DEFAULT '',scan_complete INTEGER NOT NULL DEFAULT 0,PRIMARY KEY(account_id,drive_id,source_id));
CREATE TABLE IF NOT EXISTS photo_backup_jobs(account_id TEXT NOT NULL,drive_id INTEGER NOT NULL,source_id TEXT NOT NULL,asset_id TEXT NOT NULL,version TEXT NOT NULL,path TEXT NOT NULL,name TEXT NOT NULL,media_type TEXT NOT NULL,resource_id TEXT NOT NULL DEFAULT '',modified_at INTEGER NOT NULL,captured_at INTEGER NOT NULL DEFAULT 0,rel_dir TEXT NOT NULL DEFAULT '',size INTEGER NOT NULL,status TEXT NOT NULL,attempts INTEGER NOT NULL DEFAULT 0,next_attempt_at INTEGER NOT NULL DEFAULT 0,last_error TEXT NOT NULL DEFAULT '',remote_message_id INTEGER NOT NULL DEFAULT 0,created_at INTEGER NOT NULL,updated_at INTEGER NOT NULL,PRIMARY KEY(account_id,drive_id,source_id,asset_id,version,resource_id));
CREATE INDEX IF NOT EXISTS photo_backup_jobs_ready ON photo_backup_jobs(account_id,drive_id,status,next_attempt_at,created_at);
CREATE TABLE IF NOT EXISTS photo_backup_counts(account_id TEXT NOT NULL,drive_id INTEGER NOT NULL,status TEXT NOT NULL,count INTEGER NOT NULL CHECK(count>=0),PRIMARY KEY(account_id,drive_id,status));
INSERT INTO photo_backup_counts(account_id,drive_id,status,count) SELECT account_id,drive_id,status,COUNT(*) FROM photo_backup_jobs GROUP BY account_id,drive_id,status ON CONFLICT(account_id,drive_id,status) DO NOTHING;
CREATE TRIGGER IF NOT EXISTS photo_backup_jobs_count_insert AFTER INSERT ON photo_backup_jobs BEGIN INSERT INTO photo_backup_counts(account_id,drive_id,status,count) VALUES(NEW.account_id,NEW.drive_id,NEW.status,1) ON CONFLICT(account_id,drive_id,status) DO UPDATE SET count=count+1; END;
CREATE TRIGGER IF NOT EXISTS photo_backup_jobs_count_delete AFTER DELETE ON photo_backup_jobs BEGIN UPDATE photo_backup_counts SET count=count-1 WHERE account_id=OLD.account_id AND drive_id=OLD.drive_id AND status=OLD.status; DELETE FROM photo_backup_counts WHERE account_id=OLD.account_id AND drive_id=OLD.drive_id AND status=OLD.status AND count=0; END;
CREATE TRIGGER IF NOT EXISTS photo_backup_jobs_count_status AFTER UPDATE OF status ON photo_backup_jobs WHEN OLD.status<>NEW.status BEGIN UPDATE photo_backup_counts SET count=count-1 WHERE account_id=OLD.account_id AND drive_id=OLD.drive_id AND status=OLD.status; DELETE FROM photo_backup_counts WHERE account_id=OLD.account_id AND drive_id=OLD.drive_id AND status=OLD.status AND count=0; INSERT INTO photo_backup_counts(account_id,drive_id,status,count) VALUES(NEW.account_id,NEW.drive_id,NEW.status,1) ON CONFLICT(account_id,drive_id,status) DO UPDATE SET count=count+1; END;`)
	if err == nil {
		_, err = e.db.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS photo_backup_jobs_native_identity ON photo_backup_jobs(account_id,drive_id,asset_id,version,resource_id,path);
CREATE INDEX IF NOT EXISTS photo_backup_jobs_last_error ON photo_backup_jobs(account_id,drive_id,updated_at DESC) WHERE last_error<>''`+";\n"+receiptIndexDDL)
	}
	if err != nil {
		return fmt.Errorf("photobackup migrate: %w", err)
	}
	_, err = e.db.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version=%d", schemaVersion))
	return err
}

func (e *Engine) PutSettings(ctx context.Context, s Settings) error {
	if !s.Scope.valid() {
		return ErrInvalid
	}
	// Manual pause has its own API so a concurrent settings save cannot
	// accidentally resume a user-paused scope.
	_, err := e.db.ExecContext(ctx, `INSERT INTO photo_backup_settings(account_id,drive_id,enabled,photos,videos,future_only,wifi_only,destination_parent_id,encrypt,manual_paused) VALUES(?,?,?,?,?,?,?,?,?,?) ON CONFLICT(account_id,drive_id) DO UPDATE SET enabled=excluded.enabled,photos=excluded.photos,videos=excluded.videos,future_only=excluded.future_only,wifi_only=excluded.wifi_only,destination_parent_id=excluded.destination_parent_id,encrypt=excluded.encrypt`, s.Scope.AccountID, s.Scope.DriveID, s.Enabled, s.Photos, s.Videos, s.FutureOnly, s.WiFiOnly, s.DestinationParentID, s.Encrypt, s.ManualPaused)
	return err
}
func (e *Engine) GetSettings(ctx context.Context, scope Scope) (Settings, error) {
	if !scope.valid() {
		return Settings{}, ErrInvalid
	}
	s := Settings{Scope: scope}
	err := e.db.QueryRowContext(ctx, `SELECT enabled,photos,videos,future_only,wifi_only,destination_parent_id,encrypt,manual_paused FROM photo_backup_settings WHERE account_id=? AND drive_id=?`, scope.AccountID, scope.DriveID).Scan(&s.Enabled, &s.Photos, &s.Videos, &s.FutureOnly, &s.WiFiOnly, &s.DestinationParentID, &s.Encrypt, &s.ManualPaused)
	return s, err
}

func (e *Engine) SetManualPaused(ctx context.Context, scope Scope, paused bool) error {
	if !scope.valid() {
		return ErrInvalid
	}
	result, err := e.db.ExecContext(ctx, `UPDATE photo_backup_settings SET manual_paused=? WHERE account_id=? AND drive_id=?`, paused, scope.AccountID, scope.DriveID)
	if err != nil {
		return err
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if changed == 0 {
		return sql.ErrNoRows
	}
	return nil
}
func (e *Engine) UpsertSource(ctx context.Context, s Source) error {
	if !s.Scope.valid() || s.ID == "" || s.Kind == "" || s.Root == "" {
		return ErrInvalid
	}
	if s.AddedAt.IsZero() {
		s.AddedAt = e.options.Now()
	}
	if s.Name == "" {
		s.Name = filepath.Base(s.Root)
	}
	var count int
	if err := e.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM photo_backup_sources WHERE account_id=? AND drive_id=? AND source_id<>?`, s.Scope.AccountID, s.Scope.DriveID, s.ID).Scan(&count); err != nil {
		return err
	}
	if count >= 256 {
		return fmt.Errorf("photobackup: source limit reached")
	}
	_, err := e.db.ExecContext(ctx, `INSERT INTO photo_backup_sources(account_id,drive_id,source_id,kind,root,name,enabled,added_at) VALUES(?,?,?,?,?,?,?,?) ON CONFLICT(account_id,drive_id,source_id) DO UPDATE SET kind=excluded.kind,root=excluded.root,name=excluded.name,enabled=excluded.enabled`, s.Scope.AccountID, s.Scope.DriveID, s.ID, s.Kind, s.Root, s.Name, s.Enabled, s.AddedAt.UnixNano())
	return err
}
func (e *Engine) RemoveSource(ctx context.Context, scope Scope, id string) error {
	if !scope.valid() || id == "" {
		return ErrInvalid
	}
	tx, err := e.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var uploading int
	if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM photo_backup_jobs WHERE account_id=? AND drive_id=? AND source_id=? AND status=?`, scope.AccountID, scope.DriveID, id, Uploading).Scan(&uploading); err != nil {
		return err
	}
	if uploading > 0 {
		return fmt.Errorf("photobackup: source has an active upload")
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM photo_backup_jobs WHERE account_id=? AND drive_id=? AND source_id=? AND status<>?`, scope.AccountID, scope.DriveID, id, Complete); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM photo_backup_sources WHERE account_id=? AND drive_id=? AND source_id=?`, scope.AccountID, scope.DriveID, id); err != nil {
		return err
	}
	return tx.Commit()
}
func (e *Engine) ListSources(ctx context.Context, scope Scope) ([]Source, error) {
	if !scope.valid() {
		return nil, ErrInvalid
	}
	rows, err := e.db.QueryContext(ctx, `SELECT source_id,kind,root,name,enabled,added_at FROM photo_backup_sources WHERE account_id=? AND drive_id=? ORDER BY source_id`, scope.AccountID, scope.DriveID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Source
	for rows.Next() {
		s := Source{Scope: scope}
		var n int64
		if err := rows.Scan(&s.ID, &s.Kind, &s.Root, &s.Name, &s.Enabled, &n); err != nil {
			return nil, err
		}
		s.AddedAt = time.Unix(0, n)
		out = append(out, s)
	}
	return out, rows.Err()
}

// assetTakenAt is what "new items only" compares against: the capture time
// when the host reports one, otherwise the last modification. The fallback
// keeps desktop folders working, where no filesystem exposes a portable
// creation time, at the cost of an edit there counting as new.
func assetTakenAt(a Asset) time.Time {
	if !a.CapturedAt.IsZero() {
		return a.CapturedAt
	}
	return a.ModifiedAt
}

// The ledger stores an unknown capture time as 0 rather than the zero time's
// negative nanoseconds, so a row written by an older host reads back as
// unknown as well.
func unixNanoOrZero(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.UnixNano()
}

func timeOrZero(unixNano int64) time.Time {
	if unixNano == 0 {
		return time.Time{}
	}
	return time.Unix(0, unixNano)
}

func mediaAllowed(a Asset, s Settings) bool {
	kind := normalizedMediaKind(a)
	return kind == "photo" && s.Photos || kind == "video" && s.Videos
}

func normalizedMediaKind(a Asset) string {
	declared := strings.ToLower(strings.TrimSpace(a.MediaType))
	if strings.HasPrefix(declared, "image/") || declared == "photo" {
		return "photo"
	}
	if strings.HasPrefix(declared, "video/") || declared == "video" {
		return "video"
	}
	ext := strings.ToLower(filepath.Ext(a.Name))
	if ext == "" {
		ext = strings.ToLower(filepath.Ext(a.Path))
	}
	switch ext {
	case ".jpg", ".jpeg", ".png", ".heic", ".heif", ".webp", ".gif", ".tif", ".tiff", ".bmp", ".avif", ".dng", ".raw", ".arw", ".cr2", ".cr3", ".nef", ".orf", ".raf", ".rw2":
		return "photo"
	case ".mp4", ".mov", ".m4v", ".avi", ".mkv", ".webm", ".3gp":
		return "video"
	default:
		return ""
	}
}

func (e *Engine) EnqueuePage(ctx context.Context, scope Scope, sourceID string, assets []Asset) (int, error) {
	if !scope.valid() || sourceID == "" || len(assets) > 128 {
		return 0, ErrInvalid
	}
	settings, err := e.GetSettings(ctx, scope)
	if err != nil {
		return 0, err
	}
	var source Source
	var addedAt int64
	err = e.db.QueryRowContext(ctx, `SELECT kind,root,name,enabled,added_at FROM photo_backup_sources WHERE account_id=? AND drive_id=? AND source_id=?`, scope.AccountID, scope.DriveID, sourceID).Scan(&source.Kind, &source.Root, &source.Name, &source.Enabled, &addedAt)
	if err != nil {
		return 0, err
	}
	source.Scope = scope
	source.ID = sourceID
	source.AddedAt = time.Unix(0, addedAt)
	tx, err := e.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	now := e.options.Now().UnixNano()
	added := 0
	for _, a := range assets {
		if a.ID == "" || a.Version == "" || (!mediaAllowed(a, settings)) {
			continue
		}
		if a.Path == "" && a.ResourceID == "" {
			continue
		}
		if settings.FutureOnly && assetTakenAt(a).Before(source.AddedAt) {
			continue
		}
		a.MediaType = normalizedMediaKind(a)
		if a.Path == "" {
			var owner, status string
			var enabled sql.NullBool
			err := tx.QueryRowContext(ctx, `SELECT j.source_id,j.status,s.enabled FROM photo_backup_jobs j LEFT JOIN photo_backup_sources s USING(account_id,drive_id,source_id) WHERE j.account_id=? AND j.drive_id=? AND j.asset_id=? AND j.version=? AND j.resource_id=? AND j.path='' LIMIT 1`, scope.AccountID, scope.DriveID, a.ID, a.Version, a.ResourceID).Scan(&owner, &status, &enabled)
			if err == nil {
				if status != string(Complete) && (!enabled.Valid || !enabled.Bool) {
					if _, err = tx.ExecContext(ctx, `UPDATE photo_backup_jobs SET source_id=?,updated_at=? WHERE account_id=? AND drive_id=? AND source_id=? AND asset_id=? AND version=? AND resource_id=?`, sourceID, now, scope.AccountID, scope.DriveID, owner, a.ID, a.Version, a.ResourceID); err != nil {
						return 0, err
					}
				}
				continue
			}
			if err != sql.ErrNoRows {
				return 0, err
			}
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM photo_backup_jobs WHERE account_id=? AND drive_id=? AND source_id=? AND asset_id=? AND resource_id=? AND version<>? AND status IN (?,?)`, scope.AccountID, scope.DriveID, sourceID, a.ID, a.ResourceID, a.Version, Pending, Error); err != nil {
			return 0, err
		}
		res, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO photo_backup_jobs(account_id,drive_id,source_id,asset_id,version,path,name,media_type,resource_id,modified_at,captured_at,rel_dir,size,status,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, scope.AccountID, scope.DriveID, sourceID, a.ID, a.Version, a.Path, a.Name, a.MediaType, a.ResourceID, a.ModifiedAt.UnixNano(), unixNanoOrZero(a.CapturedAt), a.RelDir, a.Size, Pending, now, now)
		if err != nil {
			return 0, err
		}
		n, _ := res.RowsAffected()
		added += int(n)
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return added, nil
}

func (e *Engine) Discover(ctx context.Context, scope Scope, sourceID string, adapter Adapter) (int, error) {
	if !scope.valid() || sourceID == "" || adapter == nil {
		return 0, ErrInvalid
	}
	settings, err := e.GetSettings(ctx, scope)
	if err != nil {
		return 0, err
	}
	sources, err := e.ListSources(ctx, scope)
	if err != nil {
		return 0, err
	}
	var source Source
	found := false
	for _, s := range sources {
		if s.ID == sourceID {
			source = s
			found = true
			break
		}
	}
	if !found {
		return 0, sql.ErrNoRows
	}
	if !settings.Enabled || !source.Enabled {
		return 0, nil
	}
	cursor := ""
	total := 0
	for {
		page, err := adapter.Page(ctx, source, cursor, min(e.options.PageSize, 128))
		if err != nil {
			return total, err
		}
		n, err := e.EnqueuePage(ctx, scope, sourceID, page.Assets)
		if err != nil {
			return total, err
		}
		total += n
		if page.NextCursor == "" {
			slog.Info("photobackup: discovery finished", "drive_id", scope.DriveID, "source", sourceID, "queued", total)
			return total, nil
		}
		if page.NextCursor == cursor {
			return total, fmt.Errorf("photobackup: adapter cursor did not advance")
		}
		cursor = page.NextCursor
	}
}

// DiscoverPage scans and commits at most one bounded page. Its checkpoint is
// durable; an in-memory adapter cursor lost on restart is reset and rescanned.
func (e *Engine) DiscoverPage(ctx context.Context, scope Scope, sourceID string, adapter Adapter) (int, bool, error) {
	if !scope.valid() || sourceID == "" || adapter == nil {
		return 0, false, ErrInvalid
	}
	var cursor string
	var complete bool
	if err := e.db.QueryRowContext(ctx, `SELECT scan_cursor,scan_complete FROM photo_backup_sources WHERE account_id=? AND drive_id=? AND source_id=?`, scope.AccountID, scope.DriveID, sourceID).Scan(&cursor, &complete); err != nil {
		return 0, false, err
	}
	if complete {
		return 0, true, nil
	}
	sources, err := e.ListSources(ctx, scope)
	if err != nil {
		return 0, false, err
	}
	var source Source
	for _, s := range sources {
		if s.ID == sourceID {
			source = s
			break
		}
	}
	page, err := adapter.Page(ctx, source, cursor, min(e.options.PageSize, 128))
	if errors.Is(err, ErrCursorExpired) {
		cursor = ""
		page, err = adapter.Page(ctx, source, "", min(e.options.PageSize, 128))
	}
	if err != nil {
		return 0, false, err
	}
	n, err := e.EnqueuePage(ctx, scope, sourceID, page.Assets)
	if err != nil {
		return 0, false, err
	}
	done := page.NextCursor == ""
	slog.Debug("photobackup: discovery page", "drive_id", scope.DriveID, "source", sourceID, "seen", len(page.Assets), "queued", n, "source_complete", done)
	_, err = e.db.ExecContext(ctx, `UPDATE photo_backup_sources SET scan_cursor=?,scan_complete=? WHERE account_id=? AND drive_id=? AND source_id=?`, page.NextCursor, done, scope.AccountID, scope.DriveID, sourceID)
	return n, done, err
}
func (e *Engine) ResetDiscovery(ctx context.Context, scope Scope, sourceID string) error {
	if !scope.valid() || sourceID == "" {
		return ErrInvalid
	}
	_, err := e.db.ExecContext(ctx, `UPDATE photo_backup_sources SET scan_cursor='',scan_complete=0 WHERE account_id=? AND drive_id=? AND source_id=?`, scope.AccountID, scope.DriveID, sourceID)
	return err
}

// RecoverInterrupted quarantines ambiguous uploads. The remote commit may have
// succeeded, so only an explicit RetryErrors call can risk sending them again.
func (e *Engine) RecoverInterrupted(ctx context.Context, scope Scope) error {
	if !scope.valid() {
		return ErrInvalid
	}
	result, err := e.db.ExecContext(ctx, `UPDATE photo_backup_jobs SET status=?,last_error=?,updated_at=? WHERE account_id=? AND drive_id=? AND status=?`, Paused, "upload interrupted; remote outcome unknown", e.options.Now().UnixNano(), scope.AccountID, scope.DriveID, Uploading)
	if err != nil {
		return err
	}
	if held, _ := result.RowsAffected(); held > 0 {
		slog.Info("photobackup: held interrupted uploads for explicit retry", "drive_id", scope.DriveID, "jobs", held)
	}
	return nil
}
func (e *Engine) RetryErrors(ctx context.Context, scope Scope) error {
	if !scope.valid() {
		return ErrInvalid
	}
	// Missing joins the retryable states because Retry is the one place the
	// user asks for an upload to happen again, and it is the only way a file
	// the drive lost can come back. Nothing else promotes it.
	result, err := e.db.ExecContext(ctx, `UPDATE photo_backup_jobs SET status=?,attempts=0,next_attempt_at=0,last_error='',updated_at=? WHERE account_id=? AND drive_id=? AND status IN (?,?,?)`, Pending, e.options.Now().UnixNano(), scope.AccountID, scope.DriveID, Error, Paused, Missing)
	if err != nil {
		return err
	}
	requeued, _ := result.RowsAffected()
	slog.Info("photobackup: retry requeued held jobs", "drive_id", scope.DriveID, "jobs", requeued)
	return nil
}

func (e *Engine) RunOnce(ctx context.Context, scope Scope, upload Uploader) (int, error) {
	if !scope.valid() || upload == nil {
		return 0, ErrInvalid
	}
	settings, err := e.GetSettings(ctx, scope)
	if err != nil {
		return 0, err
	}
	if !settings.Enabled || settings.ManualPaused {
		slog.Debug("photobackup: run refused", "drive_id", scope.DriveID, "reason", refusalReason(settings))
		return 0, nil
	}
	rows, err := e.db.QueryContext(ctx, `SELECT j.source_id,j.asset_id,j.version,j.path,j.name,j.media_type,j.resource_id,j.modified_at,j.captured_at,j.rel_dir,j.size,j.attempts,s.kind,s.root,s.name,s.enabled,s.added_at FROM photo_backup_jobs j JOIN photo_backup_sources s USING(account_id,drive_id,source_id) WHERE j.account_id=? AND j.drive_id=? AND s.enabled=1 AND j.status IN (?,?) AND j.next_attempt_at<=? AND ((j.media_type='photo' AND ?) OR (j.media_type='video' AND ?)) ORDER BY j.created_at LIMIT 1`, scope.AccountID, scope.DriveID, Pending, Error, e.options.Now().UnixNano(), settings.Photos, settings.Videos)
	if err != nil {
		return 0, err
	}
	type item struct {
		source   Source
		asset    Asset
		attempts int
	}
	var items []item
	for rows.Next() {
		var x item
		var mt, captured, added int64
		x.source.Scope = scope
		if err := rows.Scan(&x.source.ID, &x.asset.ID, &x.asset.Version, &x.asset.Path, &x.asset.Name, &x.asset.MediaType, &x.asset.ResourceID, &mt, &captured, &x.asset.RelDir, &x.asset.Size, &x.attempts, &x.source.Kind, &x.source.Root, &x.source.Name, &x.source.Enabled, &added); err != nil {
			rows.Close()
			return 0, err
		}
		x.asset.ModifiedAt = time.Unix(0, mt)
		x.asset.CapturedAt = timeOrZero(captured)
		x.source.AddedAt = time.Unix(0, added)
		items = append(items, x)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return 0, err
	}
	rows.Close()
	done := 0
	for _, x := range items {
		if !x.source.Enabled {
			continue
		}
		now := e.options.Now()
		res, err := e.db.ExecContext(ctx, `UPDATE photo_backup_jobs SET status=?,updated_at=? WHERE account_id=? AND drive_id=? AND source_id=? AND asset_id=? AND version=? AND resource_id=? AND status IN (?,?)`, Uploading, now.UnixNano(), scope.AccountID, scope.DriveID, x.source.ID, x.asset.ID, x.asset.Version, x.asset.ResourceID, Pending, Error)
		if err != nil {
			return done, err
		}
		n, _ := res.RowsAffected()
		if n == 0 {
			continue
		}
		slog.Debug("photobackup: uploading", "drive_id", scope.DriveID, "source", x.source.ID, "name", x.asset.Name, "size", x.asset.Size, "attempt", x.attempts+1)
		result, upErr := upload(ctx, UploadRequest{Scope: scope, Source: x.source, Asset: x.asset, ChannelID: scope.DriveID, ParentID: settings.DestinationParentID, Encrypt: settings.Encrypt})
		persistCtx, cancelPersist := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		// A positive remote receipt is authoritative even if cancellation raced
		// with the uploader returning. Without one, cancellation leaves the
		// remote outcome uncertain and must require an explicit retry.
		if result.RemoteMessageID > 0 {
			upErr = nil
		} else if ctx.Err() != nil {
			// upErr is deliberately left alone: this asset is parked as Paused
			// with its reason in last_error and the loop moves on, so the
			// cancellation is never reported as a per-asset upload failure.
			slog.Info("photobackup: upload interrupted, held for explicit retry", "drive_id", scope.DriveID, "name", x.asset.Name)
			_, dbErr := e.db.ExecContext(persistCtx, `UPDATE photo_backup_jobs SET status=?,last_error=?,updated_at=? WHERE account_id=? AND drive_id=? AND source_id=? AND asset_id=? AND version=? AND resource_id=?`, Paused, "upload interrupted; remote outcome unknown", now.UnixNano(), scope.AccountID, scope.DriveID, x.source.ID, x.asset.ID, x.asset.Version, x.asset.ResourceID)
			cancelPersist()
			if dbErr != nil {
				return done, dbErr
			}
			continue
		}
		if upErr == nil && result.RemoteMessageID <= 0 {
			upErr = fmt.Errorf("photobackup: uploader returned no remote identity")
		}
		if upErr != nil {
			attempts := x.attempts + 1
			status := Error
			if attempts >= e.options.MaxAttempts {
				status = Paused
			}
			delay := e.options.BaseBackoff * time.Duration(1<<min(attempts-1, 10))
			slog.Warn("photobackup: upload failed", "drive_id", scope.DriveID, "name", x.asset.Name, "attempts", attempts, "status", status, "retry_in", delay, "error", upErr)
			_, dbErr := e.db.ExecContext(persistCtx, `UPDATE photo_backup_jobs SET status=?,attempts=?,next_attempt_at=?,last_error=?,updated_at=? WHERE account_id=? AND drive_id=? AND source_id=? AND asset_id=? AND version=? AND resource_id=?`, status, attempts, now.Add(delay).UnixNano(), upErr.Error(), now.UnixNano(), scope.AccountID, scope.DriveID, x.source.ID, x.asset.ID, x.asset.Version, x.asset.ResourceID)
			cancelPersist()
			if dbErr != nil {
				return done, dbErr
			}
			continue
		}
		_, err = e.db.ExecContext(persistCtx, `UPDATE photo_backup_jobs SET status=?,remote_message_id=?,last_error='',updated_at=? WHERE account_id=? AND drive_id=? AND source_id=? AND asset_id=? AND version=? AND resource_id=?`, Complete, result.RemoteMessageID, now.UnixNano(), scope.AccountID, scope.DriveID, x.source.ID, x.asset.ID, x.asset.Version, x.asset.ResourceID)
		cancelPersist()
		if err != nil {
			return done, err
		}
		slog.Info("photobackup: uploaded", "drive_id", scope.DriveID, "name", x.asset.Name, "size", x.asset.Size, "msg_id", result.RemoteMessageID)
		done++
	}
	return done, nil
}

// refusalReason names why a run did nothing, in the same words the panel uses,
// so a log answers "I pressed backup and nothing happened" on its own.
func refusalReason(settings Settings) string {
	if settings.ManualPaused {
		return "paused by the user"
	}
	return "backup is off for this drive"
}

func (e *Engine) Status(ctx context.Context, scope Scope) (Status, error) {
	if !scope.valid() {
		return Status{}, ErrInvalid
	}
	rows, err := e.db.QueryContext(ctx, `SELECT status,count FROM photo_backup_counts WHERE account_id=? AND drive_id=?`, scope.AccountID, scope.DriveID)
	if err != nil {
		return Status{}, err
	}
	defer rows.Close()
	var out Status
	for rows.Next() {
		var s string
		var n int64
		if err := rows.Scan(&s, &n); err != nil {
			return out, err
		}
		switch JobStatus(s) {
		case Pending:
			out.Pending = n
		case Uploading:
			out.Uploading = n
		case Complete:
			out.Complete = n
		case Error:
			out.Error = n
		case Paused:
			out.Paused = n
		case Missing:
			out.Missing = n
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return out, err
	}
	rows.Close()
	_ = e.db.QueryRowContext(ctx, `SELECT last_error FROM photo_backup_jobs WHERE account_id=? AND drive_id=? AND last_error<>'' ORDER BY updated_at DESC LIMIT 1`, scope.AccountID, scope.DriveID).Scan(&out.LastError)
	return out, nil
}
