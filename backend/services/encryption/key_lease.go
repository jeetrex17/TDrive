package encryption

import (
	"errors"
	"fmt"
	"sync"

	"TDrive/backend/projection"
)

var (
	ErrVaultNotConfigured   = errors.New("personal encryption vault is not configured")
	ErrMasterKeyLeaseClosed = errors.New("master key lease is closed")
	ErrInvalidMasterKey     = errors.New("invalid master key")
)

// MasterKeyLease owns an independent in-memory copy of the unlocked personal
// vault key. A mount must close its lease when it stops serving encrypted data.
type MasterKeyLease struct {
	keyMu  sync.RWMutex
	key    []byte
	closed bool
}

// AcquireMasterKeyLease returns an isolated key lease only when the personal
// vault is configured and currently unlocked.
func (s *Service) AcquireMasterKeyLease() (*MasterKeyLease, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	channelID, err := s.requiredPersonalID()
	if err != nil {
		return nil, err
	}
	config, err := projection.GetEncryptionConfig(s.db, channelID)
	if errors.Is(err, projection.ErrEncryptionConfigNotFound) {
		return nil, ErrVaultNotConfigured
	}
	if err != nil {
		return nil, fmt.Errorf("load personal encryption vault: %w", err)
	}
	if !config.Enabled {
		return nil, ErrVaultNotConfigured
	}

	s.masterKeyMu.Lock()
	defer s.masterKeyMu.Unlock()
	if s.masterKey == nil {
		return nil, ErrPasswordRequired
	}
	if len(s.masterKey) != masterKeySize {
		zeroBytes(s.masterKey)
		s.masterKey = nil
		return nil, ErrInvalidMasterKey
	}
	return &MasterKeyLease{key: append([]byte(nil), s.masterKey...)}, nil
}

// Key returns a defensive copy so callers cannot mutate the lease-owned key.
func (l *MasterKeyLease) Key() ([]byte, error) {
	if l == nil {
		return nil, ErrMasterKeyLeaseClosed
	}
	l.keyMu.RLock()
	defer l.keyMu.RUnlock()
	if l.closed || l.key == nil {
		return nil, ErrMasterKeyLeaseClosed
	}
	if len(l.key) != masterKeySize {
		return nil, ErrInvalidMasterKey
	}
	return append([]byte(nil), l.key...), nil
}

// Close releases the lease and best-effort wipes its owned key. It is safe to
// call repeatedly and concurrently with Key.
func (l *MasterKeyLease) Close() {
	if l == nil {
		return
	}
	l.keyMu.Lock()
	if !l.closed {
		zeroBytes(l.key)
		l.key = nil
		l.closed = true
	}
	l.keyMu.Unlock()
}
