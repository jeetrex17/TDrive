// Package mountpolicy establishes the security-sensitive encryption policy of
// a personal drive before callers interpret a missing local config as proof
// that plaintext operations are safe.
package mountpolicy

import (
	"context"
	"database/sql"
	"errors"

	"TDrive/backend/projection"
)

// ErrEncryptionPolicyUnavailable is intentionally safe to expose through the
// GUI and daemon protocol. Detailed storage or network failures stay behind
// this boundary instead of leaking paths, endpoints, or Telegram internals.
var ErrEncryptionPolicyUnavailable = errors.New("encryption policy could not be verified; reconnect and try again")

type Policy struct {
	Encrypted bool
	Unlocked  bool
}

type RefreshFunc func(context.Context, int64) error
type UnlockStatusFunc func() (bool, error)

// ResolvePersonal returns plaintext eligibility only after authoritative
// history synchronization proves that no encryption policy exists.
//
// A missing derived row is first repaired from the canonical local replay so
// an already-known encrypted drive remains usable while offline. If replay has
// no policy, refresh must complete a full/incremental authoritative sync. The
// replay is then rebuilt again to validate integrity and select the newest
// password configuration. Every uncertain state fails closed.
func ResolvePersonal(
	ctx context.Context,
	db *sql.DB,
	channelID int64,
	refresh RefreshFunc,
	unlockStatus UnlockStatusFunc,
) (Policy, error) {
	if ctx == nil || db == nil || channelID <= 0 || refresh == nil || unlockStatus == nil {
		return Policy{}, ErrEncryptionPolicyUnavailable
	}
	config, exists, err := EnsurePersonalConfig(ctx, db, channelID, refresh)
	if err != nil {
		return Policy{}, err
	}
	if !exists {
		return Policy{}, nil
	}
	return encryptedPolicy(config, unlockStatus)
}

// EnsurePersonalConfig returns an encryption config when one is present and
// returns exists=false only after a successful authoritative refresh proves
// its absence. It is shared by mount eligibility and password setup so neither
// path can accidentally create plaintext or replace the remote master key from
// an incomplete local projection.
func EnsurePersonalConfig(
	ctx context.Context,
	db *sql.DB,
	channelID int64,
	refresh RefreshFunc,
) (projection.EncryptionConfig, bool, error) {
	if ctx == nil || db == nil || channelID <= 0 || refresh == nil {
		return projection.EncryptionConfig{}, false, ErrEncryptionPolicyUnavailable
	}
	// The derived encryption row is only a cache. Rebuild from canonical replay
	// before reading it so valid history can repair a stale or corrupt cache.
	found, err := projection.RebuildEncryptionConfigFromReplay(db, channelID)
	if err != nil {
		return projection.EncryptionConfig{}, false, ErrEncryptionPolicyUnavailable
	}
	if found {
		config, err := projection.GetEncryptionConfig(db, channelID)
		if err != nil {
			return projection.EncryptionConfig{}, false, ErrEncryptionPolicyUnavailable
		}
		return config, true, nil
	}
	if err := refresh(ctx, channelID); err != nil {
		return projection.EncryptionConfig{}, false, ErrEncryptionPolicyUnavailable
	}

	// A legitimate sync always records an encfg op in replay before deriving
	// the config row. Capture the post-refresh state so a buggy refresher cannot
	// bypass canonical replay and have that row silently erased below.
	_, refreshedConfigErr := projection.GetEncryptionConfig(db, channelID)
	found, err = projection.RebuildEncryptionConfigFromReplay(db, channelID)
	if err != nil {
		return projection.EncryptionConfig{}, false, ErrEncryptionPolicyUnavailable
	}
	if !found {
		if !errors.Is(refreshedConfigErr, projection.ErrEncryptionConfigNotFound) {
			return projection.EncryptionConfig{}, false, ErrEncryptionPolicyUnavailable
		}
		return projection.EncryptionConfig{}, false, nil
	}

	config, err := projection.GetEncryptionConfig(db, channelID)
	if err != nil {
		return projection.EncryptionConfig{}, false, ErrEncryptionPolicyUnavailable
	}
	return config, true, nil
}

func encryptedPolicy(config projection.EncryptionConfig, unlockStatus UnlockStatusFunc) (Policy, error) {
	if !config.Enabled || unlockStatus == nil {
		return Policy{}, ErrEncryptionPolicyUnavailable
	}
	unlocked, err := unlockStatus()
	if err != nil {
		return Policy{}, ErrEncryptionPolicyUnavailable
	}
	return Policy{Encrypted: true, Unlocked: unlocked}, nil
}
