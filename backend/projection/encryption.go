package projection

import (
	"database/sql"
	"errors"
	"fmt"
	"time"

	tdcrypto "TDrive/backend/crypto"
)

// EncryptionConfig is the per-channel encryption password metadata. v1
// uses it for the personal channel only.
type EncryptionConfig struct {
	ChannelID        int64
	Enabled          bool
	KDFSalt          []byte
	KDFParamsJSON    string
	WrappedMasterKey []byte
	KeyCheck         []byte
	Hint             string
	CreatedAt        int64
	Version          int
}

var (
	ErrEncryptionConfigNotFound           = errors.New("projection: encryption config not found")
	ErrInvalidEncryptionConfig            = errors.New("projection: invalid encryption config")
	ErrUnsupportedEncryptionConfigVersion = errors.New("projection: unsupported encryption config version")
)

func GetEncryptionConfig(db *sql.DB, channelID int64) (EncryptionConfig, error) {
	var c EncryptionConfig
	var enabled int
	err := db.QueryRow(`
		SELECT channel_id, enabled, kdf_salt, kdf_params_json, wrapped_master_key, key_check, hint, created_at, version
		FROM encryption WHERE channel_id = ?
	`, channelID).Scan(&c.ChannelID, &enabled, &c.KDFSalt, &c.KDFParamsJSON, &c.WrappedMasterKey, &c.KeyCheck, &c.Hint, &c.CreatedAt, &c.Version)
	if errors.Is(err, sql.ErrNoRows) {
		return EncryptionConfig{}, ErrEncryptionConfigNotFound
	}
	if err != nil {
		return EncryptionConfig{}, fmt.Errorf("projection: get encryption: %w", err)
	}
	c.Enabled = enabled == 1
	if err := validateEncryptionConfig(c); err != nil {
		return EncryptionConfig{}, fmt.Errorf("projection: get encryption: %w", err)
	}
	return c, nil
}

// PutEncryptionConfig writes (inserts or replaces) the config for a
// channel. Used when the user first creates an encryption password and,
// later, password change.
func PutEncryptionConfig(db *sql.DB, c EncryptionConfig) error {
	return putEncryptionConfig(db, c)
}

func PutEncryptionConfigTx(tx *sql.Tx, c EncryptionConfig) error {
	return putEncryptionConfig(tx, c)
}

type sqlExecer interface {
	Exec(query string, args ...any) (sql.Result, error)
}

func putEncryptionConfig(exec sqlExecer, c EncryptionConfig) error {
	if c.ChannelID == 0 {
		return fmt.Errorf("projection: encryption config requires channel id")
	}
	if c.CreatedAt == 0 {
		c.CreatedAt = time.Now().Unix()
	}
	if c.Version == 0 {
		c.Version = 1
	}
	if err := validateEncryptionConfig(c); err != nil {
		return err
	}
	enabled := 0
	if c.Enabled {
		enabled = 1
	}
	_, err := exec.Exec(`
		INSERT INTO encryption (channel_id, enabled, kdf_salt, kdf_params_json, wrapped_master_key, key_check, hint, created_at, version)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(channel_id) DO UPDATE SET
			enabled = excluded.enabled,
			kdf_salt = excluded.kdf_salt,
			kdf_params_json = excluded.kdf_params_json,
			wrapped_master_key = excluded.wrapped_master_key,
			key_check = excluded.key_check,
			hint = excluded.hint,
			version = excluded.version
	`, c.ChannelID, enabled, c.KDFSalt, c.KDFParamsJSON, c.WrappedMasterKey, c.KeyCheck, c.Hint, c.CreatedAt, c.Version)
	if err != nil {
		return fmt.Errorf("projection: put encryption: %w", err)
	}
	return nil
}

func validateEncryptionConfig(c EncryptionConfig) error {
	if c.Version != 1 {
		return fmt.Errorf("%w: %w", ErrInvalidEncryptionConfig, ErrUnsupportedEncryptionConfigVersion)
	}
	params, err := tdcrypto.UnmarshalParams(c.KDFParamsJSON)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrInvalidEncryptionConfig, err)
	}
	if err := tdcrypto.ValidateVaultMaterial(c.KDFSalt, params, c.WrappedMasterKey, c.KeyCheck); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalidEncryptionConfig, err)
	}
	return nil
}
