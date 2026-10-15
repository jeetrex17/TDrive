package file

import (
	"context"
	"fmt"
	"sync"
)

// Interactive uploads can be paused and have files cancelled before they
// start. Neither touches a send already under way: an ordinary upload cannot
// stop part way and continue later, so pausing holds back files that have not
// started, and in-flight files finish (or are cancelled one by one).

// errUploadSkipped marks a file cancelled before it started. Nothing was sent.
var errUploadSkipped = fmt.Errorf("upload canceled before it started: %w", context.Canceled)

type uploadPause struct {
	mu     sync.Mutex
	resume chan struct{} // non-nil while paused; closed on resume
}

// PauseUploads holds back interactive files that have not started yet.
func (s *Service) PauseUploads() {
	s.pause.mu.Lock()
	defer s.pause.mu.Unlock()
	if s.pause.resume == nil {
		s.pause.resume = make(chan struct{})
	}
}

// ResumeUploads lets held-back files start again.
func (s *Service) ResumeUploads() {
	s.pause.mu.Lock()
	defer s.pause.mu.Unlock()
	if s.pause.resume != nil {
		close(s.pause.resume)
		s.pause.resume = nil
	}
}

// UploadsPaused reports whether interactive uploads are held back.
func (s *Service) UploadsPaused() bool {
	s.pause.mu.Lock()
	defer s.pause.mu.Unlock()
	return s.pause.resume != nil
}

// waitWhileUploadsPaused blocks a file that has not started until the user
// resumes or the batch is cancelled.
func (s *Service) waitWhileUploadsPaused(ctx context.Context) error {
	s.pause.mu.Lock()
	resume := s.pause.resume
	s.pause.mu.Unlock()
	if resume == nil {
		return nil
	}
	select {
	case <-resume:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// uploadWindow is one running batch's id range. A cancel that arrives for a
// file in the range before it starts marks it skipped instead of missing it.
type uploadWindow struct {
	first, end int
	started    map[int]bool
	skipped    map[int]bool
}

func (s *Service) openUploadWindow(first, count int) (*uploadWindow, func()) {
	window := &uploadWindow{first: first, end: first + count, started: make(map[int]bool), skipped: make(map[int]bool)}
	s.uploadCancelMu.Lock()
	s.uploadWindows = append(s.uploadWindows, window)
	s.uploadCancelMu.Unlock()
	return window, func() {
		s.uploadCancelMu.Lock()
		defer s.uploadCancelMu.Unlock()
		for index, open := range s.uploadWindows {
			if open == window {
				s.uploadWindows = append(s.uploadWindows[:index], s.uploadWindows[index+1:]...)
				return
			}
		}
	}
}

// claimUploadStart records that a file is starting, or reports that it was
// cancelled while it waited.
func (s *Service) claimUploadStart(window *uploadWindow, uploadID int) bool {
	s.uploadCancelMu.Lock()
	defer s.uploadCancelMu.Unlock()
	if window.skipped[uploadID] {
		return false
	}
	window.started[uploadID] = true
	return true
}

// skipWaitingUploadLocked marks a not-yet-started file of a running batch.
func (s *Service) skipWaitingUploadLocked(uploadID int) bool {
	for _, window := range s.uploadWindows {
		if uploadID >= window.first && uploadID < window.end && !window.started[uploadID] {
			window.skipped[uploadID] = true
			return true
		}
	}
	return false
}

// uploadAcceptedObserver hears that Telegram holds a file whose local index
// entry is still to be written with the rest of its batch. The file exists and
// must not be sent again; it is no longer uploading.
type uploadAcceptedObserver interface {
	Accepted(id int)
}
