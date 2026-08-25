// Package mountadapter connects protocol-level mounted-drive mutations to the
// durable Telegram write coordinator. It is intentionally a top-level adapter:
// lower-level projection, mount, and service packages must not import it.
package mountadapter

import (
	"context"
	"io"

	"TDrive/backend/mountfs"
	"TDrive/backend/mountwrite"
)

// Node is a current, stable projection namespace entry.
type Node struct {
	ObjectID    string
	ParentID    string
	Name        string
	Kind        mountfs.Kind
	Revision    uint64
	Size        int64
	ContentHash string
	Encrypted   bool
}

// Resolver resolves the current portable projection namespace. Implementations
// must bypass read snapshot caches so expected revisions describe the state
// against which Telegram control operations will compare-and-swap.
type Resolver interface {
	Resolve(ctx context.Context, path string) (Node, bool, error)
}

// Engine is the injectable durable mutation coordinator used by Session.
type Engine interface {
	Put(context.Context, mountwrite.PutRequest, io.Reader) (mountwrite.MutationResult, error)
	Mkdir(context.Context, mountwrite.MkdirRequest) (mountwrite.MutationResult, error)
	Move(context.Context, mountwrite.MoveRequest) (mountwrite.MutationResult, error)
	Delete(context.Context, mountwrite.DeleteRequest) (mountwrite.MutationResult, error)
	Recover(context.Context) (mountwrite.RecoveryReport, error)
	Status() mountwrite.Status
	Drain(context.Context) error
	Close(context.Context) error
}

var _ Engine = (*mountwrite.Coordinator)(nil)
