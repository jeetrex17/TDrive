package app

import (
	"context"
	"fmt"
	"math"
	"time"

	"TDrive/backend/photobackup"
	fileservice "TDrive/backend/services/file"
)

// Fixed slots bound progress independently of library size. Per-item generations
// reject callbacks after completion without suppressing another active upload.
type photoBackupProgressState struct {
	scope      photobackup.Scope
	generation uint64
	items      [photoBackupConcurrency]photoBackupProgressItem
	lastEvent  time.Time
}

type photoBackupProgressItem struct {
	generation uint64
	name       string
	total      int64
	percent    float64
}

func (a *App) beginPhotoBackupProgress(ctx context.Context, scope photobackup.Scope, name string, size int64) (func(fileservice.BackupUploadProgress), func()) {
	a.photoBackupMu.Lock()
	p := &a.photoBackupProgress
	if p.scope != scope {
		p.items = [photoBackupConcurrency]photoBackupProgressItem{}
		p.scope = scope
	}
	slot := -1
	for i, item := range p.items {
		if item.generation == 0 {
			slot = i
			break
		}
	}
	if slot < 0 {
		a.photoBackupMu.Unlock()
		return func(fileservice.BackupUploadProgress) {}, func() {}
	}
	p.generation++
	generation := p.generation
	p.items[slot] = photoBackupProgressItem{generation: generation, name: name, total: max(0, size)}
	a.photoBackupMu.Unlock()
	a.emitPhotoBackupProgress()
	update := func(p fileservice.BackupUploadProgress) {
		if ctx.Err() != nil || math.IsNaN(p.Percent) || math.IsInf(p.Percent, 0) {
			return
		}
		a.photoBackupMu.Lock()
		current := a.photoBackupProgress.items[slot]
		if current.generation != generation {
			a.photoBackupMu.Unlock()
			return
		}
		current.total = max(0, p.BytesTotal)
		current.percent = max(current.percent, min(100, max(0, p.Percent)))
		a.photoBackupProgress.items[slot] = current
		a.photoBackupMu.Unlock()
		a.emitPhotoBackupProgress()
	}
	finish := func() {
		a.photoBackupMu.Lock()
		if a.photoBackupProgress.items[slot].generation == generation {
			a.photoBackupProgress.items[slot] = photoBackupProgressItem{}
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
	count := 0
	var total, done int64
	for _, item := range current.items {
		if item.generation == 0 {
			continue
		}
		count++
		state.Status.CurrentFile = item.name
		total += item.total
		done += int64(float64(item.total) * item.percent / 100)
	}
	if count > 1 {
		state.Status.CurrentFile = fmt.Sprintf("%d files", count)
	}
	state.Status.CurrentFileBytesTotal = total
	state.Status.CurrentFileBytesDone = done
	if total > 0 {
		state.Status.CurrentFilePercent = float64(done) / float64(total) * 100
	}
	return state
}
