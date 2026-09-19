package main

import (
	"runtime"
	"sync"
	"time"
)

const (
	iosPhotoBackupBackgroundWindow     = 20 * time.Second
	androidPhotoBackupBackgroundWindow = 5*time.Hour + 50*time.Minute
)

type photoBackupBackgroundState struct {
	mu         sync.Mutex
	armed      bool
	background bool
	leaseStart time.Time
	timer      *time.Timer
	generation uint64
	now        func() time.Time
	after      func(time.Duration, func()) *time.Timer
}

// SetPhotoBackupBackgroundLease records a lease only after the native host has
// acquired its execution grant. Repeated calls cannot extend Android's
// cumulative dataSync budget.
func (a *App) SetPhotoBackupBackgroundLease(active bool) {
	if a == nil {
		return
	}
	s := &a.photoBackupBackground
	s.mu.Lock()
	if !active {
		s.generation++
		s.armed = false
		// Android's six-hour allowance is cumulative. Dropping and reacquiring
		// our lease must not manufacture a fresh platform budget.
		if runtime.GOOS != "android" {
			s.leaseStart = time.Time{}
		}
		s.stopTimerLocked()
		background := s.background
		s.mu.Unlock()
		if background {
			a.stopPhotoBackup()
		}
		return
	}
	// A lease can only be armed while the UI is visible, when the platform is
	// allowed to grant it. Repeated refreshes are idempotent and cannot
	// invalidate an already scheduled deadline.
	if s.armed || s.background {
		s.mu.Unlock()
		return
	}
	s.generation++
	s.armed = true
	if runtime.GOOS == "android" && s.leaseStart.IsZero() {
		s.leaseStart = s.clock()()
	}
	s.mu.Unlock()
}

// enterPhotoBackupBackground fails closed without an acquired native lease.
// Its Go timer is a conservative fallback because suspended JavaScript cannot
// reliably deliver the native expiry event. The OS may end execution earlier.
func (a *App) enterPhotoBackupBackground(platform string) bool {
	if a == nil {
		return false
	}
	s := &a.photoBackupBackground
	s.mu.Lock()
	s.generation++
	generation := s.generation
	s.background = true
	s.stopTimerLocked()
	if !s.armed {
		s.mu.Unlock()
		return false
	}
	now := s.clock()()
	deadline := now.Add(iosPhotoBackupBackgroundWindow)
	if platform == "android" {
		if s.leaseStart.IsZero() {
			s.leaseStart = now
		}
		deadline = s.leaseStart.Add(androidPhotoBackupBackgroundWindow)
	}
	delay := deadline.Sub(now)
	if delay <= 0 {
		s.armed = false
		s.mu.Unlock()
		return false
	}
	s.timer = s.scheduler()(delay, func() {
		s.mu.Lock()
		expired := s.background && s.generation == generation
		if !expired {
			s.mu.Unlock()
			return
		}
		s.armed = false
		s.timer = nil
		s.mu.Unlock()
		if expired {
			a.stopPhotoBackup()
		}
	})
	s.mu.Unlock()
	return true
}

func (a *App) leavePhotoBackupBackground() {
	if a == nil {
		return
	}
	s := &a.photoBackupBackground
	s.mu.Lock()
	s.generation++
	s.background = false
	s.stopTimerLocked()
	s.mu.Unlock()
}

func (a *App) photoBackupMayStartNextJob() bool {
	if a == nil {
		return false
	}
	s := &a.photoBackupBackground
	s.mu.Lock()
	defer s.mu.Unlock()
	return !s.background
}

func (s *photoBackupBackgroundState) clock() func() time.Time {
	if s.now != nil {
		return s.now
	}
	return time.Now
}
func (s *photoBackupBackgroundState) scheduler() func(time.Duration, func()) *time.Timer {
	if s.after != nil {
		return s.after
	}
	return time.AfterFunc
}
func (s *photoBackupBackgroundState) stopTimerLocked() {
	if s.timer != nil {
		s.timer.Stop()
	}
	s.timer = nil
}
