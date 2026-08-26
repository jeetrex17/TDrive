package personaldrive

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"testing"

	"TDrive/backend/auth"
	"TDrive/backend/backfill"
	"TDrive/backend/projection"
	tdsync "TDrive/backend/sync"
	"TDrive/backend/tgclient"

	_ "modernc.org/sqlite"
)

type fakeTelegram struct {
	mu           sync.Mutex
	channels     []tgclient.OwnedBroadcastChannel
	listErr      error
	createResult tgclient.OwnedBroadcastChannel
	createErr    error
	listCalls    int
	createCalls  int
}

func (f *fakeTelegram) ListOwnedBroadcastChannels(context.Context) ([]tgclient.OwnedBroadcastChannel, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.listCalls++
	if f.listErr != nil {
		return nil, f.listErr
	}
	out := make([]tgclient.OwnedBroadcastChannel, len(f.channels))
	copy(out, f.channels)
	return out, nil
}

func (f *fakeTelegram) CreateBroadcastChannel(context.Context, string, string) (tgclient.OwnedBroadcastChannel, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.createCalls++
	return f.createResult, f.createErr
}

func (f *fakeTelegram) counts() (list, create int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.listCalls, f.createCalls
}

type fakeAuthoritativeSync struct {
	mu       sync.Mutex
	calls    []int64
	errors   []error
	onEnsure func(int64)
}

func (f *fakeAuthoritativeSync) EnsureAuthoritative(_ context.Context, channelID int64) error {
	f.mu.Lock()
	f.calls = append(f.calls, channelID)
	call := len(f.calls) - 1
	onEnsure := f.onEnsure
	var err error
	if call < len(f.errors) {
		err = f.errors[call]
	}
	f.mu.Unlock()
	if onEnsure != nil {
		onEnsure(channelID)
	}
	return err
}

func newServiceDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestPrepareUsesSavedDriveWithoutDiscovery(t *testing.T) {
	telegram := &fakeTelegram{}
	var used int64
	service := NewService(Config{
		DB:       newServiceDB(t),
		Telegram: telegram,
		LoadConfig: func() (int64, error) {
			return 8200, nil
		},
		UseSaved: func(_ context.Context, id int64) error {
			used = id
			return nil
		},
	})

	state, err := service.Prepare(context.Background())
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if state.Status != StatusReady || state.ChannelID != 8200 {
		t.Fatalf("state = %#v", state)
	}
	if used != 8200 {
		t.Fatalf("used saved channel = %d, want 8200", used)
	}
	listCalls, createCalls := telegram.counts()
	if listCalls != 0 || createCalls != 0 {
		t.Fatalf("Telegram calls = list:%d create:%d, want zero", listCalls, createCalls)
	}
}

func TestPrepareRejectsUnavailableSavedActivation(t *testing.T) {
	wantErr := errors.New("activation failed")
	service := NewService(Config{
		DB:         newServiceDB(t),
		LoadConfig: func() (int64, error) { return 8200, nil },
		UseSaved:   func(context.Context, int64) error { return wantErr },
	})
	if _, err := service.Prepare(context.Background()); !errors.Is(err, wantErr) {
		t.Fatalf("Prepare error = %v, want %v", err, wantErr)
	}

	service = NewService(Config{
		DB:         newServiceDB(t),
		LoadConfig: func() (int64, error) { return 8200, nil },
	})
	if _, err := service.Prepare(context.Background()); err == nil {
		t.Fatal("Prepare unexpectedly succeeded without saved activation")
	}
}

func TestPrepareValidatesConfiguration(t *testing.T) {
	wantErr := errors.New("config unavailable")
	service := NewService(Config{
		LoadConfig: func() (int64, error) { return 0, wantErr },
	})
	if _, err := service.Prepare(context.Background()); !errors.Is(err, wantErr) {
		t.Fatalf("load error = %v, want %v", err, wantErr)
	}

	service = NewService(Config{
		LoadConfig: func() (int64, error) { return -1, nil },
	})
	if _, err := service.Prepare(context.Background()); err == nil {
		t.Fatal("negative configured channel unexpectedly succeeded")
	}
}

func TestPrepareMissingConfigRequiresSelectionWithoutTelegram(t *testing.T) {
	telegram := &fakeTelegram{channels: []tgclient.OwnedBroadcastChannel{{ID: 1, Title: "TDrive"}}}
	service := NewService(Config{
		Telegram:   telegram,
		LoadConfig: func() (int64, error) { return 0, nil },
	})

	state, err := service.Prepare(context.Background())
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if state.Status != StatusSelectionRequired || state.ChannelID != 0 {
		t.Fatalf("state = %#v, want selection required", state)
	}
	if list, create := telegram.counts(); list != 0 || create != 0 {
		t.Fatalf("Prepare touched Telegram: list=%d create=%d", list, create)
	}
}

// An unparseable config.json (for example one edited by hand) must lead to
// the explicit picker, not a dead end, and must never create a channel.
func TestUnreadableConfigIsTreatedAsUnconfigured(t *testing.T) {
	loadInvalid := func() (int64, error) {
		return 0, fmt.Errorf("%w: unexpected token", auth.ErrConfigInvalid)
	}
	telegram := &fakeTelegram{channels: []tgclient.OwnedBroadcastChannel{{
		ID: 1001, AccessHash: 8001, Title: "TDrive", HasActivity: true,
	}}}
	var saved, active int64
	service := NewService(Config{
		DB:         newServiceDB(t),
		Telegram:   telegram,
		Sync:       &fakeAuthoritativeSync{},
		LoadConfig: loadInvalid,
		SaveConfig: func(id int64) error { saved = id; return nil },
		UseSaved:   func(context.Context, int64) error { t.Fatal("activated an unreadable config"); return nil },
		SetActive:  func(id int64) { active = id },
	})

	state, err := service.Prepare(context.Background())
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if state.Status != StatusSelectionRequired {
		t.Fatalf("status = %q, want %q", state.Status, StatusSelectionRequired)
	}
	if err := service.Select(context.Background(), 1001); err != nil {
		t.Fatalf("Select: %v", err)
	}
	if saved != 1001 || active != 1001 {
		t.Fatalf("saved=%d active=%d, want 1001", saved, active)
	}
	if _, creates := telegram.counts(); creates != 0 {
		t.Fatalf("create calls = %d, want 0", creates)
	}
}

func TestDiscoverListsSortedCandidatesWithoutCreating(t *testing.T) {
	telegram := &fakeTelegram{channels: []tgclient.OwnedBroadcastChannel{
		{ID: 9, Title: "Work", CreatedAt: 1, HasActivity: true},
		{ID: 4, Title: "tdrive", CreatedAt: 40, HasActivity: false},
		{ID: 3, Title: " TDrive ", CreatedAt: 30, HasActivity: true},
		{ID: 2, Title: "TDrive", CreatedAt: 20, HasActivity: true},
		{ID: 8, Title: "Archive", CreatedAt: 2, HasActivity: false},
		{ID: 2, Title: "Duplicate", CreatedAt: 1, HasActivity: true},
		{ID: 0, Title: "Invalid", CreatedAt: 1, HasActivity: true},
	}}
	original := append([]tgclient.OwnedBroadcastChannel(nil), telegram.channels...)
	service := NewService(Config{Telegram: telegram})

	candidates, err := service.Discover(context.Background())
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	var ids []int64
	for _, candidate := range candidates {
		ids = append(ids, candidate.ID)
	}
	if want := []int64{2, 3, 4, 9, 8}; !reflect.DeepEqual(ids, want) {
		t.Fatalf("candidate ids = %v, want %v", ids, want)
	}
	if !candidates[0].Recommended || candidates[1].Recommended {
		t.Fatalf("only the first default-titled candidate may be recommended: %#v", candidates)
	}
	if candidates[1].Title != "TDrive" {
		t.Fatalf("title not trimmed: %q", candidates[1].Title)
	}
	if !reflect.DeepEqual(telegram.channels, original) {
		t.Fatalf("Discover mutated Telegram candidates: %#v", telegram.channels)
	}
	if list, create := telegram.counts(); list != 1 || create != 0 {
		t.Fatalf("Telegram calls = list:%d create:%d, want 1 and 0", list, create)
	}
}

func TestDiscoverEmptyAndFailureNeverCreate(t *testing.T) {
	t.Run("empty", func(t *testing.T) {
		telegram := &fakeTelegram{}
		service := NewService(Config{Telegram: telegram})
		candidates, err := service.Discover(context.Background())
		if err != nil {
			t.Fatalf("Discover: %v", err)
		}
		if len(candidates) != 0 {
			t.Fatalf("candidates = %#v, want none", candidates)
		}
		if _, creates := telegram.counts(); creates != 0 {
			t.Fatalf("create calls = %d, want 0", creates)
		}
	})

	t.Run("lookup error", func(t *testing.T) {
		wantErr := errors.New("offline")
		telegram := &fakeTelegram{listErr: wantErr}
		service := NewService(Config{Telegram: telegram})
		if _, err := service.Discover(context.Background()); !errors.Is(err, wantErr) {
			t.Fatalf("Discover error = %v, want %v", err, wantErr)
		}
		if _, creates := telegram.counts(); creates != 0 {
			t.Fatalf("create calls = %d, want 0", creates)
		}
	})

	t.Run("no telegram", func(t *testing.T) {
		if _, err := NewService(Config{}).Discover(context.Background()); err == nil {
			t.Fatal("Discover unexpectedly succeeded without Telegram")
		}
	})
}

func TestSelectRevalidatesAndRejectsUnknownChannel(t *testing.T) {
	telegram := &fakeTelegram{channels: []tgclient.OwnedBroadcastChannel{{ID: 1001, Title: "TDrive"}}}
	var saved, active int64
	service := NewService(Config{
		DB:         newServiceDB(t),
		Telegram:   telegram,
		Sync:       &fakeAuthoritativeSync{},
		LoadConfig: func() (int64, error) { return 0, nil },
		SaveConfig: func(id int64) error { saved = id; return nil },
		SetActive:  func(id int64) { active = id },
	})

	for _, id := range []int64{0, -1, 9999} {
		if err := service.Select(context.Background(), id); err == nil {
			t.Fatalf("Select(%d) unexpectedly succeeded", id)
		}
	}
	if saved != 0 || active != 0 {
		t.Fatalf("invalid selection changed state: saved=%d active=%d", saved, active)
	}
}

func TestSelectAndCreateUseConfiguredDriveWithoutRemoteMutation(t *testing.T) {
	telegram := &fakeTelegram{}
	var used []int64
	service := NewService(Config{
		Telegram:   telegram,
		LoadConfig: func() (int64, error) { return 8200, nil },
		UseSaved: func(_ context.Context, id int64) error {
			used = append(used, id)
			return nil
		},
	})
	if err := service.Select(context.Background(), 9999); err != nil {
		t.Fatalf("Select configured drive: %v", err)
	}
	if err := service.Create(context.Background()); err != nil {
		t.Fatalf("Create configured drive: %v", err)
	}
	if want := []int64{8200, 8200}; !reflect.DeepEqual(used, want) {
		t.Fatalf("activated = %v, want %v", used, want)
	}
	list, create := telegram.counts()
	if list != 0 || create != 0 {
		t.Fatalf("configured path touched Telegram: list=%d create=%d", list, create)
	}
}

func TestSelectPropagatesRevalidationFailure(t *testing.T) {
	wantErr := errors.New("lookup failed")
	service := NewService(Config{
		Telegram:   &fakeTelegram{listErr: wantErr},
		LoadConfig: func() (int64, error) { return 0, nil },
	})
	if err := service.Select(context.Background(), 8200); !errors.Is(err, wantErr) {
		t.Fatalf("Select error = %v, want %v", err, wantErr)
	}
}

func TestSelectUsesFreshMetadataAndCommitsAfterAuthoritativeSync(t *testing.T) {
	db := newServiceDB(t)
	telegram := &fakeTelegram{channels: []tgclient.OwnedBroadcastChannel{{
		ID: 1001, AccessHash: 8001, Title: "Renamed TDrive", CreatedAt: 123, HasActivity: true,
	}}}
	var order []string
	syncer := &fakeAuthoritativeSync{onEnsure: func(id int64) {
		row, err := projection.GetChannel(db, id)
		if err != nil {
			t.Errorf("channel not registered before sync: %v", err)
			return
		}
		if row.AccessHash != 8001 || row.Title != "Renamed TDrive" || row.JoinedAt != 123 {
			t.Errorf("registered row = %#v", row)
		}
		order = append(order, "sync")
	}}
	var saved, active int64
	service := NewService(Config{
		DB:         db,
		Telegram:   telegram,
		Sync:       syncer,
		LoadConfig: func() (int64, error) { return saved, nil },
		SaveConfig: func(id int64) error {
			order = append(order, "save")
			saved = id
			return nil
		},
		SetActive: func(id int64) {
			order = append(order, "active")
			active = id
		},
	})

	if err := service.Select(context.Background(), 1001); err != nil {
		t.Fatalf("Select: %v", err)
	}
	if want := []string{"sync", "save", "active"}; !reflect.DeepEqual(order, want) {
		t.Fatalf("order = %v, want %v", order, want)
	}
	if saved != 1001 || active != 1001 {
		t.Fatalf("saved=%d active=%d, want 1001", saved, active)
	}
	row, err := projection.GetChannel(db, 1001)
	if err != nil {
		t.Fatalf("GetChannel: %v", err)
	}
	if !row.PersonalBackfillDone {
		// The fake sync does not set initial_sync_done, but successful recovery
		// must still suppress the legacy write-producing personal backfill.
		t.Fatal("recovered channel was not marked backfill-complete")
	}
}

func TestSelectSyncFailureDoesNotCommitOrActivate(t *testing.T) {
	wantErr := errors.New("history unavailable")
	var saved, active int64
	db := newServiceDB(t)
	service := NewService(Config{
		DB: db,
		Telegram: &fakeTelegram{channels: []tgclient.OwnedBroadcastChannel{{
			ID: 1001, AccessHash: 8001, Title: "TDrive",
		}}},
		Sync:       &fakeAuthoritativeSync{errors: []error{wantErr}},
		LoadConfig: func() (int64, error) { return 0, nil },
		SaveConfig: func(id int64) error { saved = id; return nil },
		SetActive:  func(id int64) { active = id },
	})

	if err := service.Select(context.Background(), 1001); !errors.Is(err, wantErr) {
		t.Fatalf("Select error = %v, want %v", err, wantErr)
	}
	if saved != 0 || active != 0 {
		t.Fatalf("failed sync changed durable state: saved=%d active=%d", saved, active)
	}
	channels, err := projection.ListChannels(db)
	if err != nil {
		t.Fatalf("ListChannels after failed recovery: %v", err)
	}
	if len(channels) != 0 {
		t.Fatalf("failed recovery left provisional channels: %#v", channels)
	}
}

func TestFailedSelectionDoesNotPolluteLaterSelection(t *testing.T) {
	wantErr := errors.New("first history scan failed")
	db := newServiceDB(t)
	telegram := &fakeTelegram{channels: []tgclient.OwnedBroadcastChannel{
		{ID: 1001, AccessHash: 8001, Title: "Old TDrive"},
		{ID: 1002, AccessHash: 8002, Title: "Current TDrive"},
	}}
	syncer := &fakeAuthoritativeSync{errors: []error{wantErr, nil}}
	var saved int64
	service := NewService(Config{
		DB:         db,
		Telegram:   telegram,
		Sync:       syncer,
		LoadConfig: func() (int64, error) { return saved, nil },
		SaveConfig: func(id int64) error { saved = id; return nil },
		SetActive:  func(int64) {},
	})

	if err := service.Select(context.Background(), 1001); !errors.Is(err, wantErr) {
		t.Fatalf("first Select error = %v, want %v", err, wantErr)
	}
	if err := service.Select(context.Background(), 1002); err != nil {
		t.Fatalf("second Select: %v", err)
	}
	channels, err := projection.ListChannels(db)
	if err != nil {
		t.Fatalf("ListChannels: %v", err)
	}
	if len(channels) != 1 || channels[0].ChannelID != 1002 || channels[0].Kind != projection.KindPersonal {
		t.Fatalf("channels after replacement selection = %#v", channels)
	}
}

// A user who already launched a version that auto-created an empty channel
// has that channel registered as personal. Recovering the real drive must
// retire it so the sidebar and mount picker never show two personal drives.
func TestSelectRetiresStalePersonalChannelAndKeepsSharedDrives(t *testing.T) {
	db := newServiceDB(t)
	const stale, shared, recovered int64 = 4000, 5000, 1001
	if err := projection.MigratePersonalChannel(db, stale); err != nil {
		t.Fatalf("register stale personal channel: %v", err)
	}
	if err := projection.InsertChannel(db, projection.Channel{
		ChannelID: shared, AccessHash: 1, Title: "Team", Kind: projection.KindShared,
	}); err != nil {
		t.Fatalf("register shared channel: %v", err)
	}
	service := NewService(Config{
		DB: db,
		Telegram: &fakeTelegram{channels: []tgclient.OwnedBroadcastChannel{{
			ID: recovered, AccessHash: 8001, Title: "TDrive", CreatedAt: 10, HasActivity: true,
		}}},
		Sync:       &fakeAuthoritativeSync{},
		LoadConfig: func() (int64, error) { return 0, nil },
		SaveConfig: func(int64) error { return nil },
		SetActive:  func(int64) {},
	})

	if err := service.Select(context.Background(), recovered); err != nil {
		t.Fatalf("Select: %v", err)
	}
	channels, err := projection.ListChannels(db)
	if err != nil {
		t.Fatalf("ListChannels: %v", err)
	}
	var ids []int64
	var personal int
	for _, channel := range channels {
		ids = append(ids, channel.ChannelID)
		if channel.Kind == projection.KindPersonal {
			personal++
		}
	}
	if want := []int64{recovered, shared}; !reflect.DeepEqual(ids, want) || personal != 1 {
		t.Fatalf("channels after recovery = %v (personal=%d), want %v with one personal", ids, personal, want)
	}
}

func TestCreateRetriesPendingChannelWithoutCreatingAnother(t *testing.T) {
	wantErr := errors.New("first sync failed")
	telegram := &fakeTelegram{createResult: tgclient.OwnedBroadcastChannel{
		ID: 5001, AccessHash: 9001, Title: "TDrive", CreatedAt: 500,
	}}
	syncer := &fakeAuthoritativeSync{errors: []error{wantErr, nil}}
	var saved, active int64
	db := newServiceDB(t)
	service := NewService(Config{
		DB:         db,
		Telegram:   telegram,
		Sync:       syncer,
		LoadConfig: func() (int64, error) { return saved, nil },
		SaveConfig: func(id int64) error { saved = id; return nil },
		SetActive:  func(id int64) { active = id },
	})

	if err := service.Create(context.Background()); !errors.Is(err, wantErr) {
		t.Fatalf("first Create error = %v, want %v", err, wantErr)
	}
	channels, err := projection.ListChannels(db)
	if err != nil {
		t.Fatalf("ListChannels after failed Create: %v", err)
	}
	if len(channels) != 0 {
		t.Fatalf("failed Create left provisional channels: %#v", channels)
	}
	if err := service.Create(context.Background()); err != nil {
		t.Fatalf("second Create: %v", err)
	}
	_, creates := telegram.counts()
	if creates != 1 {
		t.Fatalf("create calls = %d, want 1", creates)
	}
	if saved != 5001 || active != 5001 {
		t.Fatalf("saved=%d active=%d, want 5001", saved, active)
	}
}

func TestCreateRetriesPendingChannelAfterConfigSaveFailure(t *testing.T) {
	wantErr := errors.New("disk full")
	telegram := &fakeTelegram{createResult: tgclient.OwnedBroadcastChannel{
		ID: 5001, AccessHash: 9001, Title: "TDrive", CreatedAt: 500,
	}}
	var saves, active int
	db := newServiceDB(t)
	service := NewService(Config{
		DB:         db,
		Telegram:   telegram,
		Sync:       &fakeAuthoritativeSync{},
		LoadConfig: func() (int64, error) { return 0, nil },
		SaveConfig: func(int64) error {
			saves++
			if saves == 1 {
				return wantErr
			}
			return nil
		},
		SetActive: func(int64) { active++ },
	})
	if err := service.Create(context.Background()); !errors.Is(err, wantErr) {
		t.Fatalf("first Create error = %v, want %v", err, wantErr)
	}
	channels, err := projection.ListChannels(db)
	if err != nil {
		t.Fatalf("ListChannels after failed config save: %v", err)
	}
	if len(channels) != 0 {
		t.Fatalf("failed config save left provisional channels: %#v", channels)
	}
	if err := service.Create(context.Background()); err != nil {
		t.Fatalf("second Create: %v", err)
	}
	_, creates := telegram.counts()
	if creates != 1 || saves != 2 || active != 1 {
		t.Fatalf("creates=%d saves=%d active=%d", creates, saves, active)
	}
}

func TestCreateRejectsRemoteFailuresAndInvalidResults(t *testing.T) {
	wantErr := errors.New("create failed")
	service := NewService(Config{
		Telegram:   &fakeTelegram{createErr: wantErr},
		LoadConfig: func() (int64, error) { return 0, nil },
	})
	if err := service.Create(context.Background()); !errors.Is(err, wantErr) {
		t.Fatalf("Create error = %v, want %v", err, wantErr)
	}

	service = NewService(Config{
		Telegram:   &fakeTelegram{createResult: tgclient.OwnedBroadcastChannel{}},
		LoadConfig: func() (int64, error) { return 0, nil },
	})
	if err := service.Create(context.Background()); err == nil {
		t.Fatal("Create unexpectedly accepted a zero channel id")
	}
}

func TestSelectRequiresRecoveryDependencies(t *testing.T) {
	service := NewService(Config{
		DB: newServiceDB(t),
		Telegram: &fakeTelegram{channels: []tgclient.OwnedBroadcastChannel{{
			ID: 8200, Title: "TDrive",
		}}},
		LoadConfig: func() (int64, error) { return 0, nil },
	})
	if err := service.Select(context.Background(), 8200); err == nil {
		t.Fatal("Select unexpectedly succeeded without sync/activation dependencies")
	}
}

type fakePeerResolver struct{ telegram *tgclient.Fake }

func (r fakePeerResolver) ResolvePeer(ctx context.Context, channelID int64) (tgclient.InputPeer, error) {
	return r.telegram.ResolveDriveChannel(ctx, channelID)
}

func TestSelectRecoversRemoteTreeWithoutTelegramWrites(t *testing.T) {
	const channelID int64 = 8200
	db := newServiceDB(t)
	telegram := tgclient.NewFake(77)
	peer := tgclient.InputPeer{ChannelID: channelID, AccessHash: 9200}
	telegram.SeedChannel(peer, "TDrive")
	telegram.SeedOwnedBroadcastChannels(tgclient.OwnedBroadcastChannel{
		ID: channelID, AccessHash: peer.AccessHash, Title: "TDrive", CreatedAt: 10, HasActivity: true,
	})
	telegram.SeedHistory(
		tgclient.HistoryMessage{MsgID: 10, Text: projection.Format(projection.Op{
			Type: projection.OpMkdir, Obj: "d:photos", Parent: projection.RootParent, Name: "Photos",
		})},
		tgclient.HistoryMessage{MsgID: 11, Text: projection.Format(projection.Op{
			Type: projection.OpMkdir, Obj: "d:trip", Parent: "d:photos", Name: "Trip",
		})},
		tgclient.HistoryMessage{MsgID: 12, Text: projection.Format(projection.Op{
			Type: projection.OpFileUpload, Parent: "d:trip", Name: "photo.jpg", FileSize: 42, FileUploadTime: 100,
		}), HasMedia: true, MediaSize: 42, DocumentName: "photo.jpg"},
	)
	resolver := fakePeerResolver{telegram: telegram}
	syncer := tdsync.NewEngine(db, telegram, resolver)
	var saved, active int64
	service := NewService(Config{
		DB:         db,
		Telegram:   telegram,
		Sync:       syncer,
		LoadConfig: func() (int64, error) { return saved, nil },
		SaveConfig: func(id int64) error { saved = id; return nil },
		SetActive:  func(id int64) { active = id },
	})

	if err := service.Select(context.Background(), channelID); err != nil {
		t.Fatalf("Select: %v", err)
	}
	if saved != channelID || active != channelID {
		t.Fatalf("saved=%d active=%d, want %d", saved, active, channelID)
	}
	photos, ok, err := projection.FolderByID(db, channelID, "d:photos")
	if err != nil || !ok || photos.ParentID != projection.RootParent {
		t.Fatalf("photos folder = %#v, %v, %v", photos, ok, err)
	}
	trip, ok, err := projection.FolderByID(db, channelID, "d:trip")
	if err != nil || !ok || trip.ParentID != "d:photos" {
		t.Fatalf("trip folder = %#v, %v, %v", trip, ok, err)
	}
	file, ok, err := projection.FileByID(db, channelID, 12)
	if err != nil || !ok || file.ParentID != "d:trip" || file.Name != "photo.jpg" || file.Size != 42 {
		t.Fatalf("file = %#v, %v, %v", file, ok, err)
	}
	row, err := projection.GetChannel(db, channelID)
	if err != nil {
		t.Fatalf("GetChannel: %v", err)
	}
	if !row.InitialSyncDone || !row.PersonalBackfillDone {
		t.Fatalf("channel readiness = %#v", row)
	}

	// Even an explicit later backfill run must remain a no-op for a recovered
	// channel; recovery is a read-only operation against Telegram.
	runner := backfill.NewRunner(db, telegram, resolver)
	if err := runner.RunPersonal(context.Background(), channelID, nil); err != nil {
		t.Fatalf("RunPersonal after recovery: %v", err)
	}
	if len(telegram.SentControls()) != 0 || len(telegram.SentFiles()) != 0 || len(telegram.DeletedBatches()) != 0 {
		t.Fatalf("recovery wrote to Telegram: controls=%d files=%d deletes=%d",
			len(telegram.SentControls()), len(telegram.SentFiles()), len(telegram.DeletedBatches()))
	}
}
