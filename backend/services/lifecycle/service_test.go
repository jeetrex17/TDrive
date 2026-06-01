package lifecycle

import (
	"context"
	"database/sql"
	"fmt"
	"sync"
	"testing"

	"TDrive/backend/backfill"
	"TDrive/backend/projection"

	_ "modernc.org/sqlite"
)

type fakeSyncer struct {
	called    int
	channelID int64
	err       error
}

func (f *fakeSyncer) Incremental(ctx context.Context, channelID int64) error {
	f.called++
	f.channelID = channelID
	return f.err
}

type fakeBackfiller struct {
	mu        sync.Mutex
	started   int
	release   chan struct{}
	startedCh chan struct{}
}

func newFakeBackfiller() *fakeBackfiller {
	return &fakeBackfiller{
		release:   make(chan struct{}),
		startedCh: make(chan struct{}, 1),
	}
}

func (f *fakeBackfiller) RunPersonal(ctx context.Context, channelID int64, onProgress func(backfill.ProgressEvent)) error {
	f.mu.Lock()
	f.started++
	started := f.started
	f.mu.Unlock()
	if started == 1 {
		f.startedCh <- struct{}{}
	}
	if onProgress != nil {
		onProgress(backfill.ProgressEvent{ChannelID: channelID, Done: 1, Total: 1, Phase: "done"})
	}
	<-f.release
	return nil
}

func (f *fakeBackfiller) Started() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.started
}

type fakeEvents struct {
	mu     sync.Mutex
	events []string
}

func (f *fakeEvents) Emit(name string, args ...any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.events = append(f.events, name)
}

func (f *fakeEvents) Has(name string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, ev := range f.events {
		if ev == name {
			return true
		}
	}
	return false
}

func testDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := projection.EnsureSchema(db); err != nil {
		t.Fatalf("ensure schema: %v", err)
	}
	return db
}

func TestInitDriveMigratesAndSetsActive(t *testing.T) {
	db := testDB(t)
	active := NewActiveDrive()
	backfiller := newFakeBackfiller()
	defer close(backfiller.release)

	svc := NewService(Config{
		DB:       db,
		Active:   active,
		Backfill: backfiller,
		PersonalChannel: func(ctx context.Context) (int64, error) {
			return 12345, nil
		},
	})

	got := svc.InitDrive(context.Background())
	if got != "Success , channel ID: 12345" {
		t.Fatalf("InitDrive = %q", got)
	}
	if active.ID() != 12345 {
		t.Fatalf("active = %d, want 12345", active.ID())
	}
	var kind string
	if err := db.QueryRow(`SELECT kind FROM channels WHERE channel_id = ?`, int64(12345)).Scan(&kind); err != nil {
		t.Fatalf("channel row missing: %v", err)
	}
	if kind != projection.KindPersonal {
		t.Fatalf("kind = %q, want %q", kind, projection.KindPersonal)
	}
}

func TestKickoffBackfillRunsOnce(t *testing.T) {
	db := testDB(t)
	backfiller := newFakeBackfiller()
	events := &fakeEvents{}
	svc := NewService(Config{
		DB:       db,
		Active:   NewActiveDrive(),
		Backfill: backfiller,
		Events:   events,
	})

	svc.kickoffPersonalBackfill(context.Background(), 77)
	<-backfiller.startedCh
	svc.kickoffPersonalBackfill(context.Background(), 77)
	if got := backfiller.Started(); got != 1 {
		t.Fatalf("backfill started %d times, want 1", got)
	}
	close(backfiller.release)
	if !events.Has("backfill_progress") {
		t.Fatalf("backfill_progress was not emitted")
	}
}

func TestSyncChannelDelegates(t *testing.T) {
	active := NewActiveDrive()
	active.Set(99)
	syncer := &fakeSyncer{}
	svc := NewService(Config{
		Active: active,
		Sync:   syncer,
	})

	if err := svc.SyncChannel(context.Background(), 0); err != nil {
		t.Fatalf("sync: %v", err)
	}
	if syncer.called != 1 || syncer.channelID != 99 {
		t.Fatalf("sync called=%d channel=%d, want called=1 channel=99", syncer.called, syncer.channelID)
	}
}

func TestRebuildProjectionDelegates(t *testing.T) {
	db := testDB(t)
	active := NewActiveDrive()
	active.Set(55)
	var rebuilt int64
	svc := NewService(Config{
		DB:     db,
		Active: active,
		Rebuild: func(gotDB *sql.DB, channelID int64) error {
			if gotDB != db {
				return fmt.Errorf("unexpected db")
			}
			rebuilt = channelID
			return nil
		},
	})

	if err := svc.RebuildProjection(0); err != nil {
		t.Fatalf("rebuild: %v", err)
	}
	if rebuilt != 55 {
		t.Fatalf("rebuilt channel = %d, want 55", rebuilt)
	}
}
