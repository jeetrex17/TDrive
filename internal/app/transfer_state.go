package app

import (
	"context"
	"fmt"
	"sync"
)

type transferKind uint8

const (
	uploadTransfer transferKind = iota
	downloadTransfer
)

type transferRun struct {
	cancel context.CancelFunc
}

// transferState owns cancellation and the mobile idle-timer override together.
// Its zero value is ready for use; App retains process lifecycle ownership.
type transferState struct {
	mu            sync.Mutex
	active        [2]*transferRun
	keepAwake     bool
	pickerSources map[string]struct{}
}

func (s *transferState) begin(ctx context.Context, kind transferKind) (context.Context, func()) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if previous := s.active[kind]; previous != nil {
		previous.cancel()
	}
	return s.startLocked(ctx, kind)
}

// Resume must not stop an unrelated batch selected by the user.
func (s *transferState) resumeUpload(ctx context.Context) (context.Context, func(), error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.active[uploadTransfer] != nil {
		return nil, nil, fmt.Errorf("another upload is already running")
	}
	ctx, finish := s.startLocked(ctx, uploadTransfer)
	return ctx, finish, nil
}

func (s *transferState) startLocked(parent context.Context, kind transferKind) (context.Context, func()) {
	ctx, cancel := context.WithCancel(parent)
	run := &transferRun{cancel: cancel}
	s.active[kind] = run
	s.syncKeepAwakeLocked()
	return ctx, func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		cancel()
		// A canceled predecessor may finish after its replacement has started.
		// Only its own run can release the slot and the phone's awake lease.
		if s.active[kind] == run {
			s.active[kind] = nil
			s.syncKeepAwakeLocked()
		}
	}
}

func (s *transferState) cancel(kind transferKind) {
	s.mu.Lock()
	run := s.active[kind]
	s.mu.Unlock()
	if run != nil {
		run.cancel()
	}
}

func (s *transferState) syncKeepAwakeLocked() {
	active := s.active[uploadTransfer] != nil || s.active[downloadTransfer] != nil
	if active != s.keepAwake {
		s.keepAwake = active
		mobileKeepAwake(active)
	}
}

func (s *transferState) rememberPickerSources(paths []string) {
	sources := make(map[string]struct{}, len(paths))
	for _, path := range paths {
		sources[path] = struct{}{}
	}
	s.mu.Lock()
	s.pickerSources = sources
	s.mu.Unlock()
}

// takePickerSource consumes ownership once, before a disposable mobile copy
// can be moved into durable resumable-upload storage.
func (s *transferState) takePickerSource(path string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, owned := s.pickerSources[path]
	delete(s.pickerSources, path)
	return owned
}
