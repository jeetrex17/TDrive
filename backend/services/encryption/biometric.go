package encryption

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"TDrive/backend/biometric"
	tdcrypto "TDrive/backend/crypto"
	"TDrive/backend/projection"
)

// BiometricScope snapshots the personal vault used by a native authentication
// request. Reading an existing configuration works offline and never creates a
// new master key. The app owner must supply its current authenticated account.
func (s *Service) BiometricScope(accountID int64) (biometric.Scope, error) {
	if err := s.ready(); err != nil {
		return biometric.Scope{}, err
	}
	if accountID <= 0 {
		return biometric.Scope{}, biometric.ErrInvalidated
	}
	channelID := s.personalID()
	if channelID == 0 {
		return biometric.Scope{}, ErrVaultNotConfigured
	}
	config, err := projection.GetEncryptionConfig(s.db, channelID)
	if errors.Is(err, projection.ErrEncryptionConfigNotFound) || (err == nil && !config.Enabled) {
		return biometric.Scope{}, ErrVaultNotConfigured
	}
	if err != nil {
		return biometric.Scope{}, err
	}
	digest, err := biometricConfigHash(accountID, config)
	if err != nil {
		return biometric.Scope{}, err
	}
	scope := biometric.Scope{AccountID: accountID, ChannelID: channelID, ConfigHash: digest}
	if err := scope.Valid(); err != nil {
		return biometric.Scope{}, err
	}
	return scope, nil
}

// BiometricEnrollmentKey verifies the password afresh and returns an owned key
// without unlocking the session. Revalidation rejects a drive or config change
// during password derivation. The caller must clear the returned buffer.
func (s *Service) BiometricEnrollmentKey(scope biometric.Scope, password string) ([]byte, error) {
	if strings.TrimSpace(password) == "" {
		return nil, fmt.Errorf("password required")
	}
	config, err := s.biometricConfigForScope(scope)
	if err != nil {
		return nil, err
	}
	key, err := s.unwrapMasterKey(config, password)
	if err != nil {
		return nil, err
	}
	if _, err := s.biometricConfigForScope(scope); err != nil {
		zeroBytes(key)
		return nil, err
	}
	return key, nil
}

// VerifyBiometricKey accepts a native key only for the still-current vault and
// its authenticated key-check marker. StoreMasterKey owns a defensive copy.
// The app's lifecycle gate must reject lock/logout results before this call.
func (s *Service) VerifyBiometricKey(scope biometric.Scope, key []byte) error {
	config, err := s.biometricConfigForScope(scope)
	if err != nil {
		return err
	}
	if len(key) != masterKeySize {
		return ErrInvalidMasterKey
	}
	if err := tdcrypto.VerifyKeyCheck(key, config.KeyCheck); err != nil {
		return biometric.ErrInvalidated
	}
	if _, err := s.biometricConfigForScope(scope); err != nil {
		return err
	}
	return s.StoreMasterKey(key)
}

// VerifyBiometricPasswordChange validates both credentials before the app
// removes native enrollment. A failed password change preserves that binding.
func (s *Service) VerifyBiometricPasswordChange(scope biometric.Scope, currentPassword, newPassword string) error {
	if err := validateNewPassword(newPassword); err != nil {
		return err
	}
	key, err := s.BiometricEnrollmentKey(scope, currentPassword)
	defer zeroBytes(key)
	return err
}

func (s *Service) biometricConfigForScope(scope biometric.Scope) (projection.EncryptionConfig, error) {
	if err := s.ready(); err != nil {
		return projection.EncryptionConfig{}, err
	}
	if err := scope.Valid(); err != nil {
		return projection.EncryptionConfig{}, err
	}
	if scope.ChannelID != s.personalID() {
		return projection.EncryptionConfig{}, biometric.ErrInvalidated
	}
	config, err := projection.GetEncryptionConfig(s.db, scope.ChannelID)
	if errors.Is(err, projection.ErrEncryptionConfigNotFound) {
		return projection.EncryptionConfig{}, biometric.ErrInvalidated
	}
	if err != nil {
		return projection.EncryptionConfig{}, err
	}
	// Bind the same snapshot that supplies the key check, rather than trusting
	// an earlier database read when sync can replace configuration concurrently.
	digest, err := biometricConfigHash(scope.AccountID, config)
	if err != nil {
		return projection.EncryptionConfig{}, err
	}
	if !config.Enabled || subtle.ConstantTimeCompare([]byte(digest), []byte(scope.ConfigHash)) != 1 || scope.ChannelID != s.personalID() {
		return projection.EncryptionConfig{}, biometric.ErrInvalidated
	}
	return config, nil
}

func biometricConfigHash(accountID int64, config projection.EncryptionConfig) (string, error) {
	params, err := tdcrypto.UnmarshalParams(config.KDFParamsJSON)
	if err != nil {
		return "", err
	}
	canonical, err := json.Marshal(struct {
		Domain           string
		AccountID        int64
		ChannelID        int64
		Version          int
		KDF              tdcrypto.Params
		Salt             []byte
		WrappedMasterKey []byte
		KeyCheck         []byte
	}{
		Domain: "tdrive/biometric/config/v1", AccountID: accountID,
		ChannelID: config.ChannelID, Version: config.Version, KDF: params,
		Salt: config.KDFSalt, WrappedMasterKey: config.WrappedMasterKey, KeyCheck: config.KeyCheck,
	})
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(canonical)
	return hex.EncodeToString(digest[:]), nil
}
