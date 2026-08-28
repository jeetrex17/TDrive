package daemon

import (
	"strings"
	"testing"
)

// A daemon left running from an older install must reject a newer CLI by
// version rather than by unknown command, so the fix is "restart the
// daemon" rather than a puzzle.
func TestValidateRequestRejectsForeignProtocolVersions(t *testing.T) {
	for _, version := range []int{0, ProtocolVersion - 1, ProtocolVersion + 1} {
		err := validateRequest(Request{Version: version, Command: CommandStatus})
		if err == nil {
			t.Fatalf("protocol version %d was accepted, want rejection", version)
		}
		if !strings.Contains(err.Error(), "unsupported daemon protocol version") {
			t.Fatalf("version %d error = %q, want a version error", version, err)
		}
	}
	if err := validateRequest(Request{Version: ProtocolVersion, Command: CommandStatus}); err != nil {
		t.Fatalf("current protocol version rejected: %v", err)
	}
}

// The personal-drive commands are what made the current version incompatible
// with v1.7.0's daemon; they must ship inside the bumped version.
func TestPersonalDriveCommandsRequireCurrentProtocolVersion(t *testing.T) {
	if ProtocolVersion < 2 {
		t.Fatalf("ProtocolVersion = %d, want >= 2 now that personal-drive commands exist", ProtocolVersion)
	}
	for _, command := range []string{CommandPersonalDrivePrepare, CommandPersonalDriveSelect, CommandPersonalDriveCreate} {
		if err := validateRequest(Request{Version: ProtocolVersion, Command: command}); err != nil {
			t.Fatalf("command %q rejected at current version: %v", command, err)
		}
	}
}
