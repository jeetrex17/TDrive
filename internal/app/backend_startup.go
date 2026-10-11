package app

import (
	"context"
	"runtime"
)

// WaitForBackendReady waits for backend construction, not just availability of
// the Wails bridge or a Telegram login event. Wails supplies the call context.
func (a *App) WaitForBackendReady(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if a.backendStartupDone != nil {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-a.backendStartupDone:
		}
	}
	a.backendStartupMu.Lock()
	defer a.backendStartupMu.Unlock()
	if a.backendStopped {
		return context.Canceled
	}
	if a.backendStartupErr != nil {
		return a.backendStartupErr
	}
	if a.engine == nil {
		return errBackendUnavailable
	}
	return ctx.Err()
}

func (a *App) finishBackendStartup(err error) {
	a.backendStartupMu.Lock()
	if a.backendStartupComplete {
		a.backendStartupMu.Unlock()
		return
	}
	a.backendStartupComplete = true
	a.backendStartupErr = err
	if a.backendStartupDone != nil {
		close(a.backendStartupDone)
	}
	a.backendStartupMu.Unlock()
	if err == nil {
		a.startAuthenticatedWork()
	}
}

// A concurrent login can complete before engine publication. Defer its
// dependent work to completion rather than block the event callback or leave a
// goroutine waiting if startup fails. Later login checks retain their usual
// resume behavior.
func (a *App) startAuthenticatedWork() {
	a.backendStartupMu.Lock()
	if !a.authReady.Load() || a.backendStopped || a.backendStartupErr != nil ||
		(a.backendStartupDone != nil && !a.backendStartupComplete) || a.engine == nil {
		a.backendStartupMu.Unlock()
		return
	}
	engine := a.engine
	a.backendStartupMu.Unlock()
	engine.ResumeLiveSync()
	if runtime.GOOS != "ios" && runtime.GOOS != "android" {
		go func() { _ = a.startPhotoBackup() }()
	}
}
