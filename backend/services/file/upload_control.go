package file

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"TDrive/backend/tgclient"
)

// Interactive uploads can be paused and have files cancelled one by one.
// Neither touches a send already under way: an ordinary upload cannot stop
// part way and continue later, so pausing holds back files that have not
// started, and in-flight files finish (or are cancelled one by one).

// errUploadCanceled marks a file the user cancelled on its own, before or
// during its send, while the rest of its batch carried on. The file is
// reported canceled, but it is not a failure of the batch.
var errUploadCanceled = fmt.Errorf("upload canceled: %w", context.Canceled)

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

// awaitUploadTurn waits until a file may start: an upload slot is free and,
// for a pausable batch, uploads are not paused. A pause that arrives while the
// file waits for its slot holds it too, and the slot goes back meanwhile, so a
// paused batch starts nothing new and keeps no slot it is not using.
func (s *Service) awaitUploadTurn(ctx context.Context, pausable bool) (func(), error) {
	for {
		if pausable {
			if err := s.waitWhileUploadsPaused(ctx); err != nil {
				return nil, err
			}
		}
		slotStarted := time.Now()
		release, err := s.acquireUploadSlot(ctx)
		reportUploadTiming(ctx, "slot_wait", slotStarted)
		if err != nil || !pausable || !s.UploadsPaused() {
			return release, err
		}
		release()
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

// startUpload marks a file as started and registers its cancel handle in one
// step, so CancelUpload finds every file of a running batch either waiting,
// and skips it, or running, and cancels it. It reports false for a file the
// user cancelled while it waited; the returned func unregisters a started one
// and releases its context.
func (s *Service) startUpload(ctx context.Context, window *uploadWindow, uploadID int) (context.Context, func(), bool) {
	s.uploadCancelMu.Lock()
	defer s.uploadCancelMu.Unlock()
	if window.skipped[uploadID] {
		return nil, nil, false
	}
	window.started[uploadID] = true
	uploadCtx, cancel := context.WithCancelCause(ctx)
	if s.uploadCancels == nil {
		s.uploadCancels = make(map[int]context.CancelCauseFunc)
	}
	s.uploadCancels[uploadID] = cancel
	return uploadCtx, func() {
		s.uploadCancelMu.Lock()
		delete(s.uploadCancels, uploadID)
		s.uploadCancelMu.Unlock()
		cancel(nil)
	}, true
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

// canceledOnItsOwn reports whether a started file stopped only because the
// user cancelled it while its batch carried on. A send whose outcome is
// unknown may already be in the chat, and an interrupted resumable upload
// keeps a recovery job, so both remain failures that need attention.
func canceledOnItsOwn(batch, file context.Context, err error) bool {
	return batch.Err() == nil && errors.Is(context.Cause(file), errUploadCanceled) &&
		!errors.Is(err, tgclient.ErrSendOutcomeUnknown) && !errors.Is(err, errUploadResumable)
}

// uploadAcceptedObserver hears that Telegram holds a file whose local index
// entry is still to be written with the rest of its batch. The file exists and
// must not be sent again; it is no longer uploading.
type uploadAcceptedObserver interface {
	Accepted(id int)
}
