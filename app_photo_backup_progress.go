package main

import (
	"context"
	"math"
	"time"

	"TDrive/backend/photobackup"
	fileservice "TDrive/backend/services/file"
)

// One snapshot per app bounds memory independently of library size. The scope
// and generation prevent a cancelled worker from updating another drive/file.
type photoBackupProgressState struct {
	scope      photobackup.Scope
	generation uint64
	name       string
	total      int64
	percent    float64
	lastEvent  time.Time
}

func (a *App) beginPhotoBackupProgress(ctx context.Context, scope photobackup.Scope, name string, size int64) (func(fileservice.BackupUploadProgress), func()) {
	a.photoBackupMu.Lock()
	generation := a.photoBackupProgress.generation + 1
	a.photoBackupProgress = photoBackupProgressState{scope: scope, generation: generation, name: name, total: max(0, size), lastEvent: a.photoBackupProgress.lastEvent}
	a.photoBackupMu.Unlock()
	a.emitPhotoBackupProgress()
	update := func(p fileservice.BackupUploadProgress) {
		if ctx.Err() != nil || math.IsNaN(p.Percent) || math.IsInf(p.Percent, 0) {
			return
		}
		a.photoBackupMu.Lock()
		current := a.photoBackupProgress
		if current.generation != generation {
			a.photoBackupMu.Unlock()
			return
		}
		current.total = max(0, p.BytesTotal)
		current.percent = max(current.percent, min(100, max(0, p.Percent)))
		a.photoBackupProgress = current
		a.photoBackupMu.Unlock()
		a.emitPhotoBackupProgress()
	}
	finish := func() {
		a.photoBackupMu.Lock()
		if a.photoBackupProgress.generation == generation {
			a.photoBackupProgress = photoBackupProgressState{generation: generation + 1, lastEvent: a.photoBackupProgress.lastEvent}
		}
		a.photoBackupMu.Unlock()
		a.emit("photo-backup:state")
	}
	return update, finish
}

func (a *App) emitPhotoBackupProgress() {
	a.photoBackupMu.Lock()
	now := time.Now()
	emit := now.Sub(a.photoBackupProgress.lastEvent) >= 250*time.Millisecond
	if emit {
		a.photoBackupProgress.lastEvent = now
	}
	a.photoBackupMu.Unlock()
	// Never emit under the worker mutex: event delivery may immediately read state.
	if emit {
		a.emit("photo-backup:state")
	}
}

func (a *App) withPhotoBackupProgress(state PhotoBackupState, scope photobackup.Scope) PhotoBackupState {
	a.photoBackupMu.Lock()
	current := a.photoBackupProgress
	a.photoBackupMu.Unlock()
	if current.scope != scope {
		return state
	}
	state.Status.CurrentFile = current.name
	state.Status.CurrentFileBytesTotal = current.total
	state.Status.CurrentFilePercent = current.percent
	state.Status.CurrentFileBytesDone = int64(float64(current.total) * current.percent / 100)
	return state
}
