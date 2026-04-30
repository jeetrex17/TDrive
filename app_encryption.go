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

// EncryptionStatus is the snapshot the frontend uses for the upload-time
// "encrypt these files?" prompt. There is no drive-wide "enabled" mode —
// encryption is a per-upload choice. The vault either exists (because
// the user once set a password) or it doesn't.
type EncryptionStatus struct {
	Available   bool `json:"available"`    // a personal channel is known
	VaultExists bool `json:"vault_exists"` // the user has set a password
	Unlocked    bool `json:"unlocked"`     // master key is in process memory
}

// ErrEncryptionLocked is returned by upload/download/preview paths when
// they need the master key but it isn't loaded. The frontend converts
// this into an unlock prompt.
var ErrEncryptionLocked = errors.New("encryption: locked")

// personalMasterKey holds the unwrapped master key for the active session.
// Cleared on logout, on Lock(), and on app restart (the binary just exits).
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

// EncryptionStatus reports whether the user has ever set a password on
// this device, and whether the master key is currently in memory.
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
	_, unlocked := loadedMasterKey()
	return EncryptionStatus{
		Available:   true,
		VaultExists: exists,
		Unlocked:    exists && unlocked,
	}, nil
}

// UnlockOrCreateVault is the single password method the frontend calls.
// If no vault exists for the personal drive, this creates one with the
// supplied password. If a vault already exists, this verifies the
// password and loads the master key into memory. Either way, the
// session ends up unlocked on success.
//
// Folding setup and unlock into one call means the frontend doesn't
// need to know — and doesn't need to ask the backend — whether this is
// the user's first encrypted upload. The upload flow asks for a
// password and the backend figures out the rest.
func (a *App) UnlockOrCreateVault(password string) error {
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
		return createVault(channelID, password)
	}
	return unlockExistingVault(existing, password)
}

func createVault(channelID int64, password string) error {
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

func unlockExistingVault(cfg projection.EncryptionConfig, password string) error {
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

// LockEncryption clears the in-memory master key. Bound for parity with
// the logout cleanup path; not surfaced in the UI in v1.
func (a *App) LockEncryption() error {
	clearMasterKey()
	return nil
}

// masterKeyForUpload returns the loaded master key when the caller has
// opted in to encryption for this batch. Encryption is a per-upload
// choice now — no drive-wide gate. When wantEncrypted is false the
// caller wants plaintext; we return a nil key and no error.
//
// Returns ErrEncryptionLocked if the user asked to encrypt but the
// session isn't unlocked. The frontend handles the prompt before
// calling, so this is a defensive check for a race where the key was
// cleared between the prompt and the upload.
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
	return nil, ErrEncryptionLocked
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
	return nil, ErrEncryptionLocked
}
