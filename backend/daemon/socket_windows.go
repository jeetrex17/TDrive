//go:build windows

package daemon

import (
	"fmt"
	"log/slog"
	"net"
	"time"

	"github.com/Microsoft/go-winio"
	"golang.org/x/sys/windows"
)

const (
	windowsPipeBufferSize  = 64 * 1024
	windowsPipeDialTimeout = 2 * time.Second
)

func SocketPath() (string, error) {
	sid, err := currentUserSID()
	if err != nil {
		return "", err
	}
	return windowsPipePathForSID(sid)
}

func listenSocket(path string) (net.Listener, error) {
	// Never log path (embeds the current user's SID) or descriptor (the SDDL
	// security descriptor string) -- only confirm the outcome.
	descriptor, err := currentUserPipeSecurityDescriptor()
	if err != nil {
		slog.Error("daemon: windows pipe security descriptor setup failed", "error", err)
		return nil, err
	}

	listener, err := winio.ListenPipe(path, &winio.PipeConfig{
		SecurityDescriptor: descriptor,
		InputBufferSize:    windowsPipeBufferSize,
		OutputBufferSize:   windowsPipeBufferSize,
	})
	if err != nil {
		slog.Error("daemon: windows named pipe listen failed", "error", err)
		return nil, fmt.Errorf("daemon socket: listen: %w", err)
	}
	slog.Info("daemon: windows named pipe listening with a per-user restricted ACL")
	return listener, nil
}

func dialSocket(path string) (net.Conn, error) {
	timeout := windowsPipeDialTimeout
	conn, err := winio.DialPipe(path, &timeout)
	if err != nil {
		slog.Debug("daemon: windows named pipe dial failed", "error", err)
		return nil, fmt.Errorf("daemon is not running. Run: tdrive daemon start: %w", err)
	}
	return conn, nil
}

func cleanupSocket(path string) {}

func currentUserPipeSecurityDescriptor() (string, error) {
	sid, err := currentUserSID()
	if err != nil {
		return "", err
	}
	return pipeSecurityDescriptorForSID(sid)
}

func currentUserSID() (string, error) {
	currentUser, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return "", fmt.Errorf("daemon socket: query current user: %w", err)
	}
	if currentUser.User.Sid == nil || !currentUser.User.Sid.IsValid() {
		return "", fmt.Errorf("daemon socket: current user has an invalid SID")
	}

	sid := currentUser.User.Sid.String()
	if sid == "" {
		return "", fmt.Errorf("daemon socket: format current-user SID")
	}
	return sid, nil
}
