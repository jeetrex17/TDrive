package main

import (
	"context"
	"fmt"
	"time"

	"TDrive/backend/core"
	encservice "TDrive/backend/services/encryption"
)

const encryptionMountTransitionTimeout = 55 * time.Second

// EncryptionStatus is the snapshot the frontend uses for per-upload prompts
// and for unlocking an encrypted personal-drive mount. Mounted writes always
// follow the drive policy; they never expose a per-file encryption choice.
type EncryptionStatus struct {
	Available          bool   `json:"available"`           // a personal channel is known
	PasswordSet        bool   `json:"password_set"`        // user has created an encryption password
	PasswordRemembered bool   `json:"password_remembered"` // master key is in process memory
	Hint               string `json:"hint"`                // optional plaintext password hint
}

// ErrEncryptionPasswordRequired is returned by upload/download/preview
// paths when they need the master key but it is not loaded into memory.
var ErrEncryptionPasswordRequired = encservice.ErrPasswordRequired

type appEncryptionService interface {
	StatusContext(context.Context) (encservice.Status, error)
	CreatePasswordContext(context.Context, string, string) error
	UsePassword(password string) error
	ChangePassword(currentPassword string, newPassword string, hint string) error
}

// personalChannelID returns the saved personal channel id without
// requiring the active drive to be the personal one. Returns 0 if no
// personal channel is configured (fresh install before InitDrive ran).
func personalChannelID() int64 {
	return core.PersonalChannelID()
}

func (a *App) encryptionService() appEncryptionService {
	if a == nil {
		return nil
	}
	if a.encryptionServiceOverride != nil {
		return a.encryptionServiceOverride
	}
	if a.engine == nil {
		return nil
	}
	return a.engine.EncryptionService()
}

func (a *App) clearEncryptionSession() {
	if a == nil {
		return
	}
	a.closeEncryptedNativeMedia()
	if a.engine != nil {
		a.engine.ClearEncryptionSession()
	}
	a.emit("encrypted_media_sessions_closed")
}

func (a *App) closeMountForEncryptionTransitionLocked(ctx context.Context) error {
	if err := a.closeMountControllerLocked(ctx); err != nil {
		return fmt.Errorf("eject TDrive before changing the encryption session: %w", err)
	}
	return nil
}

func (a *App) lockEncryptionSession() error {
	ctx, cancel := context.WithTimeout(context.Background(), encryptionMountTransitionTimeout)
	defer cancel()
	release, err := a.acquireMountLifecycle(ctx)
	if err != nil {
		return fmt.Errorf("eject TDrive before changing the encryption session: %w", err)
	}
	defer release()
	return a.lockEncryptionSessionLocked(ctx)
}

// lockEncryptionSessionLocked requires mountLifecycle to be held. Keeping the
// controller close and key erasure under one gate prevents a racing Start from
// acquiring a lease between those two steps.
func (a *App) lockEncryptionSessionLocked(ctx context.Context) error {
	if err := a.closeMountForEncryptionTransitionLocked(ctx); err != nil {
		return err
	}
	a.clearEncryptionSession()
	return nil
}

// runWithClosedMountForLogout holds the lifecycle gate through all local
// logout cleanup. A queued mount can only resume after logout has removed the
// session data, at which point it cannot acquire an encryption key lease.
func (a *App) runWithClosedMountForLogout(action func() error) error {
	ctx, cancel := context.WithTimeout(context.Background(), encryptionMountTransitionTimeout)
	defer cancel()
	release, err := a.acquireMountLifecycle(ctx)
	if err != nil {
		return fmt.Errorf("eject TDrive before changing the encryption session: %w", err)
	}
	defer release()
	if err := a.lockEncryptionSessionLocked(ctx); err != nil {
		return err
	}
	if action != nil {
		if err := action(); err != nil {
			return err
		}
	}
	// Only successful local cleanup is terminal. If cleanup fails, the mount is
	// already safely closed and the key cleared, but the user may unlock and
	// remount instead of being trapped in a half-logged-out process.
	a.mountLifecycleTerminal = true
	return nil
}

// EncryptionStatus reports whether the user has set an encryption
// password, and whether that password has already been accepted for the
// current app session.
func (a *App) EncryptionStatus() (EncryptionStatus, error) {
	service := a.encryptionService()
	if service == nil {
		return EncryptionStatus{}, fmt.Errorf("backend not ready")
	}
	status, err := service.StatusContext(a.appContext())
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
	if a.encryptionService() == nil {
		return fmt.Errorf("backend not ready")
	}
	ctx, cancel := context.WithTimeout(context.Background(), encryptionMountTransitionTimeout)
	defer cancel()
	release, err := a.acquireMountLifecycle(ctx)
	if err != nil {
		return fmt.Errorf("eject TDrive before changing the encryption session: %w", err)
	}
	defer release()
	if err := a.closeMountForEncryptionTransitionLocked(ctx); err != nil {
		return err
	}
	return a.encryptionService().CreatePasswordContext(ctx, password, hint)
}

// UseEncryptionPassword verifies an existing encryption password and keeps
// the master key in memory for the rest of the app session.
func (a *App) UseEncryptionPassword(password string) error {
	if a.encryptionService() == nil {
		return fmt.Errorf("backend not ready")
	}
	ctx, cancel := context.WithTimeout(context.Background(), encryptionMountTransitionTimeout)
	defer cancel()
	release, err := a.acquireMountLifecycle(ctx)
	if err != nil {
		return fmt.Errorf("unlock encryption: %w", err)
	}
	defer release()
	return a.encryptionService().UsePassword(password)
}

// ChangeEncryptionPassword verifies the current password, then re-wraps
// the same master key with the new password. Existing encrypted files stay
// decryptable; file contents are not re-encrypted.
func (a *App) ChangeEncryptionPassword(currentPassword string, newPassword string, hint string) error {
	if a.encryptionService() == nil {
		return fmt.Errorf("backend not ready")
	}
	ctx, cancel := context.WithTimeout(context.Background(), encryptionMountTransitionTimeout)
	defer cancel()
	release, err := a.acquireMountLifecycle(ctx)
	if err != nil {
		return fmt.Errorf("change encryption password: %w", err)
	}
	defer release()
	return a.encryptionService().ChangePassword(currentPassword, newPassword, hint)
}
