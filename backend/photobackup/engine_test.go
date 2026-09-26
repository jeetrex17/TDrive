package photobackup

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func TestFolderCloseReleasesPausedTraversal(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"one.jpg", "two.jpg"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	adapter := NewLocalFolderAdapter(32, nil)
	page, err := adapter.Page(context.Background(), Source{Root: root}, "", 1)
	if err != nil || page.NextCursor == "" {
		t.Fatalf("page=%+v err=%v", page, err)
	}
	handle := adapter.sessions[page.NextCursor].stack[0].dir
	adapter.Close()
	if len(adapter.sessions) != 0 {
		t.Fatal("paused scan retained")
	}
	if _, err := handle.Stat(); err == nil {
		t.Fatal("directory handle retained after close")
	}
	adapter.Close()
}

func TestNativeSourceDisplayNameSurvivesRoundTrip(t *testing.T) {
	now := time.Now()
	engine, scope := testEngine(t, &now)
	source := Source{Scope: scope, ID: "phassetcollection:camera", Kind: "ios", Root: "phassetcollection:UUID", Name: "Camera", Enabled: true}
	if err := engine.UpsertSource(context.Background(), source); err != nil {
		t.Fatal(err)
	}
	sources, err := engine.ListSources(context.Background(), scope)
	if err != nil {
		t.Fatal(err)
	}
	if len(sources) != 1 || sources[0].Name != "Camera" || sources[0].Root != "phassetcollection:UUID" {
		t.Fatalf("sources=%+v", sources)
	}
}

func testEngine(t *testing.T, now *time.Time) (*Engine, Scope) {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "backup.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	e, err := Open(db, Options{PageSize: 2, MaxAttempts: 2, BaseBackoff: time.Second, Now: func() time.Time { return *now }})
	if err != nil {
		t.Fatal(err)
	}
	if err = e.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	return e, Scope{AccountID: "acct", DriveID: 7}
}
func configure(t *testing.T, e *Engine, s Scope) {
	t.Helper()
	ctx := context.Background()
	if err := e.PutSettings(ctx, Settings{Scope: s, Enabled: true, Photos: true, Videos: true, DestinationParentID: "d:root", Encrypt: true}); err != nil {
		t.Fatal(err)
	}
	if err := e.UpsertSource(ctx, Source{Scope: s, ID: "camera", Kind: "folder", Root: "/photos", Enabled: true}); err != nil {
		t.Fatal(err)
	}
}

func TestSettingsSourcesAndScopeIsolation(t *testing.T) {
	now := time.Unix(10, 0)
	e, a := testEngine(t, &now)
	b := Scope{AccountID: "other", DriveID: 7}
	configure(t, e, a)
	configure(t, e, b)
	got, err := e.GetSettings(context.Background(), a)
	if err != nil || !got.Encrypt || got.DestinationParentID != "d:root" {
		t.Fatalf("settings=%+v err=%v", got, err)
	}
	if err = e.RemoveSource(context.Background(), a, "camera"); err != nil {
		t.Fatal(err)
	}
	sources, err := e.ListSources(context.Background(), b)
	if err != nil || len(sources) != 1 {
		t.Fatalf("other scope sources=%v err=%v", sources, err)
	}
}

func TestManualPauseRejectsInvalidAndUnconfiguredScopes(t *testing.T) {
	now := time.Unix(100, 0)
	engine, scope := testEngine(t, &now)
	for _, invalid := range []Scope{{}, {AccountID: scope.AccountID}, {DriveID: scope.DriveID}} {
		if err := engine.SetManualPaused(context.Background(), invalid, true); !errors.Is(err, ErrInvalid) {
			t.Fatalf("invalid scope pause: %v", err)
		}
	}
	if err := engine.SetManualPaused(context.Background(), scope, true); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("unconfigured scope pause: %v", err)
	}
	if _, err := engine.GetSettings(context.Background(), scope); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("pause must not create backup settings: %v", err)
	}
}

func TestManualPauseIsDurableScopedAndBlocksUploads(t *testing.T) {
	now := time.Unix(15, 0)
	e, scope := testEngine(t, &now)
	otherDrive := Scope{AccountID: scope.AccountID, DriveID: scope.DriveID + 1}
	configure(t, e, scope)
	configure(t, e, otherDrive)
	if _, err := e.EnqueuePage(context.Background(), scope, "camera", []Asset{{ID: "1", Version: "v", Path: "/a.jpg", Name: "a.jpg", ModifiedAt: now}}); err != nil {
		t.Fatal(err)
	}
	if err := e.SetManualPaused(context.Background(), scope, true); err != nil {
		t.Fatal(err)
	}
	settings, err := e.GetSettings(context.Background(), scope)
	if err != nil || !settings.ManualPaused {
		t.Fatalf("settings=%+v err=%v", settings, err)
	}
	otherSettings, err := e.GetSettings(context.Background(), otherDrive)
	if err != nil || otherSettings.ManualPaused {
		t.Fatalf("other settings=%+v err=%v", otherSettings, err)
	}
	calls := 0
	if uploaded, err := e.RunOnce(context.Background(), scope, func(context.Context, UploadRequest) (UploadResult, error) {
		calls++
		return UploadResult{RemoteMessageID: 1}, nil
	}); err != nil || uploaded != 0 || calls != 0 {
		t.Fatalf("uploaded=%d calls=%d err=%v", uploaded, calls, err)
	}

	// A fresh Engine over the same database must observe the pause; it is not
	// process-local worker state.
	reopened, err := Open(e.db, Options{Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	settings, err = reopened.GetSettings(context.Background(), scope)
	if err != nil || !settings.ManualPaused {
		t.Fatalf("reopened settings=%+v err=%v", settings, err)
	}
	if err := reopened.SetManualPaused(context.Background(), scope, false); err != nil {
		t.Fatal(err)
	}
	if uploaded, err := reopened.RunOnce(context.Background(), scope, func(context.Context, UploadRequest) (UploadResult, error) {
		calls++
		return UploadResult{RemoteMessageID: 1}, nil
	}); err != nil || uploaded != 1 || calls != 1 {
		t.Fatalf("uploaded=%d calls=%d err=%v", uploaded, calls, err)
	}
}

func TestMigrateVersionOneAddsDurablePauseState(t *testing.T) {
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "legacy.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	// A v1 ledger already had sources and a queue; only the settings differed.
	if _, err = db.Exec(`CREATE TABLE photo_backup_sources(account_id TEXT NOT NULL,drive_id INTEGER NOT NULL,source_id TEXT NOT NULL,kind TEXT NOT NULL,root TEXT NOT NULL,name TEXT NOT NULL,enabled INTEGER NOT NULL,added_at INTEGER NOT NULL,PRIMARY KEY(account_id,drive_id,source_id));
INSERT INTO photo_backup_sources VALUES('acct',7,'camera','folder','/camera','camera',1,1);
CREATE TABLE photo_backup_settings(account_id TEXT NOT NULL,drive_id INTEGER NOT NULL,enabled INTEGER NOT NULL,photos INTEGER NOT NULL,videos INTEGER NOT NULL,future_only INTEGER NOT NULL,wifi_only INTEGER NOT NULL,charging_only INTEGER NOT NULL,destination_parent_id TEXT NOT NULL,encrypt INTEGER NOT NULL,PRIMARY KEY(account_id,drive_id));
INSERT INTO photo_backup_settings VALUES('acct',7,1,1,1,0,0,0,'',0);
CREATE TABLE photo_backup_jobs(account_id TEXT NOT NULL,drive_id INTEGER NOT NULL,source_id TEXT NOT NULL,asset_id TEXT NOT NULL,version TEXT NOT NULL,path TEXT NOT NULL,name TEXT NOT NULL,media_type TEXT NOT NULL,resource_id TEXT NOT NULL DEFAULT '',modified_at INTEGER NOT NULL,size INTEGER NOT NULL,status TEXT NOT NULL,attempts INTEGER NOT NULL DEFAULT 0,next_attempt_at INTEGER NOT NULL DEFAULT 0,last_error TEXT NOT NULL DEFAULT '',remote_message_id INTEGER NOT NULL DEFAULT 0,created_at INTEGER NOT NULL,updated_at INTEGER NOT NULL,PRIMARY KEY(account_id,drive_id,source_id,asset_id,version,resource_id));
INSERT INTO photo_backup_jobs VALUES('acct',7,'camera','a','v','/a.jpg','a.jpg','photo','',1,1,'pending',0,0,'',0,1,1);
PRAGMA user_version=1`); err != nil {
		t.Fatal(err)
	}
	engine, err := Open(db, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if err = engine.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	var version int
	if err = db.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil || version != schemaVersion {
		t.Fatalf("version=%d err=%v", version, err)
	}
	var chargingColumns, captureColumns, queued int
	if err = db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('photo_backup_settings') WHERE name='charging_only'`).Scan(&chargingColumns); err != nil || chargingColumns != 0 {
		t.Fatalf("charging columns=%d err=%v", chargingColumns, err)
	}
	if err = db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('photo_backup_jobs') WHERE name='captured_at'`).Scan(&captureColumns); err != nil || captureColumns != 1 {
		t.Fatalf("capture columns=%d err=%v", captureColumns, err)
	}
	if err = db.QueryRow(`SELECT COUNT(*) FROM photo_backup_jobs WHERE captured_at=0`).Scan(&queued); err != nil || queued != 1 {
		t.Fatalf("queued rows after migration=%d err=%v", queued, err)
	}
	settings, err := engine.GetSettings(context.Background(), Scope{AccountID: "acct", DriveID: 7})
	if err != nil || !settings.Enabled || !settings.Photos || !settings.Videos || settings.ManualPaused {
		t.Fatalf("settings=%+v err=%v", settings, err)
	}
	if err = engine.SetManualPaused(context.Background(), settings.Scope, true); err != nil {
		t.Fatal(err)
	}
	if err = engine.Migrate(context.Background()); err != nil {
		t.Fatalf("reopen migration: %v", err)
	}
	settings, err = engine.GetSettings(context.Background(), settings.Scope)
	if err != nil || !settings.ManualPaused {
		t.Fatalf("reopened settings=%+v err=%v", settings, err)
	}
}

func TestMigrateVersionTwoDropsChargingWithoutBlockingOrLosingQueue(t *testing.T) {
	now := time.Unix(18, 0)
	engine, scope := testEngine(t, &now)
	configure(t, engine, scope)
	if err := engine.PutSettings(context.Background(), Settings{Scope: scope, Enabled: true, Photos: true, Videos: true, WiFiOnly: true, DestinationParentID: "d:root", Encrypt: true}); err != nil {
		t.Fatal(err)
	}
	if err := engine.SetManualPaused(context.Background(), scope, true); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.EnqueuePage(context.Background(), scope, "camera", []Asset{{ID: "queued", Version: "v1", Path: "/queued.jpg", Name: "queued.jpg", MediaType: "photo", ModifiedAt: now, Size: 42}}); err != nil {
		t.Fatal(err)
	}
	// Rewind a current ledger to the exact shape a v2 release wrote.
	if _, err := engine.db.Exec(`ALTER TABLE photo_backup_settings ADD COLUMN charging_only INTEGER NOT NULL DEFAULT 0;
UPDATE photo_backup_settings SET charging_only=1;
ALTER TABLE photo_backup_settings DROP COLUMN receipt_cursor;
ALTER TABLE photo_backup_jobs DROP COLUMN captured_at;
ALTER TABLE photo_backup_jobs DROP COLUMN rel_dir;
PRAGMA user_version=2`); err != nil {
		t.Fatal(err)
	}

	if err := engine.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	settings, err := engine.GetSettings(context.Background(), scope)
	if err != nil || !settings.Enabled || !settings.Photos || !settings.Videos || !settings.WiFiOnly || !settings.Encrypt || !settings.ManualPaused || settings.DestinationParentID != "d:root" {
		t.Fatalf("settings=%+v err=%v", settings, err)
	}
	status, err := engine.Status(context.Background(), scope)
	if err != nil || status.Pending != 1 {
		t.Fatalf("status=%+v err=%v", status, err)
	}
	if err := engine.SetManualPaused(context.Background(), scope, false); err != nil {
		t.Fatal(err)
	}
	called := 0
	uploaded, err := engine.RunOnce(context.Background(), scope, func(_ context.Context, request UploadRequest) (UploadResult, error) {
		called++
		if request.Asset.ID != "queued" || request.Asset.Size != 42 {
			t.Fatalf("request=%+v", request)
		}
		return UploadResult{RemoteMessageID: 99}, nil
	})
	if err != nil || uploaded != 1 || called != 1 {
		t.Fatalf("uploaded=%d called=%d err=%v", uploaded, called, err)
	}
	var version, chargingColumns, captureColumns int
	if err := engine.db.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil || version != schemaVersion {
		t.Fatalf("version=%d err=%v", version, err)
	}
	if err := engine.db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('photo_backup_settings') WHERE name='charging_only'`).Scan(&chargingColumns); err != nil || chargingColumns != 0 {
		t.Fatalf("charging columns=%d err=%v", chargingColumns, err)
	}
	if err := engine.db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('photo_backup_jobs') WHERE name='captured_at'`).Scan(&captureColumns); err != nil || captureColumns != 1 {
		t.Fatalf("capture columns=%d err=%v", captureColumns, err)
	}
	if err := engine.Migrate(context.Background()); err != nil {
		t.Fatalf("repeat migration: %v", err)
	}
}

// v3 is what every current install is on, so this is the upgrade that will
// actually run in the field.
func TestMigrateVersionThreeAddsCaptureTimeWithoutLosingQueue(t *testing.T) {
	now := time.Unix(30, 0)
	engine, scope := testEngine(t, &now)
	configure(t, engine, scope)
	if _, err := engine.EnqueuePage(context.Background(), scope, "camera", []Asset{{ID: "queued", Version: "v1", Path: "/queued.jpg", Name: "queued.jpg", MediaType: "photo", ModifiedAt: now, Size: 42}}); err != nil {
		t.Fatal(err)
	}
	// Rewind a current ledger to the exact shape a v3 release wrote.
	if _, err := engine.db.Exec(`ALTER TABLE photo_backup_jobs DROP COLUMN captured_at;
ALTER TABLE photo_backup_settings DROP COLUMN receipt_cursor;
ALTER TABLE photo_backup_jobs DROP COLUMN rel_dir;
PRAGMA user_version=3`); err != nil {
		t.Fatal(err)
	}
	if err := engine.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	var version int
	if err := engine.db.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil || version != schemaVersion {
		t.Fatalf("version=%d err=%v", version, err)
	}
	uploaded, err := engine.RunOnce(context.Background(), scope, func(_ context.Context, r UploadRequest) (UploadResult, error) {
		// A row written before the column existed must read back as an
		// unknown capture time, never as 1970.
		if r.Asset.ID != "queued" || !r.Asset.CapturedAt.IsZero() {
			t.Fatalf("request=%+v", r)
		}
		return UploadResult{RemoteMessageID: 5}, nil
	})
	if err != nil || uploaded != 1 {
		t.Fatalf("uploaded=%d err=%v", uploaded, err)
	}
	if err := engine.Migrate(context.Background()); err != nil {
		t.Fatalf("repeat migration: %v", err)
	}
}

// v5 is the ledger a release before watched folders on a phone wrote. Its
// queue must survive, and a row from it must read back as belonging to the top
// of its source rather than to some invented folder.
func TestMigrateVersionFiveAddsRelativeDirWithoutLosingQueue(t *testing.T) {
	now := time.Unix(40, 0)
	engine, scope := testEngine(t, &now)
	configure(t, engine, scope)
	if _, err := engine.EnqueuePage(context.Background(), scope, "camera", []Asset{{ID: "queued", Version: "v1", ResourceID: "native:queued", Name: "queued.jpg", MediaType: "photo", ModifiedAt: now, Size: 42}}); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.db.Exec(`ALTER TABLE photo_backup_jobs DROP COLUMN rel_dir;
PRAGMA user_version=5`); err != nil {
		t.Fatal(err)
	}
	if err := engine.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	var version int
	if err := engine.db.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil || version != schemaVersion {
		t.Fatalf("version=%d err=%v", version, err)
	}
	uploaded, err := engine.RunOnce(context.Background(), scope, func(_ context.Context, r UploadRequest) (UploadResult, error) {
		if r.Asset.ID != "queued" || r.Asset.RelDir != "" {
			t.Fatalf("request=%+v", r)
		}
		return UploadResult{RemoteMessageID: 7}, nil
	})
	if err != nil || uploaded != 1 {
		t.Fatalf("uploaded=%d err=%v", uploaded, err)
	}
	if err := engine.Migrate(context.Background()); err != nil {
		t.Fatalf("repeat migration: %v", err)
	}
}

// A watched folder on a phone reports where each item sat, and that has to
// survive the queue: it is the only thing that keeps two photos with the same
// name in different subfolders apart when they land in the drive.
func TestEnqueueKeepsHostReportedRelativeDir(t *testing.T) {
	now := time.Unix(50, 0)
	engine, scope := testEngine(t, &now)
	configure(t, engine, scope)
	if _, err := engine.EnqueuePage(context.Background(), scope, "camera", []Asset{{ID: "nested", Version: "v1", ResourceID: "native:nested", Name: "IMG_1.jpg", MediaType: "photo", RelDir: "Trips/Rome", ModifiedAt: now, Size: 3}}); err != nil {
		t.Fatal(err)
	}
	seen := ""
	if _, err := engine.RunOnce(context.Background(), scope, func(_ context.Context, r UploadRequest) (UploadResult, error) {
		seen = r.Asset.RelDir
		return UploadResult{RemoteMessageID: 8}, nil
	}); err != nil {
		t.Fatal(err)
	}
	if seen != "Trips/Rome" {
		t.Fatalf("rel dir=%q", seen)
	}
}

func TestEnqueueDeduplicatesFiltersAndUploadsSerially(t *testing.T) {
	now := time.Unix(20, 0)
	e, scope := testEngine(t, &now)
	configure(t, e, scope)
	assets := []Asset{{ID: "1", Version: "a", Path: "/a.jpg", Name: "a.jpg", ModifiedAt: now, Size: 1}, {ID: "1", Version: "a", Path: "/a.jpg", Name: "a.jpg", ModifiedAt: now, Size: 1}, {ID: "2", Version: "a", Path: "/x.txt", Name: "x.txt", ModifiedAt: now}, {ID: "3", Version: "a", ResourceID: "native:3", Name: "c.mov", ModifiedAt: now}}
	n, err := e.EnqueuePage(context.Background(), scope, "camera", assets)
	if err != nil || n != 2 {
		t.Fatalf("added=%d err=%v", n, err)
	}
	calls := 0
	uploaded, err := e.RunOnce(context.Background(), scope, func(_ context.Context, r UploadRequest) (UploadResult, error) {
		calls++
		if r.Asset.ID == "" || r.Source.ID != "camera" || r.ParentID != "d:root" || !r.Encrypt {
			t.Fatalf("request=%+v", r)
		}
		return UploadResult{RemoteMessageID: int64(calls)}, nil
	})
	if err != nil || uploaded != 1 || calls != 1 {
		t.Fatalf("uploaded=%d calls=%d err=%v", uploaded, calls, err)
	}
	st, _ := e.Status(context.Background(), scope)
	if st.Complete != 1 || st.Pending != 1 {
		t.Fatalf("status=%+v", st)
	}
}

func TestRetryBackoffAndInterruptedNeedsExplicitRetry(t *testing.T) {
	now := time.Unix(30, 0)
	e, scope := testEngine(t, &now)
	configure(t, e, scope)
	_, _ = e.EnqueuePage(context.Background(), scope, "camera", []Asset{{ID: "1", Version: "v", Path: "/a.jpg", Name: "a.jpg", ModifiedAt: now}})
	fail := func(context.Context, UploadRequest) (UploadResult, error) {
		return UploadResult{}, errors.New("offline")
	}
	if n, err := e.RunOnce(context.Background(), scope, fail); err != nil || n != 0 {
		t.Fatal(n, err)
	}
	st, _ := e.Status(context.Background(), scope)
	if st.Error != 1 || st.LastError != "offline" {
		t.Fatalf("status=%+v", st)
	}
	now = now.Add(time.Second)
	_, _ = e.RunOnce(context.Background(), scope, fail)
	st, _ = e.Status(context.Background(), scope)
	if st.Paused != 1 {
		t.Fatalf("status=%+v", st)
	}
	if err := e.RetryErrors(context.Background(), scope); err != nil {
		t.Fatal(err)
	}
	st, _ = e.Status(context.Background(), scope)
	if st.Pending != 1 {
		t.Fatalf("status=%+v", st)
	}
	_, err := e.db.Exec(`UPDATE photo_backup_jobs SET status='uploading'`)
	if err != nil {
		t.Fatal(err)
	}
	if err = e.RecoverInterrupted(context.Background(), scope); err != nil {
		t.Fatal(err)
	}
	st, _ = e.Status(context.Background(), scope)
	if st.Paused != 1 || st.LastError == "" {
		t.Fatalf("status=%+v", st)
	}
}

// Backup takes everything in a watched folder. What a host reports about when
// a photo was taken still has to survive the ledger, because the drive files
// it by that date; it just no longer decides whether it is backed up at all.
func TestNativePageCarriesCaptureTimeAndIsBounded(t *testing.T) {
	now := time.Unix(100, 0)
	e, scope := testEngine(t, &now)
	if err := e.PutSettings(context.Background(), Settings{Scope: scope, Enabled: true, Photos: true}); err != nil {
		t.Fatal(err)
	}
	if err := e.UpsertSource(context.Background(), Source{Scope: scope, ID: "native", Kind: "device-folder", Root: "external_primary:DCIM/", Enabled: true, AddedAt: now}); err != nil {
		t.Fatal(err)
	}
	// Both, including the one taken long before the folder was added.
	n, err := e.EnqueuePage(context.Background(), scope, "native", []Asset{
		{ID: "old", Version: "1", ResourceID: "r", Name: "o.jpg", CapturedAt: now.Add(-time.Hour), ModifiedAt: now.Add(-time.Second)},
		{ID: "shot", Version: "1", ResourceID: "r2", Name: "s.jpg", CapturedAt: now.Add(time.Second), ModifiedAt: now.Add(time.Second)},
	})
	if err != nil || n != 2 {
		t.Fatalf("every item in a watched folder is backed up: added=%d err=%v", n, err)
	}
	seen := map[string]time.Time{}
	for i := range 2 {
		if _, err := e.RunOnce(context.Background(), scope, func(_ context.Context, r UploadRequest) (UploadResult, error) {
			seen[r.Asset.ID] = r.Asset.CapturedAt
			return UploadResult{RemoteMessageID: int64(i + 1)}, nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	if !seen["shot"].Equal(now.Add(time.Second)) || !seen["old"].Equal(now.Add(-time.Hour)) {
		t.Fatalf("capture time must round-trip through the ledger: %v", seen)
	}
	if _, err = e.EnqueuePage(context.Background(), scope, "native", make([]Asset, 129)); !errors.Is(err, ErrInvalid) {
		t.Fatalf("err=%v", err)
	}
}

func TestLocalFolderAdapterPagingDepthSymlinksAndExclusion(t *testing.T) {
	root := t.TempDir()
	mustWrite := func(path string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("x"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	mustWrite(filepath.Join(root, "a.jpg"))
	mustWrite(filepath.Join(root, "b.mov"))
	mustWrite(filepath.Join(root, "skip", "c.jpg"))
	mustWrite(filepath.Join(root, "deep", "one", "d.jpg"))
	if err := os.Symlink(root, filepath.Join(root, "loop")); err != nil {
		t.Fatal(err)
	}
	a := LocalFolderAdapter{MaxDepth: 2, ExcludedRoots: []string{filepath.Join(root, "skip")}}
	source := Source{Root: root}
	p1, err := a.Page(context.Background(), source, "", 1)
	if err != nil || len(p1.Assets) != 1 || p1.NextCursor == "" {
		t.Fatalf("p1=%+v err=%v", p1, err)
	}
	p2, err := a.Page(context.Background(), source, p1.NextCursor, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(p2.Assets) != 1 || p2.Assets[0].Name == p1.Assets[0].Name {
		t.Fatalf("p1=%+v p2=%+v", p1, p2)
	}
}

func TestInvalidArguments(t *testing.T) {
	now := time.Now()
	e, scope := testEngine(t, &now)
	if _, err := Open(nil, Options{}); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	if err := e.PutSettings(context.Background(), Settings{}); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	if _, err := e.Discover(context.Background(), scope, "x", nil); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
}

type pageAdapter struct {
	pages map[string]Page
	err   error
}

func (a pageAdapter) Page(context.Context, Source, string, int) (Page, error) {
	if a.err != nil {
		return Page{}, a.err
	}
	return a.pages[""], nil
}

type cursorAdapter struct{ calls int }

func (a *cursorAdapter) Page(_ context.Context, _ Source, cursor string, _ int) (Page, error) {
	a.calls++
	if cursor == "" {
		return Page{Assets: []Asset{{ID: "1", Version: "v", Path: "/one.jpg", Name: "one.jpg", ModifiedAt: time.Now()}}, NextCursor: "next"}, nil
	}
	return Page{Assets: []Asset{{ID: "2", Version: "v", Path: "/two.mov", Name: "two.mov", ModifiedAt: time.Now()}}}, nil
}

func TestDiscoverPagesAndAdapterFailures(t *testing.T) {
	now := time.Unix(200, 0)
	e, scope := testEngine(t, &now)
	configure(t, e, scope)
	a := &cursorAdapter{}
	n, err := e.Discover(context.Background(), scope, "camera", a)
	if err != nil || n != 2 || a.calls != 2 {
		t.Fatalf("added=%d calls=%d err=%v", n, a.calls, err)
	}
	boom := errors.New("scan failed")
	_, err = e.Discover(context.Background(), scope, "camera", pageAdapter{err: boom})
	if !errors.Is(err, boom) {
		t.Fatalf("err=%v", err)
	}
	stuck := pageAdapter{pages: map[string]Page{"": {NextCursor: ""}}}
	if _, err = e.Discover(context.Background(), scope, "missing", stuck); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("err=%v", err)
	}
}

func TestRunOnceRejectsEmptyReceiptAndHonorsDisabled(t *testing.T) {
	now := time.Unix(300, 0)
	e, scope := testEngine(t, &now)
	configure(t, e, scope)
	_, _ = e.EnqueuePage(context.Background(), scope, "camera", []Asset{{ID: "1", Version: "v", Path: "/a.jpg", Name: "a.jpg", ModifiedAt: now}})
	_, err := e.RunOnce(context.Background(), scope, func(context.Context, UploadRequest) (UploadResult, error) { return UploadResult{}, nil })
	if err != nil {
		t.Fatal(err)
	}
	st, _ := e.Status(context.Background(), scope)
	if st.Error != 1 || st.LastError == "" {
		t.Fatalf("status=%+v", st)
	}
	settings, _ := e.GetSettings(context.Background(), scope)
	settings.Enabled = false
	if err = e.PutSettings(context.Background(), settings); err != nil {
		t.Fatal(err)
	}
	called := false
	n, err := e.RunOnce(context.Background(), scope, func(context.Context, UploadRequest) (UploadResult, error) {
		called = true
		return UploadResult{RemoteMessageID: 1}, nil
	})
	if err != nil || n != 0 || called {
		t.Fatalf("n=%d called=%v err=%v", n, called, err)
	}
}

func TestLatestVersionSupersedesQueuedAndDisabledSourceDoesNotStarve(t *testing.T) {
	now := time.Unix(400, 0)
	e, scope := testEngine(t, &now)
	configure(t, e, scope)
	if err := e.UpsertSource(context.Background(), Source{Scope: scope, ID: "other", Kind: "folder", Root: "/other", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	_, _ = e.EnqueuePage(context.Background(), scope, "camera", []Asset{{ID: "same", Version: "1", Path: "/old.jpg", Name: "old.jpg", ModifiedAt: now}})
	_, _ = e.EnqueuePage(context.Background(), scope, "camera", []Asset{{ID: "same", Version: "2", Path: "/new.jpg", Name: "new.jpg", ModifiedAt: now}})
	_, _ = e.EnqueuePage(context.Background(), scope, "other", []Asset{{ID: "ready", Version: "1", Path: "/ready.avif", Name: "ready.avif", ModifiedAt: now}})
	sources, _ := e.ListSources(context.Background(), scope)
	for _, s := range sources {
		if s.ID == "camera" {
			s.Enabled = false
			if err := e.UpsertSource(context.Background(), s); err != nil {
				t.Fatal(err)
			}
		}
	}
	var got string
	n, err := e.RunOnce(context.Background(), scope, func(_ context.Context, r UploadRequest) (UploadResult, error) {
		got = r.Asset.ID
		return UploadResult{RemoteMessageID: 9}, nil
	})
	if err != nil || n != 1 || got != "ready" {
		t.Fatalf("n=%d got=%q err=%v", n, got, err)
	}
	var queued int
	if err = e.db.QueryRow(`SELECT COUNT(*) FROM photo_backup_jobs WHERE source_id='camera'`).Scan(&queued); err != nil || queued != 1 {
		t.Fatalf("queued=%d err=%v", queued, err)
	}
}

func TestStatusWithSingleConnectionAndCanceledUploadReceipt(t *testing.T) {
	now := time.Unix(500, 0)
	e, scope := testEngine(t, &now)
	configure(t, e, scope)
	e.db.SetMaxOpenConns(1)
	_, _ = e.EnqueuePage(context.Background(), scope, "camera", []Asset{{ID: "x", Version: "1", Path: "/x.jpg", Name: "x.jpg", ModifiedAt: now}})
	ctx, cancel := context.WithCancel(context.Background())
	n, err := e.RunOnce(ctx, scope, func(context.Context, UploadRequest) (UploadResult, error) {
		cancel()
		return UploadResult{RemoteMessageID: 22}, nil
	})
	if err != nil || n != 1 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	st, err := e.Status(context.Background(), scope)
	if err != nil || st.Complete != 1 {
		t.Fatalf("status=%+v err=%v", st, err)
	}
}

func TestLocalFolderAdapterCursorLifecycle(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.jpg"), []byte("a"), 0644); err != nil {
		t.Fatal(err)
	}
	a := NewLocalFolderAdapter(4, nil)
	if _, err := a.Page(context.Background(), Source{Root: root}, "expired", 1); err == nil {
		t.Fatal("expected expired cursor error")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := a.Page(ctx, Source{Root: root}, "", 1); !errors.Is(err, context.Canceled) {
		t.Fatalf("err=%v", err)
	}
	excluded := NewLocalFolderAdapter(4, []string{root})
	if _, err := excluded.Page(context.Background(), Source{Root: root}, "", 1); err == nil {
		t.Fatal("expected excluded root error")
	}
}

func TestNativeAssetDeduplicatesAcrossAlbums(t *testing.T) {
	now := time.Unix(600, 0)
	e, scope := testEngine(t, &now)
	if err := e.PutSettings(context.Background(), Settings{Scope: scope, Enabled: true, Photos: true, Videos: true}); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"all", "favorites"} {
		if err := e.UpsertSource(context.Background(), Source{Scope: scope, ID: id, Kind: "ios", Root: id, Enabled: true}); err != nil {
			t.Fatal(err)
		}
	}
	asset := Asset{ID: "native-id", Version: "v1", ResourceID: "resource", Name: "photo.jpg", ModifiedAt: now}
	n1, err := e.EnqueuePage(context.Background(), scope, "all", []Asset{asset})
	if err != nil {
		t.Fatal(err)
	}
	n2, err := e.EnqueuePage(context.Background(), scope, "favorites", []Asset{asset})
	if err != nil || n1 != 1 || n2 != 0 {
		t.Fatalf("added=%d/%d err=%v", n1, n2, err)
	}
	if err = e.RemoveSource(context.Background(), scope, "all"); err != nil {
		t.Fatal(err)
	}
	n3, err := e.EnqueuePage(context.Background(), scope, "favorites", []Asset{asset})
	if err != nil || n3 != 1 {
		t.Fatalf("reowned=%d err=%v", n3, err)
	}
	var owner string
	if err = e.db.QueryRow(`SELECT source_id FROM photo_backup_jobs WHERE asset_id='native-id' AND resource_id='resource'`).Scan(&owner); err != nil || owner != "favorites" {
		t.Fatalf("owner=%q err=%v", owner, err)
	}
	paired := asset
	paired.ResourceID = "paired-video"
	paired.Name = "photo.mov"
	if n4, err := e.EnqueuePage(context.Background(), scope, "favorites", []Asset{paired}); err != nil || n4 != 1 {
		t.Fatalf("paired=%d err=%v", n4, err)
	}
	if _, err = e.db.Exec(`UPDATE photo_backup_jobs SET status='complete' WHERE resource_id='resource'`); err != nil {
		t.Fatal(err)
	}
	if err = e.RemoveSource(context.Background(), scope, "favorites"); err != nil {
		t.Fatal(err)
	}
	if err = e.UpsertSource(context.Background(), Source{Scope: scope, ID: "album2", Kind: "ios", Root: "album2", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	if n5, err := e.EnqueuePage(context.Background(), scope, "album2", []Asset{asset}); err != nil || n5 != 0 {
		t.Fatalf("completed duplicate=%d err=%v", n5, err)
	}
}

func TestDiscoverPageCheckpointAndReset(t *testing.T) {
	now := time.Unix(700, 0)
	e, scope := testEngine(t, &now)
	configure(t, e, scope)
	a := &cursorAdapter{}
	n, done, err := e.DiscoverPage(context.Background(), scope, "camera", a)
	if err != nil || n != 1 || done {
		t.Fatalf("first=%d done=%v err=%v", n, done, err)
	}
	n, done, err = e.DiscoverPage(context.Background(), scope, "camera", a)
	if err != nil || n != 1 || !done {
		t.Fatalf("second=%d done=%v err=%v", n, done, err)
	}
	if err = e.ResetDiscovery(context.Background(), scope, "camera"); err != nil {
		t.Fatal(err)
	}
	_, done, err = e.DiscoverPage(context.Background(), scope, "camera", a)
	if err != nil || done {
		t.Fatalf("reset done=%v err=%v", done, err)
	}
}

func TestStatusUsesMaintainedCountersAtScale(t *testing.T) {
	now := time.Unix(800, 0)
	e, scope := testEngine(t, &now)
	_, err := e.db.Exec(`WITH RECURSIVE n(x) AS (VALUES(1) UNION ALL SELECT x+1 FROM n WHERE x<100000)
INSERT INTO photo_backup_jobs(account_id,drive_id,source_id,asset_id,version,path,name,media_type,modified_at,size,status,created_at,updated_at)
SELECT 'acct',7,'bulk',printf('asset-%d',x),'1',printf('/%d.jpg',x),printf('%d.jpg',x),'',0,1,'pending',0,0 FROM n`)
	if err != nil {
		t.Fatal(err)
	}
	st, err := e.Status(context.Background(), scope)
	if err != nil || st.Pending != 100000 {
		t.Fatalf("status=%+v err=%v", st, err)
	}
	if _, err = e.db.Exec(`UPDATE photo_backup_jobs SET status='complete' WHERE asset_id='asset-1'; DELETE FROM photo_backup_jobs WHERE asset_id='asset-2'`); err != nil {
		t.Fatal(err)
	}
	st, err = e.Status(context.Background(), scope)
	if err != nil || st.Pending != 99998 || st.Complete != 1 {
		t.Fatalf("after changes=%+v err=%v", st, err)
	}
	rows, err := e.db.Query(`EXPLAIN QUERY PLAN SELECT status,count FROM photo_backup_counts WHERE account_id=? AND drive_id=?`, scope.AccountID, scope.DriveID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	plan := ""
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		plan += detail
	}
	if strings.Contains(plan, "photo_backup_jobs") || !strings.Contains(plan, "photo_backup_counts") {
		t.Fatalf("unexpected plan: %s", plan)
	}
}

func TestRunOnceHonorsCurrentMediaSelectionWithoutStarvation(t *testing.T) {
	now := time.Unix(900, 0)
	e, scope := testEngine(t, &now)
	configure(t, e, scope)
	_, err := e.EnqueuePage(context.Background(), scope, "camera", []Asset{{ID: "video", Version: "1", Path: "/first.mov", Name: "first.mov", ModifiedAt: now}, {ID: "photo", Version: "1", Path: "/second.jpg", Name: "second.jpg", ModifiedAt: now}})
	if err != nil {
		t.Fatal(err)
	}
	settings, err := e.GetSettings(context.Background(), scope)
	if err != nil {
		t.Fatal(err)
	}
	settings.Videos = false
	if err = e.PutSettings(context.Background(), settings); err != nil {
		t.Fatal(err)
	}
	var uploaded []string
	n, err := e.RunOnce(context.Background(), scope, func(_ context.Context, r UploadRequest) (UploadResult, error) {
		uploaded = append(uploaded, r.Asset.ID)
		return UploadResult{RemoteMessageID: 1}, nil
	})
	if err != nil || n != 1 || len(uploaded) != 1 || uploaded[0] != "photo" {
		t.Fatalf("n=%d uploaded=%v err=%v", n, uploaded, err)
	}
	if n, err = e.RunOnce(context.Background(), scope, func(_ context.Context, r UploadRequest) (UploadResult, error) {
		uploaded = append(uploaded, r.Asset.ID)
		return UploadResult{RemoteMessageID: 2}, nil
	}); err != nil || n != 0 {
		t.Fatalf("disabled video ran: n=%d err=%v", n, err)
	}
	settings.Videos = true
	if err = e.PutSettings(context.Background(), settings); err != nil {
		t.Fatal(err)
	}
	n, err = e.RunOnce(context.Background(), scope, func(_ context.Context, r UploadRequest) (UploadResult, error) {
		uploaded = append(uploaded, r.Asset.ID)
		return UploadResult{RemoteMessageID: 2}, nil
	})
	if err != nil || n != 1 || uploaded[len(uploaded)-1] != "video" {
		t.Fatalf("enabled video: n=%d uploaded=%v err=%v", n, uploaded, err)
	}
}

func TestRemoveSourceDropsUncommittedCountsButKeepsCompletedReceipt(t *testing.T) {
	now := time.Unix(1000, 0)
	e, scope := testEngine(t, &now)
	configure(t, e, scope)
	_, err := e.EnqueuePage(context.Background(), scope, "camera", []Asset{{ID: "pending", Version: "1", Path: "/p.jpg", Name: "p.jpg", ModifiedAt: now}, {ID: "done", Version: "1", Path: "/d.jpg", Name: "d.jpg", ModifiedAt: now}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = e.db.Exec(`UPDATE photo_backup_jobs SET status='complete',remote_message_id=4 WHERE asset_id='done'`); err != nil {
		t.Fatal(err)
	}
	if err = e.RemoveSource(context.Background(), scope, "camera"); err != nil {
		t.Fatal(err)
	}
	st, err := e.Status(context.Background(), scope)
	if err != nil || st.Pending != 0 || st.Complete != 1 {
		t.Fatalf("status=%+v err=%v", st, err)
	}
	var pending, complete int
	if err = e.db.QueryRow(`SELECT COUNT(*) FILTER(WHERE status='pending'),COUNT(*) FILTER(WHERE status='complete') FROM photo_backup_jobs`).Scan(&pending, &complete); err != nil || pending != 0 || complete != 1 {
		t.Fatalf("ledger pending=%d complete=%d err=%v", pending, complete, err)
	}
}

func TestRemoveSourceRefusesActiveUpload(t *testing.T) {
	now := time.Unix(1100, 0)
	e, scope := testEngine(t, &now)
	configure(t, e, scope)
	_, _ = e.EnqueuePage(context.Background(), scope, "camera", []Asset{{ID: "active", Version: "1", Path: "/a.jpg", Name: "a.jpg", ModifiedAt: now}})
	_, _ = e.db.Exec(`UPDATE photo_backup_jobs SET status='uploading'`)
	if err := e.RemoveSource(context.Background(), scope, "camera"); err == nil {
		t.Fatal("expected active upload conflict")
	}
	sources, err := e.ListSources(context.Background(), scope)
	if err != nil || len(sources) != 1 {
		t.Fatalf("sources=%v err=%v", sources, err)
	}
}

// A source the user removed -- or one an older build wrote under a different
// id -- leaves its unfinished jobs behind. Nothing will ever scan or upload
// them, so they must not go on counting as work the backup is waiting to do.
func TestMigratePrunesJobsWhoseSourceIsGone(t *testing.T) {
	now := time.Now()
	engine, scope := testEngine(t, &now)
	ctx := context.Background()
	if err := engine.UpsertSource(ctx, Source{Scope: scope, ID: "folder:/Pictures", Kind: "folder", Root: "/Pictures", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	insert := func(sourceID, assetID, status string) {
		t.Helper()
		if _, err := engine.db.ExecContext(ctx, `INSERT INTO photo_backup_jobs(account_id,drive_id,source_id,asset_id,version,path,name,media_type,resource_id,modified_at,size,status,created_at,updated_at) VALUES(?,?,?,?,'1',?,?,'photo','',0,1,?,1,1)`,
			scope.AccountID, scope.DriveID, sourceID, assetID, "/"+assetID, assetID, status); err != nil {
			t.Fatal(err)
		}
	}
	insert("folder:/Pictures", "kept.jpg", string(Pending))
	insert("folder:/Pictures/images", "orphan.jpg", string(Pending))
	insert("folder:/Pictures/images", "orphan-failed.jpg", string(Error))
	// The receipt of a file that did reach the drive: the record of it stays,
	// so the next scan of a folder that still covers it sends nothing.
	insert("folder:/Pictures/images", "done.jpg", string(Complete))

	if err := engine.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	status, err := engine.Status(ctx, scope)
	if err != nil {
		t.Fatal(err)
	}
	if status.Pending != 1 || status.Error != 0 || status.Complete != 1 {
		t.Fatalf("status=%+v", status)
	}
	var names []string
	rows, err := engine.db.QueryContext(ctx, `SELECT asset_id FROM photo_backup_jobs ORDER BY asset_id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		names = append(names, name)
	}
	if strings.Join(names, ",") != "done.jpg,kept.jpg" {
		t.Fatalf("jobs=%v", names)
	}
}

// The album picker is gone, and so are the sources it made: a ledger row for
// an album names a place nothing can scan any more. What it already put in the
// drive stays recorded, so adding the folder that holds those photos does not
// send every one of them a second time.
func TestMigrateDropsSourcesTheAlbumPickerMade(t *testing.T) {
	now := time.Now()
	engine, scope := testEngine(t, &now)
	ctx := context.Background()
	for _, source := range []Source{
		{Scope: scope, ID: "folder:/Pictures", Kind: "folder", Root: "/Pictures", Enabled: true},
		{Scope: scope, ID: "tree:external_primary:DCIM/", Kind: "device-folder", Root: "external_primary:DCIM/", Enabled: true},
		{Scope: scope, ID: "all", Kind: "library", Root: "content://media", Enabled: true},
		{Scope: scope, ID: "bucket:9", Kind: "album", Root: "content://media", Enabled: true},
	} {
		if err := engine.UpsertSource(ctx, source); err != nil {
			t.Fatal(err)
		}
	}
	insert := func(sourceID, assetID, status string) {
		t.Helper()
		if _, err := engine.db.ExecContext(ctx, `INSERT INTO photo_backup_jobs(account_id,drive_id,source_id,asset_id,version,path,name,media_type,resource_id,modified_at,size,status,created_at,updated_at) VALUES(?,?,?,?,'1','',?,'photo','',0,1,?,1,1)`,
			scope.AccountID, scope.DriveID, sourceID, assetID, assetID, status); err != nil {
			t.Fatal(err)
		}
	}
	insert("bucket:9", "waiting.jpg", string(Pending))
	insert("bucket:9", "sent.jpg", string(Complete))

	if err := engine.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	sources, err := engine.ListSources(ctx, scope)
	if err != nil {
		t.Fatal(err)
	}
	var kinds []string
	for _, source := range sources {
		kinds = append(kinds, source.Kind)
	}
	// Ordered by source id: "folder:/Pictures" then "tree:external_primary:…".
	if strings.Join(kinds, ",") != "folder,device-folder" {
		t.Fatalf("sources = %v, want only the two folder kinds", kinds)
	}
	status, err := engine.Status(ctx, scope)
	if err != nil {
		t.Fatal(err)
	}
	if status.Pending != 0 || status.Complete != 1 {
		t.Fatalf("status = %+v, want the queue gone and the receipt kept", status)
	}
}
