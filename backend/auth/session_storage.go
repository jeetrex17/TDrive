package auth

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"

	"github.com/gotd/td/session"
)

type privateSessionStorage struct {
	path string
	mu   sync.Mutex
}

var _ session.Storage = (*privateSessionStorage)(nil)

func (s *privateSessionStorage) LoadSession(_ context.Context) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	data, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, session.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("read session: %w", err)
	}
	return data, nil
}

func (s *privateSessionStorage) StoreSession(_ context.Context, data []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	return writePrivateFile(s.path, data)
}
