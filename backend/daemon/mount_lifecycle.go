package daemon

import (
	"context"
	"errors"
	"fmt"
)

var errDaemonMountLifecycleTerminal = errors.New("mount lifecycle is terminal")

func (s *Server) acquireMountLifecycle(ctx context.Context) (func(), error) {
	if s == nil {
		return nil, fmt.Errorf("mount: daemon is not ready")
	}
	if err := s.mountLifecycle.Lock(ctx); err != nil {
		return nil, err
	}
	if s.mountLifecycleTerminal {
		s.mountLifecycle.Unlock()
		return nil, errDaemonMountLifecycleTerminal
	}
	return s.mountLifecycle.Unlock, nil
}
