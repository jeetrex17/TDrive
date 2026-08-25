// Package mountlifecycle provides process-level coordination shared by the
// GUI and daemon mount entry points.
package mountlifecycle

import (
	"context"
	"errors"
	"sync"
)

// ErrContextRequired reports an invalid lifecycle acquisition boundary.
var ErrContextRequired = errors.New("mount lifecycle: context is required")

// Gate is a zero-value, context-aware binary gate. Status reads stay outside
// the gate, while callers waiting behind an OS mount operation can still honor
// cancellation or a deadline.
type Gate struct {
	once  sync.Once
	token chan struct{}
}

// Lock acquires the gate or returns when ctx is canceled.
func (gate *Gate) Lock(ctx context.Context) error {
	if ctx == nil {
		return ErrContextRequired
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	gate.initialize()
	select {
	case gate.token <- struct{}{}:
		if err := ctx.Err(); err != nil {
			<-gate.token
			return err
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// TryLock acquires the gate without waiting.
func (gate *Gate) TryLock() bool {
	gate.initialize()
	select {
	case gate.token <- struct{}{}:
		return true
	default:
		return false
	}
}

// Unlock releases a gate previously acquired with Lock or TryLock.
func (gate *Gate) Unlock() {
	<-gate.token
}

func (gate *Gate) initialize() {
	gate.once.Do(func() { gate.token = make(chan struct{}, 1) })
}
