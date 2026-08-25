package mountpolicy

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"reflect"
	"testing"

	"TDrive/backend/projection"

	_ "modernc.org/sqlite"
)

const policyChannelID int64 = 90901

func TestResolvePersonalRepairsEncryptedPolicyFromReplayOffline(t *testing.T) {
	db := policyDB(t)
	seedPolicyReplay(t, db, validPolicyConfig())
	if _, err := db.Exec(`DELETE FROM encryption WHERE channel_id = ?`, policyChannelID); err != nil {
		t.Fatalf("delete derived config: %v", err)
	}
	refreshCalls := 0

	policy, err := ResolvePersonal(context.Background(), db, policyChannelID, func(context.Context, int64) error {
		refreshCalls++
		return errors.New("offline")
	}, func() (bool, error) { return false, nil })
	if err != nil {
		t.Fatalf("ResolvePersonal() error = %v", err)
	}
	if !policy.Encrypted || policy.Unlocked || refreshCalls != 0 {
		t.Fatalf("ResolvePersonal() = %#v, refresh calls = %d", policy, refreshCalls)
	}
}

func TestResolvePersonalRepairsCorruptDerivedPolicyFromCanonicalReplay(t *testing.T) {
	db := policyDB(t)
	seedPolicyReplay(t, db, validPolicyConfig())
	if _, err := db.Exec(`UPDATE encryption SET kdf_params_json = ? WHERE channel_id = ?`, "not-json", policyChannelID); err != nil {
		t.Fatalf("corrupt derived config: %v", err)
	}
	refreshCalls := 0

	policy, err := ResolvePersonal(context.Background(), db, policyChannelID, func(context.Context, int64) error {
		refreshCalls++
		return errors.New("offline")
	}, func() (bool, error) { return true, nil })
	if err != nil {
		t.Fatalf("ResolvePersonal() error = %v", err)
	}
	if !policy.Encrypted || !policy.Unlocked || refreshCalls != 0 {
		t.Fatalf("ResolvePersonal() = %#v, refresh calls = %d", policy, refreshCalls)
	}
	if _, err := projection.GetEncryptionConfig(db, policyChannelID); err != nil {
		t.Fatalf("canonical replay did not repair derived config: %v", err)
	}
}

func TestResolvePersonalFailsClosedWhenAuthoritativeRefreshFails(t *testing.T) {
	db := policyDB(t)
	sentinel := errors.New("network unavailable")

	policy, err := ResolvePersonal(context.Background(), db, policyChannelID, func(context.Context, int64) error {
		return sentinel
	}, func() (bool, error) { return true, nil })
	if policy != (Policy{}) || !errors.Is(err, ErrEncryptionPolicyUnavailable) || errors.Is(err, sentinel) {
		t.Fatalf("ResolvePersonal() = (%#v, %v), want sanitized unavailable error", policy, err)
	}
}

func TestResolvePersonalAllowsPlaintextOnlyAfterAuthoritativeRefresh(t *testing.T) {
	db := policyDB(t)
	refreshCalls := 0

	policy, err := ResolvePersonal(context.Background(), db, policyChannelID, func(context.Context, int64) error {
		refreshCalls++
		return nil
	}, func() (bool, error) { return false, nil })
	if err != nil {
		t.Fatalf("ResolvePersonal() error = %v", err)
	}
	if policy != (Policy{}) || refreshCalls != 1 {
		t.Fatalf("ResolvePersonal() = %#v, refresh calls = %d", policy, refreshCalls)
	}
}

func TestResolvePersonalUsesConfigRestoredByAuthoritativeRefresh(t *testing.T) {
	db := policyDB(t)

	policy, err := ResolvePersonal(context.Background(), db, policyChannelID, func(context.Context, int64) error {
		seedPolicyReplay(t, db, validPolicyConfig())
		return nil
	}, func() (bool, error) { return true, nil })
	if err != nil {
		t.Fatalf("ResolvePersonal() error = %v", err)
	}
	if !policy.Encrypted || !policy.Unlocked {
		t.Fatalf("ResolvePersonal() = %#v, want unlocked encrypted policy", policy)
	}
}

func TestResolvePersonalUsesExistingConfigWithoutRefresh(t *testing.T) {
	db := policyDB(t)
	seedPolicyReplay(t, db, validPolicyConfig())
	refreshCalls := 0
	policy, err := ResolvePersonal(context.Background(), db, policyChannelID, func(context.Context, int64) error {
		refreshCalls++
		return nil
	}, func() (bool, error) { return true, nil })
	if err != nil || !policy.Encrypted || !policy.Unlocked || refreshCalls != 0 {
		t.Fatalf("ResolvePersonal() = (%#v, %v), refresh calls = %d", policy, err, refreshCalls)
	}
}

func TestResolvePersonalFailsClosedForInvalidPolicyStates(t *testing.T) {
	t.Run("noncanonical config is preserved but not trusted", func(t *testing.T) {
		db := policyDB(t)
		want := validPolicyConfig()
		if err := projection.PutEncryptionConfig(db, want); err != nil {
			t.Fatalf("put noncanonical config: %v", err)
		}
		before, err := projection.GetEncryptionConfig(db, policyChannelID)
		if err != nil {
			t.Fatalf("get noncanonical config before resolve: %v", err)
		}
		refreshCalls := 0
		policy, err := ResolvePersonal(context.Background(), db, policyChannelID, func(context.Context, int64) error {
			refreshCalls++
			return errors.New("offline")
		}, func() (bool, error) { return true, nil })
		if policy != (Policy{}) || !errors.Is(err, ErrEncryptionPolicyUnavailable) || refreshCalls != 1 {
			t.Fatalf("ResolvePersonal() = (%#v, %v), refresh calls = %d", policy, err, refreshCalls)
		}
		got, getErr := projection.GetEncryptionConfig(db, policyChannelID)
		if getErr != nil || !reflect.DeepEqual(got, before) {
			t.Fatalf("preserved config = (%#v, %v), want %#v", got, getErr, before)
		}
	})

	t.Run("noncanonical disabled config", func(t *testing.T) {
		db := policyDB(t)
		config := validPolicyConfig()
		config.Enabled = false
		if err := projection.PutEncryptionConfig(db, config); err != nil {
			t.Fatalf("put disabled config: %v", err)
		}
		policy, err := ResolvePersonal(context.Background(), db, policyChannelID, func(context.Context, int64) error { return nil }, func() (bool, error) { return false, nil })
		if policy != (Policy{}) || !errors.Is(err, ErrEncryptionPolicyUnavailable) {
			t.Fatalf("ResolvePersonal() = (%#v, %v)", policy, err)
		}
	})

	t.Run("unlock status failure", func(t *testing.T) {
		db := policyDB(t)
		seedPolicyReplay(t, db, validPolicyConfig())
		detail := errors.New("key state unavailable")
		policy, err := ResolvePersonal(context.Background(), db, policyChannelID, func(context.Context, int64) error { return nil }, func() (bool, error) { return false, detail })
		if policy != (Policy{}) || !errors.Is(err, ErrEncryptionPolicyUnavailable) || errors.Is(err, detail) {
			t.Fatalf("ResolvePersonal() = (%#v, %v)", policy, err)
		}
	})

	t.Run("tampered replay", func(t *testing.T) {
		db := policyDB(t)
		seedPolicyReplay(t, db, validPolicyConfig())
		if _, err := projection.ProjectFromOp(db, policyChannelID, 101, projection.Op{Type: projection.OpEncConfig}, 1, "edited"); err != nil {
			t.Fatalf("record tamper: %v", err)
		}
		policy, err := ResolvePersonal(context.Background(), db, policyChannelID, func(context.Context, int64) error { return nil }, func() (bool, error) { return false, nil })
		if policy != (Policy{}) || !errors.Is(err, ErrEncryptionPolicyUnavailable) {
			t.Fatalf("ResolvePersonal() = (%#v, %v)", policy, err)
		}
	})

	t.Run("refresh bypasses canonical replay", func(t *testing.T) {
		db := policyDB(t)
		policy, err := ResolvePersonal(context.Background(), db, policyChannelID, func(context.Context, int64) error {
			return projection.PutEncryptionConfig(db, validPolicyConfig())
		}, func() (bool, error) { return false, nil })
		if policy != (Policy{}) || !errors.Is(err, ErrEncryptionPolicyUnavailable) {
			t.Fatalf("ResolvePersonal() = (%#v, %v)", policy, err)
		}
	})
}

func TestEnsurePersonalConfigRejectsInvalidBoundary(t *testing.T) {
	config, exists, err := EnsurePersonalConfig(nil, nil, 0, nil)
	if !reflect.DeepEqual(config, projection.EncryptionConfig{}) || exists || !errors.Is(err, ErrEncryptionPolicyUnavailable) {
		t.Fatalf("EnsurePersonalConfig() = (%#v, %t, %v)", config, exists, err)
	}
}

func TestResolvePersonalRejectsInvalidBoundaries(t *testing.T) {
	db := policyDB(t)
	refresh := func(context.Context, int64) error { return nil }
	unlock := func() (bool, error) { return false, nil }
	tests := []struct {
		name      string
		ctx       context.Context
		db        *sql.DB
		channelID int64
		refresh   RefreshFunc
		unlock    UnlockStatusFunc
	}{
		{name: "nil context", db: db, channelID: policyChannelID, refresh: refresh, unlock: unlock},
		{name: "nil database", ctx: context.Background(), channelID: policyChannelID, refresh: refresh, unlock: unlock},
		{name: "zero channel", ctx: context.Background(), db: db, refresh: refresh, unlock: unlock},
		{name: "negative channel", ctx: context.Background(), db: db, channelID: -1, refresh: refresh, unlock: unlock},
		{name: "nil refresh", ctx: context.Background(), db: db, channelID: policyChannelID, unlock: unlock},
		{name: "nil unlock status", ctx: context.Background(), db: db, channelID: policyChannelID, refresh: refresh},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			policy, err := ResolvePersonal(test.ctx, test.db, test.channelID, test.refresh, test.unlock)
			if policy != (Policy{}) || !errors.Is(err, ErrEncryptionPolicyUnavailable) {
				t.Fatalf("ResolvePersonal() = (%#v, %v), want sanitized unavailable error", policy, err)
			}
		})
	}
}

func policyDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	if err := projection.MigratePersonalChannel(db, policyChannelID); err != nil {
		t.Fatalf("migrate projection: %v", err)
	}
	return db
}

func seedPolicyReplay(t *testing.T, db *sql.DB, cfg projection.EncryptionConfig) {
	t.Helper()
	op := projection.Op{
		Type:             projection.OpEncConfig,
		KDFSalt:          append([]byte(nil), cfg.KDFSalt...),
		KDFParamsJSON:    cfg.KDFParamsJSON,
		WrappedMasterKey: append([]byte(nil), cfg.WrappedMasterKey...),
		KeyCheck:         append([]byte(nil), cfg.KeyCheck...),
		ConfigVersion:    cfg.Version,
	}
	if _, err := projection.ProjectFromOp(db, policyChannelID, 101, op, 1, projection.Format(op)); err != nil {
		t.Fatalf("seed policy replay: %v", err)
	}
}

func validPolicyConfig() projection.EncryptionConfig {
	return projection.EncryptionConfig{
		ChannelID:        policyChannelID,
		Enabled:          true,
		KDFSalt:          bytes.Repeat([]byte{0x11}, 16),
		KDFParamsJSON:    `{"memory":65536,"time":3,"parallelism":4,"key_len":32,"salt_len":16}`,
		WrappedMasterKey: bytes.Repeat([]byte{0x22}, 72),
		KeyCheck:         bytes.Repeat([]byte{0x33}, 59),
		Version:          1,
	}
}
