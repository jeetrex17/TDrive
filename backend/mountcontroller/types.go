// Package mountcontroller owns the transactional lifecycle of one read-only
// desktop mount. It keeps the capability-bearing WebDAV endpoint inside the Go
// backend and exposes only safe, OS-facing mount status.
package mountcontroller

import (
	"context"
	"errors"
	"fmt"

	"TDrive/backend/mountfs"
	"TDrive/backend/mountos"
)

const (
	DriveKindPersonal = "personal"
	DriveKindShared   = "shared"
)

const (
	PhaseStopped   Phase = "stopped"
	PhasePreparing Phase = "preparing"
	PhaseAttaching Phase = "attaching"
	PhaseMounted   Phase = "mounted"
	PhaseDetaching Phase = "detaching"
	PhaseFailed    Phase = "failed"
)

const (
	readOnlyMode        = "read-only"
	defaultWindowsDrive = "T:"
)

var (
	ErrInvalidConfiguration = errors.New("mount controller: invalid configuration")
	ErrInvalidContext       = errors.New("mount controller: context is required")
	ErrInvalidDrive         = errors.New("mount controller: invalid drive")
	ErrInvalidWindowsDrive  = errors.New("mount controller: invalid Windows drive")
	ErrConflict             = errors.New("mount controller: conflicting mount")
	ErrNotMounted           = errors.New("mount controller: drive is not mounted")
	ErrStartFailed          = errors.New("mount controller: start failed")
	ErrStopFailed           = errors.New("mount controller: stop failed")
	ErrOpenFailed           = errors.New("mount controller: open failed")
	ErrEndpointUnavailable  = errors.New("mount controller: local endpoint unavailable")
)

// Phase is the externally observable lifecycle phase. Transitional phases are
// intentionally explicit so the GUI can disable duplicate actions without
// guessing from booleans.
type Phase string

// Drive is an immutable projection record pinned for the lifetime of a mount.
// Starting a mount never changes core.Engine.ActiveChannelID.
type Drive struct {
	ID    int64  `json:"id"`
	Title string `json:"title"`
	Kind  string `json:"kind"`
}

// StartOptions contains the only platform-specific user choice in v1.
type StartOptions struct {
	WindowsDrive string `json:"windows_drive,omitempty"`
}

// Status is safe to return through daemon IPC or Wails. It deliberately has
// no endpoint URL, capability token, command line, or opaque attachment data.
type Status struct {
	Phase          Phase  `json:"phase"`
	Running        bool   `json:"running"`
	Mounted        bool   `json:"mounted"`
	DriveID        int64  `json:"drive_id,omitempty"`
	DriveTitle     string `json:"drive_title,omitempty"`
	DriveKind      string `json:"drive_kind,omitempty"`
	Label          string `json:"label,omitempty"`
	Location       string `json:"location,omitempty"`
	AttachmentKind string `json:"attachment_kind,omitempty"`
	Mode           string `json:"mode,omitempty"`
	WindowsDrive   string `json:"windows_drive,omitempty"`
	Error          string `json:"error,omitempty"`
}

// ConflictError reports the safe identity fields that disagree with the
// currently preparing or mounted session.
type ConflictError struct {
	ActiveDriveID         int64
	RequestedDriveID      int64
	ActiveWindowsDrive    string
	RequestedWindowsDrive string
}

func (err *ConflictError) Error() string {
	if err == nil {
		return ErrConflict.Error()
	}
	if err.ActiveDriveID != err.RequestedDriveID {
		return fmt.Sprintf(
			"%s: drive %d is already mounted; stop it before mounting drive %d",
			ErrConflict,
			err.ActiveDriveID,
			err.RequestedDriveID,
		)
	}
	return fmt.Sprintf(
		"%s: drive %d already uses %s; stop it before changing to %s",
		ErrConflict,
		err.ActiveDriveID,
		err.ActiveWindowsDrive,
		err.RequestedWindowsDrive,
	)
}

func (err *ConflictError) Is(target error) bool { return target == ErrConflict }

// ContentLifetime owns resources shared by all opened files in one mount.
type ContentLifetime interface {
	Close()
}

// FilesystemBuilder is the injection boundary for the projection/content
// stack. Production uses mountcontent and mountfs; tests can use a deterministic
// fake without opening SQLite or Telegram.
type FilesystemBuilder interface {
	Build(context.Context, int64, mountfs.Options) (*mountfs.FS, ContentLifetime, error)
}

// EndpointConfig is private-by-convention backend data. Endpoint must never be
// serialized or copied into Status.
type EndpointConfig struct {
	FS           *mountfs.FS
	DriveID      int64
	DriveTitle   string
	WindowsDrive string
}

// EndpointStatus contains the capability URL only long enough to hand it to
// the trusted OS connector.
type EndpointStatus struct {
	Endpoint string
}

// EndpointHealth is capability-free liveness information.
type EndpointHealth struct {
	Running bool
}

// Endpoint abstracts the loopback WebDAV server for deterministic lifecycle
// tests.
type Endpoint interface {
	Start(context.Context, EndpointConfig) (EndpointStatus, error)
	Health() EndpointHealth
	Stop(context.Context) error
}

// Dependencies are validated and retained as immutable interfaces by a
// Controller. SnapshotOptions with a zero TTL selects the mount-specific TTL,
// while the remaining zero fields retain mountfs production defaults.
type Dependencies struct {
	Filesystems     FilesystemBuilder
	Endpoint        Endpoint
	Connector       mountos.Connector
	SnapshotOptions mountfs.Options
}

type operationError struct {
	message string
	kind    error
}

func (err *operationError) Error() string {
	if err == nil {
		return "mount operation failed"
	}
	return err.message
}

func (err *operationError) Is(target error) bool {
	if err == nil {
		return false
	}
	return errors.Is(err.kind, target)
}
