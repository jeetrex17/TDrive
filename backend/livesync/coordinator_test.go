package livesync

import (
	"context"
	"sync"
	"testing"
	"testing/synctest"
	"time"
)

type fakeActivity struct {
	ch chan int64
}

func newFakeActivity() *fakeActivity {
	return &fakeActivity{ch: make(chan int64, 32)}
}

func (f *fakeActivity) Signals() <-chan int64 { return f.ch }

func (f *fakeActivity) Signal(id int64) { f.ch <- id }

type fakeSyncer struct {
	mu    sync.Mutex
	calls []int64
	err   error
}

func (f *fakeSyncer) SyncChannel(_ context.Context, channelID int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, channelID)
	return f.err
}

func (f *fakeSyncer) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

func (f *fakeSyncer) snapshot() []int64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]int64(nil), f.calls...)
}

type eventRecorder struct {
	mu     sync.Mutex
	events []string
}

func (r *eventRecorder) Emit(name string, _ ...any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, name)
}

func (r *eventRecorder) has(name string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, event := range r.events {
		if event == name {
			return true
		}
	}
	return false
}

func TestCoordinatorCoalescesSignalBursts(t *testing.T) {
	activity := newFakeActivity()
	syncer := &fakeSyncer{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	c := NewCoordinator(Config{
		Activity: activity,
		Syncer:   syncer,
		ListChannels: func(context.Context) ([]int64, error) {
			return []int64{42}, nil
		},
		Debounce:         10 * time.Millisecond,
		BackstopInterval: time.Hour,
	})
	c.Start(ctx)
	defer c.Stop()

	activity.Signal(42)
	activity.Signal(42)
	activity.Signal(42)

	eventually(t, time.Second, func() bool { return syncer.count() == 1 })
	if got := syncer.snapshot(); len(got) != 1 || got[0] != 42 {
		t.Fatalf("sync calls = %v, want [42]", got)
	}
}

func TestCoordinatorIgnoresUnknownChannels(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		const debounce = 5 * time.Millisecond
		activity := newFakeActivity()
		syncer := &fakeSyncer{}
		loads := 0
		c := NewCoordinator(Config{
			Activity: activity,
			Syncer:   syncer,
			ListChannels: func(context.Context) ([]int64, error) {
				loads++
				return []int64{1}, nil
			},
			Debounce:         debounce,
			BackstopInterval: time.Hour,
		})
		c.Start(t.Context())
		t.Cleanup(c.Stop)
		synctest.Wait()

		activity.Signal(99)
		synctest.Wait()
		if loads != 2 {
			t.Fatalf("known-channel loads = %d, want startup and unknown-signal reload", loads)
		}
		// Advance beyond debounce only after the unknown signal was processed.
		time.Sleep(debounce)
		synctest.Wait()
		if got := syncer.snapshot(); len(got) != 0 {
			t.Fatalf("sync calls = %v, want none for unknown channel 99", got)
		}

		activity.Signal(1)
		synctest.Wait()
		time.Sleep(debounce)
		synctest.Wait()
		if got := syncer.snapshot(); len(got) != 1 || got[0] != 1 {
			t.Fatalf("sync calls = %v, want [1] after a known-channel signal", got)
		}
	})
}

func TestCoordinatorBackstopSyncsKnownChannels(t *testing.T) {
	activity := newFakeActivity()
	syncer := &fakeSyncer{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	c := NewCoordinator(Config{
		Activity: activity,
		Syncer:   syncer,
		ListChannels: func(context.Context) ([]int64, error) {
			return []int64{7, 8}, nil
		},
		Debounce:         time.Millisecond,
		BackstopInterval: 10 * time.Millisecond,
	})
	c.Start(ctx)
	defer c.Stop()

	eventually(t, time.Second, func() bool { return syncer.count() >= 2 })
}

func TestCoordinatorEmitsFailureEvents(t *testing.T) {
	activity := newFakeActivity()
	syncer := &fakeSyncer{err: context.Canceled}
	events := &eventRecorder{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	c := NewCoordinator(Config{
		Activity: activity,
		Syncer:   syncer,
		Events:   events,
		ListChannels: func(context.Context) ([]int64, error) {
			return []int64{5}, nil
		},
		Debounce:         5 * time.Millisecond,
		BackstopInterval: time.Hour,
	})
	c.Start(ctx)
	defer c.Stop()

	activity.Signal(5)
	eventually(t, time.Second, func() bool { return events.has(EventFailed) })
}

func eventually(t *testing.T, timeout time.Duration, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if ok() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("condition not met within %s", timeout)
}
