package daemon

import (
	"fmt"
	"log/slog"
	"strconv"
	"strings"
)

const (
	securityIdentifierRevision     = 1
	maxSecurityIdentifierAuthority = 1<<48 - 1
	maxSIDSubAuthorities           = 15
	windowsNamedPipePrefix         = `\\.\pipe\TDrive-daemon-`
)

func windowsPipePathForSID(sid string) (string, error) {
	if err := validateSIDString(sid); err != nil {
		return "", fmt.Errorf("daemon socket: invalid current-user SID: %w", err)
	}
	return windowsNamedPipePrefix + sid, nil
}

// pipeSecurityDescriptorForSID returns a protected DACL that grants access
// only to the supplied user SID. Keeping the formatter platform-neutral makes
// the security boundary independently testable on every development OS.
func pipeSecurityDescriptorForSID(sid string) (string, error) {
	if err := validateSIDString(sid); err != nil {
		return "", fmt.Errorf("daemon socket: invalid current-user SID: %w", err)
	}
	return "D:P(A;;GA;;;" + sid + ")", nil
}

// validateSIDString never logs sid itself, only that validation failed --
// a Windows SID identifies the local user/machine account.
func validateSIDString(sid string) error {
	parts := strings.Split(sid, "-")
	if len(parts) < 4 || parts[0] != "S" {
		slog.Warn("daemon: SID validation failed, malformed")
		return fmt.Errorf("malformed SID")
	}

	revision, err := parseSIDComponent(parts[1], 8)
	if err != nil || revision != securityIdentifierRevision {
		return fmt.Errorf("unsupported revision")
	}
	if _, err := parseSIDComponent(parts[2], 48); err != nil {
		return fmt.Errorf("invalid identifier authority")
	}

	subAuthorities := parts[3:]
	if len(subAuthorities) > maxSIDSubAuthorities {
		return fmt.Errorf("too many sub-authorities")
	}
	for _, component := range subAuthorities {
		if _, err := parseSIDComponent(component, 32); err != nil {
			return fmt.Errorf("invalid sub-authority")
		}
	}
	return nil
}

func parseSIDComponent(component string, bitSize int) (uint64, error) {
	if component == "" {
		return 0, fmt.Errorf("empty component")
	}
	for _, char := range component {
		if char < '0' || char > '9' {
			return 0, fmt.Errorf("non-decimal component")
		}
	}
	value, err := strconv.ParseUint(component, 10, bitSize)
	if err != nil {
		return 0, err
	}
	if bitSize == 48 && value > maxSecurityIdentifierAuthority {
		return 0, fmt.Errorf("identifier authority overflow")
	}
	return value, nil
}
