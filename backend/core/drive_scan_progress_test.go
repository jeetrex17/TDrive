package core

import (
	"reflect"
	"sync"
	"testing"
	"time"

	tdsync "TDrive/backend/sync"
)

type recordingEvents struct {
	mu     sync.Mutex
	name   string
	events []any
}

func (r *recordingEvents) Emit(name string, args ...any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.name = name
	r.events = append(r.events, args...)
}

func TestEmitDriveScanProgressUsesStringIDsAndWholeSeconds(t *testing.T) {
	events := &recordingEvents{}
	engine := &Engine{events: events}

	engine.emitDriveScanProgress(tdsync.Progress{
		ChannelID:     9_007_199_254_740_993,
		Phase:         tdsync.ProgressApplying,
		PagesDone:     3,
		PagesTotal:    12,
		MessagesDone:  300,
		MessagesTotal: 1170,
	})
	// A partial second must round up: "resuming in 0s" would read as a hang.
	engine.emitDriveScanProgress(tdsync.Progress{
		ChannelID: 42,
		Phase:     tdsync.ProgressWaiting,
		Wait:      1500 * time.Millisecond,
	})

	if events.name != DriveScanProgressEventName {
		t.Fatalf("event name = %q, want %q", events.name, DriveScanProgressEventName)
	}
	want := []any{
		DriveScanProgressEvent{
			ChannelID: "9007199254740993", Phase: "applying",
			PagesDone: 3, PagesTotal: 12, MessagesDone: 300, MessagesTotal: 1170,
		},
		DriveScanProgressEvent{ChannelID: "42", Phase: "waiting", WaitSeconds: 2},
	}
	if !reflect.DeepEqual(events.events, want) {
		t.Fatalf("events = %#v, want %#v", events.events, want)
	}
}

func TestEmitDriveScanProgressWithoutEventSink(t *testing.T) {
	(&Engine{}).emitDriveScanProgress(tdsync.Progress{ChannelID: 1, Phase: tdsync.ProgressCounting})
	var nilEngine *Engine
	nilEngine.emitDriveScanProgress(tdsync.Progress{ChannelID: 1, Phase: tdsync.ProgressCounting})
}
