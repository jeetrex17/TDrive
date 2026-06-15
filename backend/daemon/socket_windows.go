//go:build windows

package daemon

import (
	"fmt"
	"net"
)

func SocketPath() (string, error) {
	return `\\.\pipe\TDrive-daemon`, nil
}

func listenSocket(path string) (net.Listener, error) {
	return nil, fmt.Errorf("windows daemon named-pipe transport is not wired yet")
}

func dialSocket(path string) (net.Conn, error) {
	return nil, fmt.Errorf("windows daemon named-pipe transport is not wired yet")
}

func cleanupSocket(path string) {}
