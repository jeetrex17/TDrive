package photobackup

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"TDrive/backend/tgclient"
)

func TestUnknownSendHeldUntilExplicitRetry(t *testing.T) {
	now := time.Unix(100, 0)
	e, scope := testEngine(t, &now)
	configure(t, e, scope)
	if n, err := e.EnqueuePage(t.Context(), scope, "camera", []Asset{{ID: "a", Version: "v", Path: "/a.jpg", Name: "a.jpg", MediaType: "photo", ModifiedAt: now}}); err != nil || n != 1 {
		t.Fatal(err)
	}
	calls := 0
	upload := func(context.Context, UploadRequest) (UploadResult, error) {
		calls++
		return UploadResult{}, fmt.Errorf("lost receipt: %w", tgclient.ErrSendOutcomeUnknown)
	}
	if _, err := e.RunOnce(t.Context(), scope, upload); err != nil {
		t.Fatal(err)
	}
	status, err := e.Status(t.Context(), scope)
	if err != nil || status.Paused != 1 || status.Error != 0 {
		t.Fatalf("unknown outcome must be held: status=%+v err=%v", status, err)
	}
	// Close and reopen the file-backed ledger with a fresh owner. Merely
	// constructing another Engine around the same connection is not restart
	// durability evidence.
	var sequence int
	var name, path string
	if err := e.db.QueryRowContext(t.Context(), `PRAGMA database_list`).Scan(&sequence, &name, &path); err != nil {
		t.Fatal(err)
	}
	if err := e.db.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := reopened.Close(); err != nil {
			t.Error(err)
		}
	})
	e, err = Open(reopened, Options{Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.Migrate(t.Context()); err != nil {
		t.Fatal(err)
	}
	now = now.Add(24 * time.Hour)
	if _, err := e.RunOnce(t.Context(), scope, upload); err != nil || calls != 1 {
		t.Fatalf("unknown outcome automatically retried: calls=%d err=%v", calls, err)
	}
	if err := e.RetryErrors(t.Context(), scope); err != nil {
		t.Fatal(err)
	}
	if _, err := e.RunOnce(t.Context(), scope, upload); err != nil || calls != 2 {
		t.Fatalf("explicit retry: calls=%d err=%v", calls, err)
	}
}

func TestRunNextCompetingWorkersRespectNewBackoff(t *testing.T) {
	now := time.Unix(100, 0)
	e, scope := testEngine(t, &now)
	e.db.SetMaxOpenConns(1)
	configure(t, e, scope)
	if n, err := e.EnqueuePage(t.Context(), scope, "camera", []Asset{{ID: "a", Version: "v", Path: "/a.jpg", Name: "a.jpg", ModifiedAt: now}}); err != nil || n != 1 {
		t.Fatalf("enqueue=%d err=%v", n, err)
	}
	const workerCount = 16
	var workers sync.WaitGroup
	results := make(chan RunResult, workerCount)
	errorsCh := make(chan error, workerCount)
	start := make(chan struct{})
	for range workerCount {
		workers.Go(func() {
			<-start
			result, err := e.RunNext(t.Context(), scope, func(context.Context, UploadRequest) (UploadResult, error) {
				return UploadResult{}, errors.New("offline")
			})
			results <- result
			errorsCh <- err
		})
	}
	close(start)
	workers.Wait()
	claimed := 0
	for range workerCount {
		if err := <-errorsCh; err != nil {
			t.Fatal(err)
		}
		if result := <-results; result.Claimed {
			claimed++
		}
	}
	if claimed != 1 {
		t.Fatalf("new backoff was ignored: %d claims", claimed)
	}
	var attempts int
	var next int64
	if err := e.db.QueryRowContext(t.Context(), `SELECT attempts,next_attempt_at FROM photo_backup_jobs`).Scan(&attempts, &next); err != nil {
		t.Fatal(err)
	}
	if attempts != 1 || next != now.Add(time.Second).UnixNano() {
		t.Fatalf("attempts=%d next=%d", attempts, next)
	}
}

func TestRunNextReceiptOverridesAllInterruptionReasons(t *testing.T) {
	for _, uploadErr := range []error{tgclient.ErrSendOutcomeUnknown, ErrUploadNotStarted, context.Canceled} {
		t.Run(uploadErr.Error(), func(t *testing.T) {
			now := time.Unix(100, 0)
			e, scope := testEngine(t, &now)
			configure(t, e, scope)
			if n, err := e.EnqueuePage(t.Context(), scope, "camera", []Asset{{ID: "a", Version: "v", Path: "/a.jpg", Name: "a.jpg", ModifiedAt: now}}); err != nil || n != 1 {
				t.Fatalf("enqueue=%d err=%v", n, err)
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			result, err := e.RunNext(ctx, scope, func(context.Context, UploadRequest) (UploadResult, error) {
				cancel()
				return UploadResult{RemoteMessageID: 91}, uploadErr
			})
			if err != nil || !result.Completed || !result.Claimed || result.Deferred {
				t.Fatalf("result=%+v err=%v", result, err)
			}
			var status string
			var receipt int64
			if err := e.db.QueryRowContext(t.Context(), `SELECT status,remote_message_id FROM photo_backup_jobs`).Scan(&status, &receipt); err != nil {
				t.Fatal(err)
			}
			if status != string(Complete) || receipt != 91 {
				t.Fatalf("status=%s receipt=%d", status, receipt)
			}
		})
	}
}

func TestRunNextDistinguishesFailureFromEmptyQueue(t *testing.T) {
	now := time.Unix(100, 0)
	e, scope := testEngine(t, &now)
	configure(t, e, scope)
	if n, err := e.EnqueuePage(t.Context(), scope, "camera", []Asset{{ID: "a", Version: "v", Path: "/a.jpg", Name: "a.jpg", MediaType: "photo", ModifiedAt: now}}); err != nil || n != 1 {
		t.Fatal(err)
	}
	upload := func(context.Context, UploadRequest) (UploadResult, error) {
		return UploadResult{}, errors.New("offline")
	}
	result, err := e.RunNext(t.Context(), scope, upload)
	if err != nil || !result.Claimed || result.Completed {
		t.Fatalf("failed work must count as claimed: result=%+v err=%v", result, err)
	}
	result, err = e.RunNext(t.Context(), scope, upload)
	if err != nil || result.Claimed || result.Completed {
		t.Fatalf("backoff job must not be reclaimed: result=%+v err=%v", result, err)
	}
}

func TestRunNextBackoffStartsWhenUploadFails(t *testing.T) {
	now := time.Unix(100, 0)
	e, scope := testEngine(t, &now)
	configure(t, e, scope)
	if n, err := e.EnqueuePage(t.Context(), scope, "camera", []Asset{{ID: "a", Version: "v", Path: "/a.jpg", Name: "a.jpg", ModifiedAt: now}}); err != nil || n != 1 {
		t.Fatalf("enqueue=%d err=%v", n, err)
	}
	result, err := e.RunNext(t.Context(), scope, func(context.Context, UploadRequest) (UploadResult, error) {
		// The attempt itself can take longer than the backoff. Counting from
		// claim time would let another worker immediately retry the failure.
		now = now.Add(time.Minute)
		return UploadResult{}, errors.New("offline")
	})
	if err != nil || !result.Claimed {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	var next int64
	if err := e.db.QueryRowContext(t.Context(), `SELECT next_attempt_at FROM photo_backup_jobs`).Scan(&next); err != nil {
		t.Fatal(err)
	}
	if want := now.Add(time.Second).UnixNano(); next != want {
		t.Fatalf("next=%d want=%d", next, want)
	}
}

func TestMigrateVersionSevenAddsFIFOClaimIndex(t *testing.T) {
	now := time.Unix(100, 0)
	e, scope := testEngine(t, &now)
	configure(t, e, scope)
	if n, err := e.EnqueuePage(t.Context(), scope, "camera", []Asset{{ID: "a", Version: "v", Path: "/a.jpg", Name: "a.jpg", ModifiedAt: now}}); err != nil || n != 1 {
		t.Fatalf("enqueue=%d err=%v", n, err)
	}
	if _, err := e.db.ExecContext(t.Context(), `DROP INDEX IF EXISTS photo_backup_jobs_ready_fifo; PRAGMA user_version=7; UPDATE photo_backup_jobs SET status='paused',attempts=1,last_error='unknown outcome'`); err != nil {
		t.Fatal(err)
	}
	if err := e.Migrate(t.Context()); err != nil {
		t.Fatal(err)
	}
	var indexes int
	if err := e.db.QueryRowContext(t.Context(), `SELECT count(*) FROM sqlite_master WHERE type='index' AND name='photo_backup_jobs_ready_fifo'`).Scan(&indexes); err != nil || indexes != 1 {
		t.Fatalf("FIFO index count=%d err=%v", indexes, err)
	}
	var status, message string
	var attempts int
	if err := e.db.QueryRowContext(t.Context(), `SELECT status,attempts,last_error FROM photo_backup_jobs`).Scan(&status, &attempts, &message); err != nil {
		t.Fatal(err)
	}
	if status != "paused" || attempts != 1 || message != "unknown outcome" {
		t.Fatalf("migration changed held job: status=%s attempts=%d message=%s", status, attempts, message)
	}
	if err := e.Migrate(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func TestRunNextPreservesFIFOAcrossReadyAndIneligibleJobs(t *testing.T) {
	now := time.Unix(100, 0)
	e, scope := testEngine(t, &now)
	configure(t, e, scope)
	if err := e.UpsertSource(t.Context(), Source{Scope: scope, ID: "disabled", Kind: "folder", Root: "/disabled"}); err != nil {
		t.Fatal(err)
	}
	assets := make([]Asset, 5)
	for i := range assets {
		id := fmt.Sprint(i)
		assets[i] = Asset{ID: id, Version: "v", Path: "/" + id + ".jpg", Name: id + ".jpg", ModifiedAt: now}
	}
	if n, err := e.EnqueuePage(t.Context(), scope, "camera", assets); err != nil || n != len(assets) {
		t.Fatalf("enqueue=%d err=%v", n, err)
	}
	if _, err := e.db.ExecContext(t.Context(), `UPDATE photo_backup_jobs SET created_at=CAST(asset_id AS INTEGER);
UPDATE photo_backup_jobs SET status='error',next_attempt_at=200000000000 WHERE asset_id='0';
UPDATE photo_backup_jobs SET media_type='video' WHERE asset_id='1';
UPDATE photo_backup_jobs SET source_id='disabled' WHERE asset_id='2';
UPDATE photo_backup_jobs SET status='error',next_attempt_at=1 WHERE asset_id='3';
UPDATE photo_backup_settings SET videos=0`); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"3", "4"} {
		var got string
		result, err := e.RunNext(t.Context(), scope, func(_ context.Context, request UploadRequest) (UploadResult, error) {
			got = request.Asset.ID
			return UploadResult{RemoteMessageID: 91}, nil
		})
		if err != nil || !result.Completed || got != want {
			t.Fatalf("got=%q want=%q result=%+v err=%v", got, want, result, err)
		}
	}
	result, err := e.RunNext(t.Context(), scope, func(context.Context, UploadRequest) (UploadResult, error) {
		return UploadResult{}, errors.New("must not claim ineligible work")
	})
	if err != nil || result.Claimed {
		t.Fatalf("ineligible work claimed: result=%+v err=%v", result, err)
	}
}

func TestRunNextConcurrentClaimsReleaseDatabaseBeforeUpload(t *testing.T) {
	now := time.Unix(100, 0)
	e, scope := testEngine(t, &now)
	e.db.SetMaxOpenConns(1)
	configure(t, e, scope)
	assets := []Asset{
		{ID: "a", Version: "v", Path: "/a.jpg", Name: "a.jpg", MediaType: "photo", ModifiedAt: now},
		{ID: "b", Version: "v", Path: "/b.jpg", Name: "b.jpg", MediaType: "photo", ModifiedAt: now},
	}
	if n, err := e.EnqueuePage(t.Context(), scope, "camera", assets); err != nil || n != 2 {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	started := make(chan string, 2)
	release := make(chan struct{})
	var workers sync.WaitGroup
	results := make(chan error, 2)
	defer workers.Wait()
	defer close(release)
	for range 2 {
		workers.Go(func() {
			result, err := e.RunNext(ctx, scope, func(_ context.Context, req UploadRequest) (UploadResult, error) {
				started <- req.Asset.ID
				select {
				case <-release:
				case <-ctx.Done():
				}
				return UploadResult{RemoteMessageID: 12}, nil
			})
			if err == nil && (!result.Claimed || !result.Completed) {
				err = fmt.Errorf("result=%+v", result)
			}
			results <- err
		})
	}
	seen := make(map[string]bool)
	for range 2 {
		select {
		case id := <-started:
			if seen[id] {
				t.Fatalf("duplicate claim: %s", id)
			}
			seen[id] = true
		case <-ctx.Done():
			t.Fatal("second upload could not start while first held no database connection")
		}
	}
	status, err := e.Status(ctx, scope)
	if err != nil || status.Uploading != 2 {
		t.Fatalf("status=%+v err=%v", status, err)
	}
	// Receipt persistence must survive cancellation of both in-flight uploads.
	cancel()
	for range 2 {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	status, err = e.Status(t.Context(), scope)
	if err != nil || status.Complete != 2 {
		t.Fatalf("status=%+v err=%v", status, err)
	}
}

func TestRunNextUnsentCancellationReturnsPending(t *testing.T) {
	now := time.Unix(100, 0)
	e, scope := testEngine(t, &now)
	configure(t, e, scope)
	if n, err := e.EnqueuePage(t.Context(), scope, "camera", []Asset{{ID: "a", Version: "v", Path: "/a.jpg", Name: "a.jpg", MediaType: "photo", ModifiedAt: now}}); err != nil || n != 1 {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	result, err := e.RunNext(ctx, scope, func(context.Context, UploadRequest) (UploadResult, error) {
		cancel()
		return UploadResult{}, errors.Join(ErrUploadNotStarted, context.Canceled)
	})
	if !result.Claimed || result.Completed || !result.Deferred || err != nil {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	status, err := e.Status(t.Context(), scope)
	if err != nil || status.Pending != 1 || status.Paused != 0 {
		t.Fatalf("status=%+v err=%v", status, err)
	}
	var attempts int
	if err := e.db.QueryRowContext(t.Context(), `SELECT attempts FROM photo_backup_jobs`).Scan(&attempts); err != nil || attempts != 0 {
		t.Fatalf("attempts=%d err=%v", attempts, err)
	}
}
