// Package biometric protects a device-local copy of the vault master key with
// native biometric authentication. Providers never expose key material to JSON
// bindings; callers own and must clear all returned secret buffers.
package biometric

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
)

var (
	ErrUnavailable = errors.New("biometric unlock is unavailable on this device")
	ErrCanceled    = errors.New("biometric authentication was canceled")
	ErrInvalidated = errors.New("biometric enrollment is no longer valid; use your password")
	ErrNotEnrolled = errors.New("biometric unlock is not enabled on this device")
)

// Scope binds native enrollment to one account, its personal drive and the
// cryptographic vault configuration. A password rewrap changes ConfigHash.
type Scope struct {
	AccountID  int64
	ChannelID  int64
	ConfigHash string
}

func (s Scope) Valid() error {
	if s.AccountID <= 0 || s.ChannelID <= 0 || len(s.ConfigHash) != sha256.Size*2 {
		return fmt.Errorf("%w: invalid vault scope", ErrInvalidated)
	}
	if _, err := hex.DecodeString(s.ConfigHash); err != nil {
		return fmt.Errorf("%w: invalid vault configuration digest", ErrInvalidated)
	}
	return nil
}

// ID is a stable native item name. It deliberately excludes ConfigHash so a
// password change can locate and remove the old device enrollment.
func (s Scope) ID() string {
	digest := scopeDigest("tdrive/biometric/item/v1", s, nil)
	return hex.EncodeToString(digest[:])
}

type Status struct {
	Available bool   `json:"available"`
	Enrolled  bool   `json:"enrolled"`
	Kind      string `json:"kind"`
	Reason    string `json:"reason"`
}

type Provider interface {
	Status(context.Context, Scope) (Status, error)
	Enroll(context.Context, Scope, []byte) error
	Unlock(context.Context, Scope) ([]byte, error)
	Delete(context.Context, Scope) error
}

// EncodeSecret returns an owned, fixed-size native payload containing a scope
// digest and the master key. The payload must only enter protected native
// storage; it is plaintext until the provider protects it.
func EncodeSecret(scope Scope, key []byte) ([]byte, error) {
	if err := scope.Valid(); err != nil {
		return nil, err
	}
	if len(key) != 32 {
		return nil, fmt.Errorf("%w: invalid master key length", ErrInvalidated)
	}
	config, _ := hex.DecodeString(scope.ConfigHash)
	digest := scopeDigest("tdrive/biometric/secret/v1", scope, config)
	payload := make([]byte, sha256.Size+32)
	copy(payload, digest[:])
	copy(payload[sha256.Size:], key)
	return payload, nil
}

// DecodeSecret checks the complete scope before returning an owned master key.
// Callers must clear both the native payload and the returned key after use.
func DecodeSecret(scope Scope, payload []byte) ([]byte, error) {
	if err := scope.Valid(); err != nil {
		return nil, err
	}
	if len(payload) != sha256.Size+32 {
		return nil, ErrInvalidated
	}
	config, _ := hex.DecodeString(scope.ConfigHash)
	digest := scopeDigest("tdrive/biometric/secret/v1", scope, config)
	if subtle.ConstantTimeCompare(digest[:], payload[:sha256.Size]) != 1 {
		return nil, ErrInvalidated
	}
	return bytes.Clone(payload[sha256.Size:]), nil
}

func scopeDigest(domain string, scope Scope, config []byte) [sha256.Size]byte {
	var identifiers [16]byte
	binary.BigEndian.PutUint64(identifiers[:8], uint64(scope.AccountID))
	binary.BigEndian.PutUint64(identifiers[8:], uint64(scope.ChannelID))
	hash := sha256.New()
	hash.Write([]byte(domain))
	hash.Write([]byte{0})
	hash.Write(identifiers[:])
	hash.Write(config)
	var digest [sha256.Size]byte
	copy(digest[:], hash.Sum(nil))
	return digest
}
