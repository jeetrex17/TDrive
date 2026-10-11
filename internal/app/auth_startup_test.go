package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"testing/synctest"
	"time"

	"TDrive/backend/core"
	"TDrive/backend/datadir"
	"TDrive/backend/processlock"

	"github.com/wailsapp/wails/v3/pkg/application"
)

func TestBackendReadinessWaitsForStartupPublication(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		app := NewApp("test")
		result := make(chan error, 1)
		go func() { result <- app.WaitForBackendReady(t.Context()) }()
		synctest.Wait()
		select {
		case err := <-result:
			t.Fatalf("readiness returned before startup: %v", err)
		default:
		}
		app.engine = &core.Engine{}
		app.finishBackendStartup(nil)
		if err := <-result; err != nil {
			t.Fatalf("readiness after publication: %v", err)
		}
		if err := app.WaitForBackendReady(t.Context()); err != nil {
			t.Fatalf("subsequent readiness: %v", err)
		}
	})
}

func TestBackendReadinessPropagatesStartupFailure(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		app := NewApp("test")
		result := make(chan error, 1)
		go func() { result <- app.WaitForBackendReady(t.Context()) }()
		synctest.Wait()
		failure := errors.New("initialize database: permission denied")
		app.finishBackendStartup(failure)
		if err := <-result; !errors.Is(err, failure) {
			t.Fatalf("readiness error = %v, want startup failure", err)
		}
		if err := app.WaitForBackendReady(t.Context()); !errors.Is(err, failure) {
			t.Fatalf("subsequent readiness error = %v, want startup failure", err)
		}
	})
}

func TestPersonalDrivePreparationCanCancelWhileBackendStarts(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		app := NewApp("test")
		ctx, cancel := context.WithCancel(t.Context())
		result := make(chan error, 1)
		go func() {
			_, err := app.drives.PreparePersonalDrive(ctx)
			result <- err
		}()
		synctest.Wait()
		select {
		case err := <-result:
			t.Fatalf("saved-drive preparation returned before readiness: %v", err)
		default:
		}
		cancel()
		if err := <-result; !errors.Is(err, context.Canceled) {
			t.Fatalf("preparation error = %v, want cancellation", err)
		}
	})
}

func TestBackendShutdownUnblocksReadiness(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		app := NewApp("test")
		result := make(chan error, 1)
		go func() { result <- app.WaitForBackendReady(t.Context()) }()
		synctest.Wait()
		if err := app.ServiceShutdown(); err != nil {
			t.Fatalf("shutdown: %v", err)
		}
		if err := <-result; !errors.Is(err, context.Canceled) {
			t.Fatalf("readiness after shutdown = %v, want cancellation", err)
		}
	})
}

func TestBackendStartupLockFailureReleasesReadiness(t *testing.T) {
	datadir.Set(t.TempDir())
	t.Cleanup(func() { datadir.Set("") })
	lock, err := processlock.Acquire("test")
	if err != nil {
		t.Fatalf("acquire existing owner: %v", err)
	}
	t.Cleanup(func() {
		if err := lock.Release(); err != nil {
			t.Errorf("release existing owner: %v", err)
		}
	})
	app := NewApp("test")
	if err := app.ServiceStartup(t.Context(), application.ServiceOptions{}); err != nil {
		t.Fatalf("startup: %v", err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	if err := app.WaitForBackendReady(ctx); !errors.Is(err, processlock.ErrAlreadyRunning) {
		t.Fatalf("readiness = %v, want existing backend owner", err)
	}
}

func TestBackendConstructionFailureReleasesReadinessAndLock(t *testing.T) {
	datadir.Set(t.TempDir())
	t.Cleanup(func() { datadir.Set("") })
	cacheBase := filepath.Join(t.TempDir(), "cache-blocker")
	if err := os.WriteFile(cacheBase, []byte("not a directory"), 0o600); err != nil {
		t.Fatalf("write cache blocker: %v", err)
	}
	datadir.SetCache(cacheBase)
	t.Cleanup(func() { datadir.SetCache("") })
	app := NewApp("test")
	if err := app.ServiceStartup(t.Context(), application.ServiceOptions{}); err != nil {
		t.Fatalf("startup: %v", err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	err := app.WaitForBackendReady(ctx)
	if err == nil || errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("readiness = %v, want backend construction failure", err)
	}
	if _, err := app.drives.PreparePersonalDrive(ctx); !errors.Is(err, app.backendStartupErr) {
		t.Fatalf("saved-drive error = %v, want startup cause", err)
	}
	lock, err := processlock.Acquire("replacement")
	if err != nil {
		t.Fatalf("startup retained backend lock: %v", err)
	}
	if err := lock.Release(); err != nil {
		t.Fatalf("release replacement owner: %v", err)
	}
}

func TestLoginSuccessEnablesAuthenticatedBackgroundWork(t *testing.T) {
	app := &App{}
	sink := runtimeEventSink{app: app}

	sink.Emit("login-code-required")
	if app.authReady.Load() {
		t.Fatal("background work became ready before login succeeded")
	}

	sink.Emit("login-success", true)
	if !app.authReady.Load() {
		t.Fatal("background work did not become ready after login succeeded")
	}
}

func TestCheckLoginStatusReusesVerifiedProcessState(t *testing.T) {
	app := &App{}
	app.authReady.Store(true)
	if !app.CheckLoginStatus() {
		t.Fatal("login status opened a second client despite verified process state")
	}
}
