package main

import (
	"context"
	"testing"
	"time"
)

func TestPhotoBackupBackgroundFailsClosedWithoutLease(t *testing.T) {
	a := &App{}
	if a.enterPhotoBackupBackground("ios") {
		t.Fatal("background execution must require a native lease")
	}
	if a.photoBackupMayStartNextJob() {
		t.Fatal("background worker must not begin another item")
	}
}

func TestPhotoBackupDeadlineCancelsActiveWorker(t *testing.T) {
	a := &App{photoBackupWaiters: make(map[string]chan photoBackupMaterialization)}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	close(done)
	a.photoBackupCancel = cancel
	a.photoBackupDone = done
	var expire func()
	a.photoBackupBackground.after = func(_ time.Duration, fn func()) *time.Timer {
		expire = fn
		return time.AfterFunc(time.Hour, func() {})
	}
	a.SetPhotoBackupBackgroundLease(true)
	a.enterPhotoBackupBackground("ios")
	expire()
	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("expired lease did not cancel the active worker")
	}
}

func TestPhotoBackupBackgroundLeaseSchedulesExpiry(t *testing.T) {
	a := &App{}
	expiryScheduled := make(chan time.Duration, 1)
	a.photoBackupBackground.now = func() time.Time { return time.Unix(100, 0) }
	a.photoBackupBackground.after = func(delay time.Duration, fn func()) *time.Timer {
		expiryScheduled <- delay
		return time.AfterFunc(time.Hour, fn)
	}
	a.SetPhotoBackupBackgroundLease(true)
	if !a.enterPhotoBackupBackground("ios") {
		t.Fatal("armed lease should permit bounded continuation")
	}
	select {
	case delay := <-expiryScheduled:
		if delay != iosPhotoBackupBackgroundWindow {
			t.Fatalf("deadline = %s", delay)
		}
	case <-time.After(time.Second):
		t.Fatal("expiry was not scheduled")
	}
	a.leavePhotoBackupBackground()
}

func TestPhotoBackupForegroundCancelsDeadline(t *testing.T) {
	a := &App{}
	a.SetPhotoBackupBackgroundLease(true)
	if !a.enterPhotoBackupBackground("android") {
		t.Fatal("armed lease should continue")
	}
	a.leavePhotoBackupBackground()
	if !a.photoBackupMayStartNextJob() {
		t.Fatal("foreground queue should continue")
	}
	if a.photoBackupBackground.timer != nil {
		t.Fatal("foregrounding must cancel deadline")
	}
}

func TestStalePhotoBackupDeadlineCannotCancelRenewedLease(t *testing.T) {
	a := &App{}
	var callbacks []func()
	a.photoBackupBackground.after = func(_ time.Duration, fn func()) *time.Timer {
		callbacks = append(callbacks, fn)
		return time.AfterFunc(time.Hour, func() {})
	}
	a.SetPhotoBackupBackgroundLease(true)
	a.enterPhotoBackupBackground("ios")
	a.leavePhotoBackupBackground()
	a.SetPhotoBackupBackgroundLease(true)
	a.enterPhotoBackupBackground("ios")
	callbacks[0]()
	if !a.photoBackupBackground.armed {
		t.Fatal("stale deadline cleared the renewed lease")
	}
	a.leavePhotoBackupBackground()
}

func TestRepeatedPhotoBackupArmKeepsExistingDeadlineValid(t *testing.T) {
	a := &App{}
	var expire func()
	a.photoBackupBackground.after = func(_ time.Duration, fn func()) *time.Timer {
		expire = fn
		return time.AfterFunc(time.Hour, func() {})
	}
	a.SetPhotoBackupBackgroundLease(true)
	a.enterPhotoBackupBackground("ios")
	a.SetPhotoBackupBackgroundLease(true)
	expire()
	if a.photoBackupBackground.armed {
		t.Fatal("repeated arm invalidated the existing expiry")
	}
}
