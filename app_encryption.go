package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync/atomic"

	"TDrive/backend"
	"TDrive/backend/auth"
	"TDrive/backend/crypto"
	"TDrive/backend/projection"
)

// EncryptionStatus is the snapshot the frontend uses for per-upload
// encryption prompts. There is no drive-wide encrypted mode: the user
// chooses whether each upload batch should be encrypted.
type EncryptionStatus struct {
	Available          bool `json:"available"`           // a personal channel is known
	PasswordSet        bool `json:"password_set"`        // user has created an encryption password
	PasswordRemembered bool `json:"password_remembered"` // master key is in process memory
}

// ErrEncryptionPasswordRequired is returned by upload/download/preview
// paths when they need the master key but it is not loaded into memory.
var ErrEncryptionPasswordRequired = errors.New("encryption password required")

// personalMasterKey holds the unwrapped master key for the active session.
// Cleared on logout and on app restart (the binary just exits).
var personalMasterKey atomic.Pointer[[]byte]

func loadedMasterKey() ([]byte, bool) {
	p := personalMasterKey.Load()
	if p == nil {
		return nil, false
	}
	return *p, true
}

func storeMasterKey(key []byte) {
	cp := append([]byte(nil), key...)
	personalMasterKey.Store(&cp)
}

func clearMasterKey() {
	personalMasterKey.Store(nil)
}

// personalChannelID returns the saved personal channel id without
// requiring the active drive to be the personal one. Returns 0 if no
// personal channel is configured (fresh install before InitDrive ran).
func personalChannelID() int64 {
	id, _ := auth.LoadConfig()
	return id
}

// EncryptionStatus reports whether the user has set an encryption
// password, and whether that password has already been accepted for the
// current app session.
func (a *App) EncryptionStatus() (EncryptionStatus, error) {
	if backend.DB == nil {
		return EncryptionStatus{}, fmt.Errorf("db not ready")
	}
	channelID := personalChannelID()
	if channelID == 0 {
		return EncryptionStatus{Available: false}, nil
	}
	_, err := projection.GetEncryptionConfig(backend.DB, channelID)
	exists := err == nil
	if err != nil && !errors.Is(err, projection.ErrEncryptionConfigNotFound) {
		return EncryptionStatus{}, err
	}
	_, remembered := loadedMasterKey()
	return EncryptionStatus{
		Available:          true,
		PasswordSet:        exists,
		PasswordRemembered: exists && remembered,
	}, nil
}

// UseEncryptionPassword is the single password method the frontend calls.
// If the user has not created an encryption password yet, this creates
// the master key and stores it wrapped by the supplied password. If the
// password already exists, this verifies it and keeps the master key in
// memory for the rest of the app session.
func (a *App) UseEncryptionPassword(password string) error {
	if backend.DB == nil {
		return fmt.Errorf("db not ready")
	}
	if strings.TrimSpace(password) == "" {
		return fmt.Errorf("password required")
	}
	channelID := personalChannelID()
	if channelID == 0 {
		return fmt.Errorf("personal drive not initialised yet")
	}

	existing, err := projection.GetEncryptionConfig(backend.DB, channelID)
	if err != nil && !errors.Is(err, projection.ErrEncryptionConfigNotFound) {
		return err
	}
	if errors.Is(err, projection.ErrEncryptionConfigNotFound) {
		return createEncryptionPassword(channelID, password)
	}
	return rememberEncryptionPassword(existing, password)
}

func createEncryptionPassword(channelID int64, password string) error {
	params := crypto.DefaultParams()
	salt, err := crypto.NewSalt(params)
	if err != nil {
		return err
	}
	master, err := crypto.NewMasterKey()
	if err != nil {
		return err
	}
	kek := crypto.DeriveKEK([]byte(password), salt, params)
	wrapped, err := crypto.WrapMasterKey(master, kek)
	if err != nil {
		return err
	}
	check, err := crypto.EncodeKeyCheck(master)
	if err != nil {
		return err
	}
	paramsJSON, err := crypto.MarshalParams(params)
	if err != nil {
		return err
	}
	cfg := projection.EncryptionConfig{
		ChannelID:        channelID,
		Enabled:          true,
		KDFSalt:          salt,
		KDFParamsJSON:    paramsJSON,
		WrappedMasterKey: wrapped,
		KeyCheck:         check,
		Version:          1,
	}
	if err := projection.PutEncryptionConfig(backend.DB, cfg); err != nil {
		return err
	}
	storeMasterKey(master)
	return nil
}

func rememberEncryptionPassword(cfg projection.EncryptionConfig, password string) error {
	params, err := crypto.UnmarshalParams(cfg.KDFParamsJSON)
	if err != nil {
		return err
	}
	kek := crypto.DeriveKEK([]byte(password), cfg.KDFSalt, params)
	master, err := crypto.UnwrapMasterKey(cfg.WrappedMasterKey, kek)
	if err != nil {
		if errors.Is(err, crypto.ErrWrongPassword) {
			return fmt.Errorf("wrong password")
		}
		return err
	}
	if err := crypto.VerifyKeyCheck(master, cfg.KeyCheck); err != nil {
		return fmt.Errorf("wrong password")
	}
	storeMasterKey(master)
	return nil
}

// masterKeyForUpload returns the loaded master key when the caller has
// opted in to encryption for this batch. Encryption is a per-upload
// choice now — no drive-wide gate. When wantEncrypted is false the
// caller wants plaintext; we return a nil key and no error.
//
// Returns ErrEncryptionPasswordRequired if the user asked to encrypt but
// the password has not been accepted for this app session. The frontend
// handles the prompt before calling, so this is a defensive race check.
func masterKeyForUpload(channelID int64, wantEncrypted bool) ([]byte, error) {
	if !wantEncrypted {
		return nil, nil
	}
	if channelID == 0 || channelID != personalChannelID() {
		return nil, fmt.Errorf("encryption is only available on My Drive")
	}
	if k, ok := loadedMasterKey(); ok {
		return k, nil
	}
	return nil, ErrEncryptionPasswordRequired
}

// writeCiphertextTemp encrypts the contents of plain into a fresh temp
// file and rewinds it to offset 0 for streaming upload. Caller owns the
// returned *os.File and must Close + Remove it.
func writeCiphertextTemp(plain io.Reader, plaintextSize int64, masterKey []byte) (*os.File, error) {
	tmp, err := os.CreateTemp("", "tdrive-enc-*")
	if err != nil {
		return nil, err
	}
	if err := crypto.EncryptStream(plain, tmp, masterKey, plaintextSize); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
		return nil, err
	}
	if _, err := tmp.Seek(0, io.SeekStart); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
		return nil, err
	}
	return tmp, nil
}

// requireMasterKeyForFile is called by download and preview paths. The
// per-file `encrypted` flag drives the decision instead of the channel-
// wide `enabled` flag, because mixed plaintext+ciphertext history is the
// expected state once a user enables encryption mid-life of a drive.
func requireMasterKeyForFile(encrypted bool) (key []byte, err error) {
	if !encrypted {
		return nil, nil
	}
	if k, ok := loadedMasterKey(); ok {
		return k, nil
	}
	return nil, ErrEncryptionPasswordRequired
}
