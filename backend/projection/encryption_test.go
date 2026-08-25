package projection

import (
	"bytes"
	"errors"
	"math"
	"reflect"
	"testing"

	tdcrypto "TDrive/backend/crypto"
)

func TestEncryptionConfigStoresHint(t *testing.T) {
	db := newTestDB(t)

	cfg := validEncryptionConfig()
	cfg.Hint = "pet name"
	if err := PutEncryptionConfig(db, cfg); err != nil {
		t.Fatalf("put config: %v", err)
	}

	got, err := GetEncryptionConfig(db, testChan)
	if err != nil {
		t.Fatalf("get config: %v", err)
	}
	if got.Hint != "pet name" {
		t.Fatalf("hint = %q", got.Hint)
	}

	cfg.Hint = ""
	if err := PutEncryptionConfig(db, cfg); err != nil {
		t.Fatalf("clear hint: %v", err)
	}
	got, err = GetEncryptionConfig(db, testChan)
	if err != nil {
		t.Fatalf("get cleared config: %v", err)
	}
	if got.Hint != "" {
		t.Fatalf("hint after clear = %q", got.Hint)
	}
}

func TestPutEncryptionConfigRejectsUntrustedVaultMetadata(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*EncryptionConfig)
		want   error
	}{
		{name: "unsupported negative version", mutate: func(c *EncryptionConfig) { c.Version = -1 }, want: ErrUnsupportedEncryptionConfigVersion},
		{name: "unsupported future version", mutate: func(c *EncryptionConfig) { c.Version = 2 }, want: ErrUnsupportedEncryptionConfigVersion},
		{name: "unsupported kdf", mutate: func(c *EncryptionConfig) {
			c.KDFParamsJSON = `{"kdf":"scrypt","memory":65536,"time":3,"parallelism":4,"key_len":32,"salt_len":16}`
		}, want: tdcrypto.ErrUnsupportedKDF},
		{name: "zero argon time", mutate: func(c *EncryptionConfig) {
			c.KDFParamsJSON = `{"memory":65536,"time":0,"parallelism":4,"key_len":32,"salt_len":16}`
		}, want: tdcrypto.ErrInvalidKDFParams},
		{name: "extreme argon memory", mutate: func(c *EncryptionConfig) {
			c.KDFParamsJSON = `{"memory":4294967295,"time":3,"parallelism":4,"key_len":32,"salt_len":16}`
		}, want: tdcrypto.ErrInvalidKDFParams},
		{name: "mismatched salt", mutate: func(c *EncryptionConfig) { c.KDFSalt = bytes.Repeat([]byte{1}, 15) }, want: tdcrypto.ErrInvalidKDFParams},
		{name: "oversized salt", mutate: func(c *EncryptionConfig) { c.KDFSalt = bytes.Repeat([]byte{1}, 65) }, want: tdcrypto.ErrInvalidKDFParams},
		{name: "short wrapped key", mutate: func(c *EncryptionConfig) { c.WrappedMasterKey = c.WrappedMasterKey[:len(c.WrappedMasterKey)-1] }, want: tdcrypto.ErrCorruptKeyData},
		{name: "oversized wrapped key", mutate: func(c *EncryptionConfig) { c.WrappedMasterKey = bytes.Repeat([]byte{1}, 1<<20) }, want: tdcrypto.ErrCorruptKeyData},
		{name: "short key check", mutate: func(c *EncryptionConfig) { c.KeyCheck = c.KeyCheck[:len(c.KeyCheck)-1] }, want: tdcrypto.ErrCorruptKeyData},
		{name: "oversized key check", mutate: func(c *EncryptionConfig) { c.KeyCheck = bytes.Repeat([]byte{1}, 1<<20) }, want: tdcrypto.ErrCorruptKeyData},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			db := newTestDB(t)
			cfg := validEncryptionConfig()
			test.mutate(&cfg)
			err := PutEncryptionConfig(db, cfg)
			if !errors.Is(err, test.want) || !errors.Is(err, ErrInvalidEncryptionConfig) {
				t.Fatalf("PutEncryptionConfig error = %v, want %v and ErrInvalidEncryptionConfig", err, test.want)
			}
			if _, err := GetEncryptionConfig(db, testChan); !errors.Is(err, ErrEncryptionConfigNotFound) {
				t.Fatalf("invalid config mutated projection: %v", err)
			}
		})
	}
}

func TestGetEncryptionConfigRejectsCorruptPersistedVault(t *testing.T) {
	db := newTestDB(t)
	cfg := validEncryptionConfig()
	if _, err := db.Exec(`
		INSERT INTO encryption (channel_id, enabled, kdf_salt, kdf_params_json, wrapped_master_key, key_check, hint, created_at, version)
		VALUES (?, 1, ?, ?, ?, ?, '', 1, ?)
	`, cfg.ChannelID, cfg.KDFSalt, cfg.KDFParamsJSON, cfg.WrappedMasterKey, cfg.KeyCheck, math.MaxInt); err != nil {
		t.Fatalf("seed corrupt config: %v", err)
	}

	got, err := GetEncryptionConfig(db, testChan)
	if !reflect.DeepEqual(got, EncryptionConfig{}) || !errors.Is(err, ErrInvalidEncryptionConfig) || !errors.Is(err, ErrUnsupportedEncryptionConfigVersion) {
		t.Fatalf("GetEncryptionConfig = %+v, %v; want zero config and stable version errors", got, err)
	}
}

func TestProjectedEncryptionConfigRejectsInvalidMetadata(t *testing.T) {
	db := newTestDB(t)
	cfg := validEncryptionConfig()
	op := Op{
		Type:             OpEncConfig,
		KDFSalt:          cfg.KDFSalt,
		KDFParamsJSON:    cfg.KDFParamsJSON,
		WrappedMasterKey: cfg.WrappedMasterKey,
		KeyCheck:         cfg.KeyCheck,
		ConfigVersion:    2,
	}
	err := runOp(t, db, testChan, 1, op)
	if !errors.Is(err, ErrBadOp) || !errors.Is(err, ErrInvalidEncryptionConfig) || !errors.Is(err, ErrUnsupportedEncryptionConfigVersion) {
		t.Fatalf("project invalid config error = %v; want ErrBadOp and stable config errors", err)
	}
}

func validEncryptionConfig() EncryptionConfig {
	return EncryptionConfig{
		ChannelID:        testChan,
		Enabled:          true,
		KDFSalt:          bytes.Repeat([]byte{0x11}, 16),
		KDFParamsJSON:    `{"memory":65536,"time":3,"parallelism":4,"key_len":32,"salt_len":16}`,
		WrappedMasterKey: bytes.Repeat([]byte{0x22}, 72),
		KeyCheck:         bytes.Repeat([]byte{0x33}, 59),
		Version:          1,
	}
}
