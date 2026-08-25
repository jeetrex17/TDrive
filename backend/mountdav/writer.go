package mountdav

import (
	"context"
	"errors"
	"io"
)

// WriteCoordinator is the narrow protocol-to-domain boundary for a writable
// mount. Implementations must make precondition checks and the final metadata
// commit atomic; mountdav deliberately does not emulate writes through its
// immutable read filesystem.
type WriteCoordinator interface {
	Put(ctx context.Context, request PutRequest, body io.Reader) (MutationResult, error)
	Mkdir(ctx context.Context, request MkdirRequest) (MutationResult, error)
	Move(ctx context.Context, request MoveRequest) (MutationResult, error)
	Delete(ctx context.Context, request DeleteRequest) (MutationResult, error)
}

type PutRequest struct {
	OperationID   string
	Path          string
	ContentLength int64
	ContentType   string
	Conditions    MutationConditions
}

type MkdirRequest struct {
	OperationID string
	Path        string
	Conditions  MutationConditions
}

type MoveRequest struct {
	OperationID     string
	SourcePath      string
	DestinationPath string
	Overwrite       bool
	Conditions      MutationConditions
}

type DeleteRequest struct {
	OperationID string
	Path        string
	Conditions  MutationConditions
}

// MutationResult describes the remotely committed state. Created controls the
// RFC status for PUT and MOVE; ETag, when present, must be a strong entity tag.
type MutationResult struct {
	Created bool
	ETag    string
}

type EntityTag struct {
	Weak   bool
	Opaque string
}

type ETagConditions struct {
	Present bool
	Any     bool
	Tags    []EntityTag
}

// DAVConditionList preserves the OR-of-AND structure of the WebDAV If header.
// The coordinator evaluates ETag terms atomically with its revision commit;
// mountdav validates and confirms the lock-token terms before invocation.
type DAVConditionList struct {
	ResourcePath string
	Conditions   []DAVCondition
}

type DAVCondition struct {
	Not       bool
	LockToken string
	ETag      *EntityTag
}

type MutationConditions struct {
	// IfMatch, IfNoneMatch, and DAVIf must be evaluated against the same
	// revision that the coordinator commits. Within DAVIf, a lock-token term
	// is true exactly when that token is present in LockTokens; lists are ORed
	// and the conditions inside one list are ANDed. Lock tokens are ephemeral
	// capabilities and must never be logged, persisted, or returned in errors.
	IfMatch     ETagConditions
	IfNoneMatch ETagConditions
	DAVIf       []DAVConditionList
	LockTokens  []string
}

var (
	ErrWriteInvalid             = errors.New("mountdav: invalid write request")
	ErrWriteForbidden           = errors.New("mountdav: write forbidden")
	ErrWriteNotFound            = errors.New("mountdav: write target not found")
	ErrWriteConflict            = errors.New("mountdav: write conflict")
	ErrWritePreconditionFailed  = errors.New("mountdav: write precondition failed")
	ErrWriteTooLarge            = errors.New("mountdav: write payload too large")
	ErrWriteLocked              = errors.New("mountdav: write target locked")
	ErrWriteInsufficientStorage = errors.New("mountdav: insufficient write storage")
	ErrWriteUnavailable         = errors.New("mountdav: write service unavailable")
)
