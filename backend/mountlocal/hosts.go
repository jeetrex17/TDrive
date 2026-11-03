// Package mountlocal defines the single persistent hostname used by macOS mounts.
package mountlocal

import (
	"errors"
	"strings"
)

// Hostname is fixed, never derived from a drive name or other user input.
const Hostname = "TDrive.localhost"

var (
	ErrHostsConflict = errors.New("TDrive mount: TDrive.localhost has a conflicting hosts entry; remove the conflicting mapping before mounting")
	ErrHostsUnsafe   = errors.New("TDrive mount: /private/etc/hosts must be a root-owned regular file without group or world write permission")
	ErrSetup         = errors.New("TDrive mount: administrator setup was canceled or failed; retry Mount and approve the one-time local hostname setup")
	ErrResolution    = errors.New("TDrive mount: TDrive.localhost must resolve only to 127.0.0.1")
	ErrProxy         = errors.New("TDrive mount: the local drive must connect directly; bypass TDrive.localhost in macOS proxy settings and retry")
)

func hostsMapping(data []byte) (bool, error) {
	found := false
	for line := range strings.SplitSeq(string(data), "\n") {
		line, _, _ = strings.Cut(line, "#")
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		for _, alias := range fields[1:] {
			if !strings.EqualFold(strings.TrimSuffix(alias, "."), Hostname) {
				continue
			}
			if fields[0] != "127.0.0.1" {
				return false, ErrHostsConflict
			}
			// A trailing-dot alias alone does not install the exact requested name.
			if !strings.HasSuffix(alias, ".") {
				found = true
			}
		}
	}
	return found, nil
}
