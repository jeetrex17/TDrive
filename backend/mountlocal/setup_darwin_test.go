//go:build darwin && !ios

package mountlocal

import (
	"context"
	"errors"
	"net"
	"testing"
)

func TestPrepareHostnameSafety(t *testing.T) {
	for _, tc := range []struct {
		name, initial, installed string
		installErr, want         error
		address                  string
		proxy                    bool
		installs                 int
	}{
		{name: "already installed", initial: "127.0.0.1 TDrive.localhost", address: "127.0.0.1"},
		{name: "install once", initial: "127.0.0.1 localhost", installed: "127.0.0.1 TDrive.localhost", address: "127.0.0.1", installs: 1},
		{name: "conflict", initial: "192.0.2.1 TDrive.localhost", want: ErrHostsConflict},
		{name: "canceled admin", installErr: context.Canceled, want: ErrSetup, installs: 1},
		{name: "installer did not map", want: ErrSetup, installs: 1},
		{name: "resolver mismatch", initial: "127.0.0.1 TDrive.localhost", address: "192.0.2.1", want: ErrResolution},
		{name: "ipv6 resolver", initial: "127.0.0.1 TDrive.localhost", address: "::1", want: ErrResolution},
		{name: "proxy", initial: "127.0.0.1 TDrive.localhost", address: "127.0.0.1", proxy: true, want: ErrProxy},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			deps := setupDependencies{
				readHosts: func() ([]byte, error) {
					if calls > 0 {
						return []byte(tc.installed), nil
					}
					return []byte(tc.initial), nil
				},
				install: func(context.Context) error { calls++; return tc.installErr },
				resolve: func(context.Context) ([]net.IPAddr, error) { return []net.IPAddr{{IP: net.ParseIP(tc.address)}}, nil },
				direct:  func(string) bool { return !tc.proxy },
			}
			err := prepare(t.Context(), "12345", deps)
			if !errors.Is(err, tc.want) || calls != tc.installs {
				t.Fatalf("prepare = %v; installs=%d; want %v/%d", err, calls, tc.want, tc.installs)
			}
		})
	}
}
