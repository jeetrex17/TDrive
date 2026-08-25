//go:build windows

package mountos

import (
	"context"
	"errors"
	"fmt"
	"time"

	"golang.org/x/sys/windows"
)

const (
	windowsWebClientServiceName  = "WebClient"
	windowsWebClientStartTimeout = 5 * time.Second
	windowsServicePollInterval   = 100 * time.Millisecond
)

// windowsServiceHandle abstracts the open WebClient service handle so the
// state machine in ensureWindowsWebClientService can be driven by a fake in
// tests instead of only ever running against the real Service Control
// Manager (which only exists on Windows and, on Windows Server images such as
// GitHub's windows-latest runner, may not even have WebClient installed).
type windowsServiceHandle interface {
	// State returns the service's current SERVICE_STATUS.CurrentState.
	State() (uint32, error)
	Start() error
	Close()
}

// windowsServiceOpener abstracts locating and opening a service by name.
// canStart reports whether the handle carries SERVICE_START rights.
type windowsServiceOpener interface {
	Open(name string) (handle windowsServiceHandle, canStart bool, err error)
}

// ensureWindowsWebClient starts the per-user WebDAV dependency when the
// service policy permits it. If the current user cannot request SERVICE_START,
// mapping is still attempted because the network provider can trigger the
// service in the interactive logon session.
func ensureWindowsWebClient(parent context.Context) error {
	return ensureWindowsWebClientService(parent, scmServiceOpener{}, waitForWindowsService)
}

// ensureWindowsWebClientService holds the actual state machine, parameterized
// over the SCM access (opener) and the poll delay (wait) so tests can exercise
// every branch deterministically. Behavior is intentionally identical to what
// this function inlined before the opener/handle seam was introduced.
func ensureWindowsWebClientService(
	parent context.Context,
	opener windowsServiceOpener,
	wait func(context.Context) error,
) error {
	if parent == nil {
		return ErrInvalidContext
	}
	ctx, cancel := context.WithTimeout(parent, windowsWebClientStartTimeout)
	defer cancel()

	service, canStart, err := opener.Open(windowsWebClientServiceName)
	if err != nil {
		return err
	}
	defer service.Close()

	startRequested := false
	for {
		state, err := service.State()
		if err != nil {
			return fmt.Errorf("query WebClient service: %w", err)
		}
		switch state {
		case windows.SERVICE_RUNNING:
			return nil
		case windows.SERVICE_STOPPED:
			if !canStart {
				// Standard-user service ACLs may deny SERVICE_START while allowing
				// the WebDAV network provider to trigger the service on demand.
				return nil
			}
			if startRequested {
				return errors.New("WebClient stopped after start request")
			}
			if err := service.Start(); err != nil &&
				!errors.Is(err, windows.ERROR_SERVICE_ALREADY_RUNNING) {
				if errors.Is(err, windows.ERROR_ACCESS_DENIED) {
					return nil
				}
				return fmt.Errorf("start WebClient service: %w", err)
			}
			startRequested = true
		case windows.SERVICE_START_PENDING, windows.SERVICE_STOP_PENDING,
			windows.SERVICE_CONTINUE_PENDING:
			// A bounded poll below observes the stable state.
		default:
			return fmt.Errorf("WebClient service state %d is unavailable", state)
		}
		if err := wait(ctx); err != nil {
			return err
		}
	}
}

// scmServiceOpener is the real windowsServiceOpener, backed by the Windows
// Service Control Manager.
type scmServiceOpener struct{}

func (scmServiceOpener) Open(name string) (windowsServiceHandle, bool, error) {
	manager, err := windows.OpenSCManager(nil, nil, windows.SC_MANAGER_CONNECT)
	if err != nil {
		return nil, false, fmt.Errorf("open service manager: %w", err)
	}
	defer windows.CloseServiceHandle(manager) //nolint:errcheck // best-effort handle cleanup

	serviceName, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return nil, false, fmt.Errorf("encode WebClient service name: %w", err)
	}
	handle, err := windows.OpenService(
		manager,
		serviceName,
		windows.SERVICE_QUERY_STATUS|windows.SERVICE_START,
	)
	if err == nil {
		return scmServiceHandle{handle: handle}, true, nil
	}
	if !errors.Is(err, windows.ERROR_ACCESS_DENIED) {
		return nil, false, fmt.Errorf("open WebClient service: %w", err)
	}
	handle, err = windows.OpenService(manager, serviceName, windows.SERVICE_QUERY_STATUS)
	if err != nil {
		return nil, false, fmt.Errorf("query WebClient service access: %w", err)
	}
	return scmServiceHandle{handle: handle}, false, nil
}

type scmServiceHandle struct {
	handle windows.Handle
}

func (h scmServiceHandle) State() (uint32, error) {
	var status windows.SERVICE_STATUS
	if err := windows.QueryServiceStatus(h.handle, &status); err != nil {
		return 0, err
	}
	return status.CurrentState, nil
}

func (h scmServiceHandle) Start() error {
	return windows.StartService(h.handle, 0, nil)
}

func (h scmServiceHandle) Close() {
	_ = windows.CloseServiceHandle(h.handle)
}

func waitForWindowsService(ctx context.Context) error {
	timer := time.NewTimer(windowsServicePollInterval)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
