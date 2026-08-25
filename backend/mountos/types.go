// Package mountos attaches TDrive's loopback WebDAV endpoint to the host OS.
//
// Attachments are deliberately opaque. Callers may retain them, display their
// safe location, and pass them back to the Connector that created them. The
// endpoint capability is never exposed through the public attachment API.
package mountos

import (
	"context"
	"errors"
)

const (
	KindDarwin  = "darwin"
	KindWindows = "windows"
	KindLinux   = "linux"
)

var (
	ErrInvalidContext           = errors.New("mount OS: invalid context")
	ErrInvalidEndpoint          = errors.New("mount OS: invalid endpoint")
	ErrInvalidLabel             = errors.New("mount OS: invalid label")
	ErrInvalidDrive             = errors.New("mount OS: invalid Windows drive")
	ErrInvalidAttachment        = errors.New("mount OS: invalid attachment")
	ErrDriveOccupied            = errors.New("mount OS: Windows drive is occupied")
	ErrWindowsWebDAVUnavailable = errors.New("mount OS: Windows WebDAV is unavailable")
	ErrLinuxDesktopUnavailable  = errors.New("mount OS: Linux desktop mounting is unavailable")
	ErrLinuxWebDAVUnavailable   = errors.New("mount OS: Linux WebDAV mounting is unavailable")
	ErrAttachFailed             = errors.New("mount OS: attach failed")
	ErrDetachFailed             = errors.New("mount OS: detach failed")
	ErrOpenFailed               = errors.New("mount OS: open failed")
	ErrVerificationFailed       = errors.New("mount OS: attachment verification failed")
	ErrAttachmentChanged        = errors.New("mount OS: attachment ownership changed")
	ErrNotSupported             = errors.New("mount OS: platform is not supported")
)

// Config describes an attachment without granting the OS connector any
// authority beyond the validated loopback endpoint and requested drive.
type Config struct {
	Endpoint     string
	Label        string
	WindowsDrive string
}

// Attachment is an opaque receipt for an OS attachment. Its zero value is not
// detachable. The safe Kind and Location accessors do not expose the endpoint.
type Attachment struct {
	owner    *ownerMarker
	id       uint64
	kind     string
	location string
}

type ownerMarker struct {
	_ byte
}

// NewAttachment creates a display-only attachment for alternate Connector
// implementations, primarily deterministic test doubles. The platform
// connector returned by New rejects it for Detach and Open because it has no
// ownership receipt.
func NewAttachment(kind, location string) Attachment {
	return Attachment{kind: kind, location: location}
}

// Kind returns the platform kind without exposing attachment credentials.
func (a Attachment) Kind() string { return a.kind }

// Location returns the safe Finder, Explorer, or file-manager location.
func (a Attachment) Location() string { return a.location }

// Connector owns the lifecycle of one or more OS attachments. Attach confirms
// the OS attachment before returning. Detach confirms both ownership and
// absence before it succeeds.
type Connector interface {
	Attach(context.Context, Config) (Attachment, error)
	Detach(context.Context, Attachment) error
	Open(context.Context, Attachment) error
}

// New returns the connector for the current operating system.
func New() Connector { return newPlatformConnector() }

type validatedConfig struct {
	endpoint string
	label    string
	drive    string
}
