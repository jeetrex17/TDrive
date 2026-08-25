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

// ensureWindowsWebClient starts the per-user WebDAV dependency when the
// service policy permits it. If the current user cannot request SERVICE_START,
// mapping is still attempted because the network provider can trigger the
// service in the interactive logon session.
func ensureWindowsWebClient(parent context.Context) error {
	if parent == nil {
		return ErrInvalidContext
	}
	ctx, cancel := context.WithTimeout(parent, windowsWebClientStartTimeout)
	defer cancel()

	manager, err := windows.OpenSCManager(nil, nil, windows.SC_MANAGER_CONNECT)
	if err != nil {
		return fmt.Errorf("open service manager: %w", err)
	}
	defer windows.CloseServiceHandle(manager) //nolint:errcheck // best-effort handle cleanup

	serviceName, err := windows.UTF16PtrFromString(windowsWebClientServiceName)
	if err != nil {
		return fmt.Errorf("encode WebClient service name: %w", err)
	}
	service, canStart, err := openWindowsWebClient(manager, serviceName)
	if err != nil {
		return err
	}
	defer windows.CloseServiceHandle(service) //nolint:errcheck // best-effort handle cleanup

	startRequested := false
	for {
		var status windows.SERVICE_STATUS
		if err := windows.QueryServiceStatus(service, &status); err != nil {
			return fmt.Errorf("query WebClient service: %w", err)
		}
		switch status.CurrentState {
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
			if err := windows.StartService(service, 0, nil); err != nil &&
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
			return fmt.Errorf("WebClient service state %d is unavailable", status.CurrentState)
		}
		if err := waitForWindowsService(ctx); err != nil {
			return err
		}
	}
}

func openWindowsWebClient(manager windows.Handle, serviceName *uint16) (windows.Handle, bool, error) {
	service, err := windows.OpenService(
		manager,
		serviceName,
		windows.SERVICE_QUERY_STATUS|windows.SERVICE_START,
	)
	if err == nil {
		return service, true, nil
	}
	if !errors.Is(err, windows.ERROR_ACCESS_DENIED) {
		return 0, false, fmt.Errorf("open WebClient service: %w", err)
	}
	service, err = windows.OpenService(manager, serviceName, windows.SERVICE_QUERY_STATUS)
	if err != nil {
		return 0, false, fmt.Errorf("query WebClient service access: %w", err)
	}
	return service, false, nil
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
