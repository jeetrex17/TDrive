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

func TestFutureOnlyAndNativePageBound(t *testing.T) {
	now := time.Unix(100, 0)
	e, scope := testEngine(t, &now)
	if err := e.PutSettings(context.Background(), Settings{Scope: scope, Enabled: true, Photos: true, FutureOnly: true}); err != nil {
		t.Fatal(err)
	}
	if err := e.UpsertSource(context.Background(), Source{Scope: scope, ID: "native", Kind: "ios", Root: "library", Enabled: true, AddedAt: now}); err != nil {
		t.Fatal(err)
	}
	n, err := e.EnqueuePage(context.Background(), scope, "native", []Asset{{ID: "old", Version: "1", ResourceID: "r", Name: "o.jpg", ModifiedAt: now.Add(-time.Second)}, {ID: "new", Version: "1", ResourceID: "r2", Name: "n.jpg", ModifiedAt: now.Add(time.Second)}})
	if err != nil || n != 1 {
		t.Fatal(n, err)
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
