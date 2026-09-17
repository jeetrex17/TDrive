package galleryprepare

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

type fakeSource struct {
	mu        sync.Mutex
	items     []Item
	ready     map[int64]bool
	pageSizes []int
	budgets   []int64
	prepare   func(context.Context, int64) (int64, error)
	estimate  func(context.Context) (Estimate, error)
}

func (f *fakeSource) Estimate(ctx context.Context, channelID int64) (Estimate, error) {
	if f.estimate != nil {
		return f.estimate(ctx)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	var out Estimate
	for _, item := range f.items {
		if !f.ready[item.MsgID] {
			out.Total++
			out.BytesTotal += item.Size
		}
	}
	return out, nil
}
func (f *fakeSource) Page(ctx context.Context, channelID, after int64, limit int) ([]Item, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.pageSizes = append(f.pageSizes, limit)
	out := []Item{}
	for _, item := range f.items {
		if item.MsgID > after && !f.ready[item.MsgID] {
			out = append(out, item)
			if len(out) == limit {
				break
			}
		}
	}
	return out, nil
}
func (f *fakeSource) Prepare(ctx context.Context, channelID, msgID, budget int64) (int64, error) {
	if budget != 10 {
		return 0, errors.New("runner did not pass admitted source bytes")
	}
	n, err := int64(10), error(nil)
	if f.prepare != nil {
		n, err = f.prepare(ctx, msgID)
	}
	if err == nil {
		f.mu.Lock()
		f.ready[msgID] = true
		f.mu.Unlock()
	}
	return n, err
}
func newSource(n int) *fakeSource {
	f := &fakeSource{ready: map[int64]bool{}}
	for i := 1; i <= n; i++ {
		f.items = append(f.items, Item{MsgID: int64(i), Size: 10})
	}
	return f
}
func waitStopped(t *testing.T, events <-chan State) State {
	t.Helper()
	deadline := time.After(3 * time.Second)
	for {
		select {
		case state := <-events:
			if !state.Running {
				return state
			}
		case <-deadline:
			t.Fatal("preparation did not stop")
			return State{}
		}
	}
}
func newTestRunner() *Runner { return New(nil) }

func TestRunnerBoundsPagesAndReportsProgress(t *testing.T) {
	source := newSource(137)
	events := make(chan State, 200)
	runner := New(func(state State) { events <- state })
	if err := runner.Start(context.Background(), 7, source); err != nil {
		t.Fatal(err)
	}
	state := waitStopped(t, events)
	if state.ChannelID != 7 || state.Completed != 137 || state.Total != 137 || state.BytesDone != 1370 || state.BytesTotal != 1370 || state.Error != "" {
		t.Fatalf("state=%+v", state)
	}
	for _, limit := range source.pageSizes {
		if limit != 64 {
			t.Fatalf("unbounded page=%d", limit)
		}
	}
	if runner.Snapshot() != state {
		t.Fatal("snapshot does not match progress")
	}
}

func TestRunnerCancelStopsActiveWorkAndAccountsPartialBytes(t *testing.T) {
	source := newSource(3)
	entered := make(chan struct{})
	source.prepare = func(ctx context.Context, id int64) (int64, error) { close(entered); <-ctx.Done(); return 6, ctx.Err() }
	events := make(chan State, 10)
	runner := New(func(state State) { events <- state })
	if err := runner.Start(context.Background(), 7, source); err != nil {
		t.Fatal(err)
	}
	<-entered
	if err := runner.Start(context.Background(), 7, source); !errors.Is(err, ErrRunning) {
		t.Fatalf("concurrent start=%v", err)
	}
	runner.Stop()
	state := waitStopped(t, events)
	if state.Completed != 0 || state.BytesDone != 6 || state.Error != "" {
		t.Fatalf("cancel state=%+v", state)
	}
}

func TestRunnerResumesMissingOnlyAfterError(t *testing.T) {
	source := newSource(4)
	fail := true
	source.prepare = func(ctx context.Context, id int64) (int64, error) {
		if id == 3 && fail {
			fail = false
			return 4, errors.New("temporary failure")
		}
		return 10, nil
	}
	events := make(chan State, 30)
	runner := New(func(state State) { events <- state })
	if err := runner.Start(context.Background(), 7, source); err != nil {
		t.Fatal(err)
	}
	state := waitStopped(t, events)
	if state.Completed != 2 || state.BytesDone != 24 || state.Error == "" {
		t.Fatalf("failure=%+v", state)
	}
	if err := runner.Start(context.Background(), 7, source); err != nil {
		t.Fatal(err)
	}
	state = waitStopped(t, events)
	if state.Total != 2 || state.Completed != 2 || state.BytesDone != 20 || state.Error != "" {
		t.Fatalf("resume=%+v", state)
	}
}

func TestRunnerCancellationDuringEstimateCannotStartDownloads(t *testing.T) {
	source := newSource(3)
	entered := make(chan struct{})
	source.estimate = func(ctx context.Context) (Estimate, error) {
		close(entered)
		<-ctx.Done()
		return Estimate{}, ctx.Err()
	}
	events := make(chan State, 10)
	runner := New(func(state State) { events <- state })
	if err := runner.Start(context.Background(), 7, source); err != nil {
		t.Fatal(err)
	}
	<-entered
	runner.Stop()
	state := waitStopped(t, events)
	if state.Error != "" || state.BytesDone != 0 || len(source.pageSizes) != 0 {
		t.Fatalf("state=%+v", state)
	}
}

func TestRunnerValidatesInputsAndSourceBudget(t *testing.T) {
	runner := newTestRunner()
	if err := runner.Start(nil, 7, newSource(1)); err == nil {
		t.Fatal("nil context accepted")
	}
	if err := runner.Start(context.Background(), 0, newSource(1)); err == nil {
		t.Fatal("zero channel accepted")
	}
	if err := runner.Start(context.Background(), 7, nil); err == nil {
		t.Fatal("nil source accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := runner.Start(ctx, 7, newSource(1)); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel=%v", err)
	}
	events := make(chan State, 10)
	runner = New(func(state State) { events <- state })
	source := newSource(2)
	source.estimate = func(context.Context) (Estimate, error) { return Estimate{Total: 2, BytesTotal: 5}, nil }
	if err := runner.Start(context.Background(), 7, source); err != nil {
		t.Fatal(err)
	}
	state := waitStopped(t, events)
	if state.BytesDone != 0 || state.Error == "" {
		t.Fatalf("budget=%+v", state)
	}
}

func TestRunnerSkipsUnsupportedSourcesWithoutStoppingLaterPhotos(t *testing.T) {
	source := newSource(3)
	source.prepare = func(ctx context.Context, id int64) (int64, error) {
		if id == 2 {
			return 10, ErrSkipped
		}
		return 10, nil
	}
	events := make(chan State, 20)
	runner := New(func(state State) { events <- state })
	if err := runner.Start(context.Background(), 7, source); err != nil {
		t.Fatal(err)
	}
	state := waitStopped(t, events)
	if state.Completed != 2 || state.Skipped != 1 || state.BytesDone != 30 || state.Error != "" {
		t.Fatalf("skip=%+v", state)
	}
}

func TestRunnerProgressObserverMayReadAndCancel(t *testing.T) {
	source := newSource(3)
	events := make(chan State, 20)
	var runner *Runner
	runner = New(func(state State) {
		_ = runner.Snapshot()
		if state.Completed == 1 {
			runner.Stop()
		}
		events <- state
	})
	if err := runner.Start(context.Background(), 7, source); err != nil {
		t.Fatal(err)
	}
	state := waitStopped(t, events)
	if state.Completed != 1 || state.Error != "" {
		t.Fatalf("observer cancellation=%+v", state)
	}
}
