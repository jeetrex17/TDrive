package projection

import (
	"database/sql"
	"errors"
	"fmt"
	"time"
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

var ErrEncryptionConfigNotFound = errors.New("projection: encryption config not found")

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
	return c, nil
}

// PutEncryptionConfig writes (inserts or replaces) the config for a
// channel. Used when the user first creates an encryption password and,
// later, password change.
func PutEncryptionConfig(db *sql.DB, c EncryptionConfig) error {
	if c.ChannelID == 0 {
		return fmt.Errorf("projection: encryption config requires channel id")
	}
	if len(c.KDFSalt) == 0 || c.KDFParamsJSON == "" {
		return fmt.Errorf("projection: encryption config missing kdf material")
	}
	if len(c.WrappedMasterKey) == 0 || len(c.KeyCheck) == 0 {
		return fmt.Errorf("projection: encryption config missing key material")
	}
	if c.CreatedAt == 0 {
		c.CreatedAt = time.Now().Unix()
	}
	if c.Version == 0 {
		c.Version = 1
	}
	enabled := 0
	if c.Enabled {
		enabled = 1
	}
	_, err := db.Exec(`
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
