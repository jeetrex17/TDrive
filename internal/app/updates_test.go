package app

import (
	"errors"
	"testing"

	"TDrive/backend/updater"
)

func TestUpdateCleanupDoesNotStartAfterMountStartupFailure(t *testing.T) {
	t.Parallel()

	cleanupCalls := 0
	started := scheduleUpdateCleanup(
		errors.New("mount controller unavailable"),
		func() error {
			cleanupCalls++
			return nil
		},
	)

	if started {
		t.Fatal("update cleanup was scheduled after mount startup failed")
	}
	if cleanupCalls != 0 {
		t.Fatalf("update cleanup calls = %d, want 0", cleanupCalls)
	}
}

func TestStoreUpdatesNeverUseGitHubInstaller(t *testing.T) {
	t.Parallel()
	shell := &updateTestShell{}
	svc := newUpdateService(nil, shell, nil, "2.1.0")
	svc.storeManaged = true
	svc.initUpdater()
	if svc.service != nil || !svc.AppVersion().StoreManaged {
		t.Fatal("Store install initialized the GitHub updater")
	}
	for _, state := range []updater.State{svc.GetUpdateState(), svc.CheckForUpdate()} {
		if state.Phase != updater.PhaseDisabled || state.CurrentVersion != "2.1.0" {
			t.Fatalf("Store update state = %+v", state)
		}
	}
	for _, action := range []func() error{svc.DownloadUpdate, svc.InstallUpdateAndRestart} {
		if err := action(); !errors.Is(err, updater.ErrDisabled) {
			t.Fatalf("Store update action = %v, want disabled", err)
		}
	}
	svc.CancelUpdateDownload()
	svc.finishCleanup(nil)
	svc.OpenUpdatePage()
	if shell.url != "ms-windows-store://pdp/?productid=9PK3XTTDC0C0" || shell.quitCalled {
		t.Fatalf("Store update action opened %q, quit=%v", shell.url, shell.quitCalled)
	}
}

// Native CI runs this executable outside MSIX, so it also exercises Windows'
// no-package result rather than only the injected Store policy case above.
func TestUnpackagedBuildRetainsGitHubUpdates(t *testing.T) {
	t.Parallel()
	svc := newUpdateService(nil, &updateTestShell{}, nil, "2.1.0")
	if svc.AppVersion().StoreManaged {
		t.Fatal("unpackaged test process was detected as Store managed")
	}
}

type updateTestShell struct {
	url        string
	quitCalled bool
}

func (s *updateTestShell) quit()              { s.quitCalled = true }
func (s *updateTestShell) openURL(url string) { s.url = url }
