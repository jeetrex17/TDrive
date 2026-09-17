package main

import (
	"context"
	"fmt"

	encservice "TDrive/backend/services/encryption"
)

// EncryptionService owns the vault password: what the frontend may know about
// it, how it is first created, how it is unlocked for the session, and how it
// is changed.
//
// These four are grouped because each one moves the master key in or out of
// process memory, and every one of them must therefore hold the mount lifecycle
// gate for the whole operation. Splitting them across types would invite a
// fifth key-touching method that forgets the gate, and a key that changes while
// a drive is mounted leaves the filesystem serving bytes it can no longer
// decrypt. Everything that only *reads* an already-unlocked key -- uploads,
// previews, the mount itself -- stays out of here on purpose.
type EncryptionService struct {
	host  serviceHost
	mount vaultMountGate
	// override replaces the engine's encryption service in tests that need to
	// park a key write mid-flight and prove a concurrent lock or logout waits
	// for it. Production always leaves this nil.
	override appEncryptionService
}

func newEncryptionService(host serviceHost, mount vaultMountGate) *EncryptionService {
	return &EncryptionService{host: host, mount: mount}
}

// mountLifecycleGate is the right to hold mount transitions still for the
// length of an operation. Anything that publishes a capability against the
// active drive needs it, so a logout cannot become terminal between the
// capability being opened and being handed to the frontend.
type mountLifecycleGate interface {
	acquireMountLifecycle(ctx context.Context) (func(), error)
}

// vaultMountGate is what this service needs from whoever owns the mount: the
// gate above, and a way to close an open mount before the key underneath it
// changes.
//
// App implements it today. It is an interface so that lifting the mount out of
// App later is a one-line rewire here instead of a rewrite of this file.
type vaultMountGate interface {
	mountLifecycleGate
	closeMountForEncryptionTransitionLocked(ctx context.Context) error
}

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

func (s *EncryptionService) service() appEncryptionService {
	if s == nil {
		return nil
	}
	if s.override != nil {
		return s.override
	}
	engine := s.host.coreEngine()
	if engine == nil {
		return nil
	}
	return engine.EncryptionService()
}

// EncryptionStatus reports whether the user has set an encryption
// password, and whether that password has already been accepted for the
// current app session.
func (s *EncryptionService) EncryptionStatus() (EncryptionStatus, error) {
	service := s.service()
	if service == nil {
		return EncryptionStatus{}, fmt.Errorf("backend not ready")
	}
	status, err := service.StatusContext(s.host.appContext())
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
func (s *EncryptionService) CreateEncryptionPassword(password string, hint string) OperationResult {
	service := s.service()
	if service == nil {
		return operationFailure(errBackendUnavailable)
	}
	ctx, cancel := context.WithTimeout(context.Background(), encryptionMountTransitionTimeout)
	defer cancel()
	release, err := s.mount.acquireMountLifecycle(ctx)
	if err != nil {
		return operationFailure(fmt.Errorf("eject TDrive before changing the encryption session: %w", err))
	}
	defer release()
	if err := s.mount.closeMountForEncryptionTransitionLocked(ctx); err != nil {
		return operationFailure(err)
	}
	return operationFailure(service.CreatePasswordContext(ctx, password, hint))
}

// UseEncryptionPassword verifies an existing encryption password and keeps
// the master key in memory for the rest of the app session.
func (s *EncryptionService) UseEncryptionPassword(password string) OperationResult {
	service := s.service()
	if service == nil {
		return operationFailure(errBackendUnavailable)
	}
	ctx, cancel := context.WithTimeout(context.Background(), encryptionMountTransitionTimeout)
	defer cancel()
	release, err := s.mount.acquireMountLifecycle(ctx)
	if err != nil {
		return operationFailure(fmt.Errorf("unlock encryption: %w", err))
	}
	defer release()
	return operationFailure(service.UsePassword(password))
}

// ChangeEncryptionPassword verifies the current password, then re-wraps
// the same master key with the new password. Existing encrypted files stay
// decryptable; file contents are not re-encrypted.
func (s *EncryptionService) ChangeEncryptionPassword(currentPassword string, newPassword string, hint string) OperationResult {
	service := s.service()
	if service == nil {
		return operationFailure(errBackendUnavailable)
	}
	ctx, cancel := context.WithTimeout(context.Background(), encryptionMountTransitionTimeout)
	defer cancel()
	release, err := s.mount.acquireMountLifecycle(ctx)
	if err != nil {
		return operationFailure(fmt.Errorf("change encryption password: %w", err))
	}
	defer release()
	return operationFailure(service.ChangePassword(currentPassword, newPassword, hint))
}
