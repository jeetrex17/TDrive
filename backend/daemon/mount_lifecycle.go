package daemon

import (
	"context"
	"errors"
	"fmt"
	"sync"
)

var errDaemonMountLifecycleTerminal = errors.New("mount lifecycle is terminal")

// mountLifecycleGate is a zero-value, context-aware binary gate. Status
// remains ungated, while callers waiting behind an OS mount operation can
// still honor request cancellation.
type mountLifecycleGate struct {
	once  sync.Once
	token chan struct{}
}

func (gate *mountLifecycleGate) lock(ctx context.Context) error {
	if ctx == nil {
		return fmt.Errorf("mount lifecycle: context is required")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	gate.once.Do(func() { gate.token = make(chan struct{}, 1) })
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

func (gate *mountLifecycleGate) tryLock() bool {
	gate.once.Do(func() { gate.token = make(chan struct{}, 1) })
	select {
	case gate.token <- struct{}{}:
		return true
	default:
		return false
	}
}

func (gate *mountLifecycleGate) unlock() {
	<-gate.token
}

func (s *Server) acquireMountLifecycle(ctx context.Context) (func(), error) {
	if s == nil {
		return nil, fmt.Errorf("mount: daemon is not ready")
	}
	if err := s.mountLifecycle.lock(ctx); err != nil {
		return nil, err
	}
	if s.mountLifecycleTerminal {
		s.mountLifecycle.unlock()
		return nil, errDaemonMountLifecycleTerminal
	}
	return s.mountLifecycle.unlock, nil
}
