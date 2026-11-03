//go:build darwin && !ios && !cgo

package mountlocal

import (
	"context"
	"net"
)

// Without CFNetwork we cannot establish the native WebDAV proxy route safely.
func directProxyRoute(string) bool { return false }

const nativeAvailable = false

func nativeResolve(context.Context) ([]net.IPAddr, error) { return nil, ErrResolution }
