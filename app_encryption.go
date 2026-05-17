package main

import (
	"TDrive/backend"
	"TDrive/backend/auth"
	"TDrive/backend/projection"
	encservice "TDrive/backend/services/encryption"
)

// EncryptionStatus is the snapshot the frontend uses for per-upload
// encryption prompts. There is no drive-wide encrypted mode: the user
// chooses whether each upload batch should be encrypted.
type EncryptionStatus struct {
	Available          bool   `json:"available"`           // a personal channel is known
	PasswordSet        bool   `json:"password_set"`        // user has created an encryption password
	PasswordRemembered bool   `json:"password_remembered"` // master key is in process memory
	Hint               string `json:"hint"`                // optional plaintext password hint
}

// ErrEncryptionPasswordRequired is returned by upload/download/preview
// paths when they need the master key but it is not loaded into memory.
var ErrEncryptionPasswordRequired = encservice.ErrPasswordRequired

// personalChannelID returns the saved personal channel id without
// requiring the active drive to be the personal one. Returns 0 if no
// personal channel is configured (fresh install before InitDrive ran).
func personalChannelID() int64 {
	id, _ := auth.LoadConfig()
	return id
}

func (a *App) newEncryptionService() *encservice.Service {
	return encservice.NewService(encservice.Config{
		DB:                backend.DB,
		PersonalChannelID: personalChannelID,
		EmitOp: func(channelID int64, op projection.Op) error {
			_, err := a.emitAndProject(channelID, op)
			return err
		},
	})
}

func (a *App) encryptionService() *encservice.Service {
	if a.enc == nil {
		a.enc = a.newEncryptionService()
	}
	return a.enc
}

func (a *App) clearEncryptionSession() {
	if a.enc != nil {
		a.enc.Clear()
	}
}

// EncryptionStatus reports whether the user has set an encryption
// password, and whether that password has already been accepted for the
// current app session.
func (a *App) EncryptionStatus() (EncryptionStatus, error) {
	status, err := a.encryptionService().Status()
	if err != nil {
		return EncryptionStatus{}, err
	}
	return EncryptionStatus{
		Available:          status.Available,
		PasswordSet:        status.PasswordSet,
		PasswordRemembered: status.PasswordRemembered,
		Hint:               status.Hint,
	}, nil
}

// CreateEncryptionPassword creates the user's first encryption password.
// It stores a random master key wrapped under the password and an optional
// plaintext hint. It refuses to overwrite an existing password.
func (a *App) CreateEncryptionPassword(password string, hint string) error {
	return a.encryptionService().CreatePassword(password, hint)
}

// UseEncryptionPassword verifies an existing encryption password and keeps
// the master key in memory for the rest of the app session.
func (a *App) UseEncryptionPassword(password string) error {
	return a.encryptionService().UsePassword(password)
}

// ChangeEncryptionPassword verifies the current password, then re-wraps
// the same master key with the new password. Existing encrypted files stay
// decryptable; file contents are not re-encrypted.
func (a *App) ChangeEncryptionPassword(currentPassword string, newPassword string, hint string) error {
	return a.encryptionService().ChangePassword(currentPassword, newPassword, hint)
}
