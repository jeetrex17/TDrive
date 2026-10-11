package app

import (
	"context"
	"errors"
	"fmt"
	"time"

	"TDrive/backend/biometric"
	"TDrive/backend/core"
	"TDrive/backend/datadir"
	encservice "TDrive/backend/services/encryption"
)

// BiometricStatus contains capability metadata only; keys never enter Wails.
type BiometricStatus struct {
	Available bool   `json:"available"`
	Enrolled  bool   `json:"enrolled"`
	Kind      string `json:"kind"`
	Reason    string `json:"reason"`
}

type biometricEncryptionService interface {
	BiometricScope(int64) (biometric.Scope, error)
	BiometricEnrollmentKey(biometric.Scope, string) ([]byte, error)
	VerifyBiometricKey(biometric.Scope, []byte) error
	VerifyBiometricPasswordChange(biometric.Scope, string, string) error
}

type biometricAttempt struct {
	ctx      context.Context
	cancel   context.CancelFunc
	epoch    uint64
	engine   *core.Engine
	scope    biometric.Scope
	service  biometricEncryptionService
	provider biometric.Provider
}

var errBiometricBusy = errors.New("biometric authentication is already in progress")

func (s *EncryptionService) nativeBiometricProvider() (biometric.Provider, error) {
	s.biometricMu.Lock()
	defer s.biometricMu.Unlock()
	if s.biometricProviderOverride != nil {
		return s.biometricProviderOverride, nil
	}
	if s.biometricProvider == nil {
		dir, err := datadir.Dir()
		if err != nil {
			return nil, err
		}
		s.biometricProvider = biometric.NewProvider(dir)
	}
	return s.biometricProvider, nil
}

func (s *EncryptionService) biometricScope(ctx context.Context) (biometric.Scope, biometricEncryptionService, error) {
	engine := s.host.coreEngine()
	if engine == nil {
		return biometric.Scope{}, nil, errBackendUnavailable
	}
	service, ok := s.service().(biometricEncryptionService)
	if !ok {
		return biometric.Scope{}, nil, biometric.ErrUnavailable
	}
	accountID, err := engine.ActorID(ctx)
	if err != nil {
		return biometric.Scope{}, nil, err
	}
	scope, err := service.BiometricScope(accountID)
	return scope, service, err
}

func (s *EncryptionService) BiometricStatus() (BiometricStatus, error) {
	scope, _, err := s.biometricScope(s.host.appContext())
	if errors.Is(err, encservice.ErrVaultNotConfigured) {
		return BiometricStatus{Reason: "not_enrolled"}, nil
	}
	if err != nil {
		return BiometricStatus{}, err
	}
	provider, err := s.nativeBiometricProvider()
	if err != nil {
		return BiometricStatus{}, err
	}
	status, err := provider.Status(s.host.appContext(), scope)
	if errors.Is(err, biometric.ErrUnavailable) {
		status.Available = false
		if status.Reason == "" {
			status.Reason = "unavailable"
		}
		err = nil
	}
	return BiometricStatus(status), err
}

// beginBiometric captures identity under the lifecycle gate, then releases it
// before any native UI. Lock and logout must remain able to cancel a prompt.
func (s *EncryptionService) beginBiometric() (*biometricAttempt, error) {
	ctx, cancel := context.WithTimeout(s.host.appContext(), 2*time.Minute)
	release, err := s.mount.acquireMountLifecycle(ctx)
	if err != nil {
		cancel()
		return nil, err
	}
	defer release()
	scope, service, err := s.biometricScope(ctx)
	if err != nil {
		cancel()
		return nil, err
	}
	provider, err := s.nativeBiometricProvider()
	if err != nil {
		cancel()
		return nil, err
	}
	s.biometricMu.Lock()
	defer s.biometricMu.Unlock()
	if s.biometricPending != nil {
		cancel()
		return nil, errBiometricBusy
	}
	attempt := &biometricAttempt{ctx: ctx, cancel: cancel, epoch: s.biometricEpoch,
		engine: s.host.coreEngine(), scope: scope, service: service, provider: provider}
	s.biometricPending = attempt
	return attempt, nil
}

func (s *EncryptionService) finishBiometric(attempt *biometricAttempt) {
	s.biometricMu.Lock()
	if s.biometricPending == attempt {
		s.biometricPending = nil
	}
	s.biometricMu.Unlock()
	attempt.cancel()
}

func (s *EncryptionService) cancelBiometricAttempt() {
	if s == nil {
		return
	}
	s.biometricMu.Lock()
	s.biometricEpoch++
	if s.biometricPending != nil {
		s.biometricPending.cancel()
	}
	s.biometricMu.Unlock()
}

// validBiometric must be called while holding the mount lifecycle gate.
func (s *EncryptionService) validBiometric(attempt *biometricAttempt) error {
	s.biometricMu.Lock()
	valid := s.biometricPending == attempt && s.biometricEpoch == attempt.epoch && attempt.ctx.Err() == nil
	s.biometricMu.Unlock()
	if !valid {
		return biometric.ErrCanceled
	}
	if s.host.coreEngine() != attempt.engine {
		return biometric.ErrInvalidated
	}
	scope, _, err := s.biometricScope(attempt.ctx)
	if err != nil {
		return err
	}
	if scope != attempt.scope {
		return biometric.ErrInvalidated
	}
	return nil
}

func (s *EncryptionService) EnableBiometricUnlock(password string) OperationResult {
	attempt, err := s.beginBiometric()
	if err != nil {
		return biometricOperationFailure(err)
	}
	defer s.finishBiometric(attempt)
	key, err := attempt.service.BiometricEnrollmentKey(attempt.scope, password)
	defer clear(key)
	if err != nil {
		return biometricOperationFailure(err)
	}
	if err := attempt.ctx.Err(); err != nil {
		return biometricOperationFailure(err)
	}
	err = attempt.provider.Enroll(attempt.ctx, attempt.scope, key)
	if err == nil {
		var release func()
		release, err = s.mount.acquireMountLifecycle(attempt.ctx)
		if err == nil {
			err = s.validBiometric(attempt)
			release()
		}
	}
	if err != nil {
		// A native callback may have written enrollment despite cancellation.
		// Keep this attempt registered until cleanup, preventing replacement.
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if cleanupErr := attempt.provider.Delete(ctx, attempt.scope); cleanupErr != nil {
			return OperationResult{Error: &OperationError{Code: "biometric_cleanup_required",
				Message: "Biometric enrollment could not be removed. Open encryption settings and disable biometric unlock before trying again.",
				cause:   errors.Join(err, cleanupErr)}}
		}
	}
	return biometricOperationFailure(err)
}

func (s *EncryptionService) UseBiometricUnlock() OperationResult {
	attempt, err := s.beginBiometric()
	if err != nil {
		return biometricOperationFailure(err)
	}
	defer s.finishBiometric(attempt)
	key, err := attempt.provider.Unlock(attempt.ctx, attempt.scope)
	defer clear(key)
	if err != nil {
		return biometricOperationFailure(err)
	}
	if attempt.ctx.Err() != nil {
		return biometricOperationFailure(biometric.ErrCanceled)
	}
	release, err := s.mount.acquireMountLifecycle(attempt.ctx)
	if err != nil {
		return biometricOperationFailure(err)
	}
	defer release()
	if err := s.validBiometric(attempt); err != nil {
		return biometricOperationFailure(err)
	}
	return biometricOperationFailure(attempt.service.VerifyBiometricKey(attempt.scope, key))
}

func (s *EncryptionService) CancelBiometricUnlock() OperationResult {
	s.cancelBiometricAttempt()
	return operationSuccess()
}

func (s *EncryptionService) DisableBiometricUnlock() OperationResult {
	s.cancelBiometricAttempt()
	ctx, cancel := context.WithTimeout(s.host.appContext(), encryptionMountTransitionTimeout)
	defer cancel()
	release, err := s.mount.acquireMountLifecycle(ctx)
	if err != nil {
		return biometricOperationFailure(err)
	}
	defer release()
	return biometricOperationFailure(s.deleteBiometricEnrollment(ctx))
}

// deleteBiometricEnrollment is noninteractive and requires the lifecycle gate.
func (s *EncryptionService) deleteBiometricEnrollment(ctx context.Context) error {
	return s.removeBiometricEnrollment(ctx, "", "", false)
}

func (s *EncryptionService) removeBiometricEnrollment(ctx context.Context, currentPassword, newPassword string, passwordChange bool) error {
	scope, service, err := s.biometricScope(ctx)
	if errors.Is(err, encservice.ErrVaultNotConfigured) || errors.Is(err, errBackendUnavailable) || errors.Is(err, biometric.ErrUnavailable) {
		return nil
	}
	if err != nil {
		return err
	}
	provider, err := s.nativeBiometricProvider()
	if err != nil {
		return err
	}
	status, err := provider.Status(ctx, scope)
	if errors.Is(err, biometric.ErrUnavailable) && !status.Enrolled {
		return nil
	}
	if err != nil {
		return err
	}
	if !status.Enrolled {
		return nil
	}
	if passwordChange {
		if err := service.VerifyBiometricPasswordChange(scope, currentPassword, newPassword); err != nil {
			return err
		}
	}
	return provider.Delete(ctx, scope)
}

func biometricOperationFailure(err error) OperationResult {
	if err == nil {
		return operationSuccess()
	}
	if errors.Is(err, encservice.ErrWrongPassword) {
		return operationFailure(err)
	}
	code, message := OperationErrorCode("biometric_failed"), "Biometric unlock failed. Use your encryption password and try again."
	switch {
	case errors.Is(err, errBackendUnavailable):
		return operationFailure(err)
	case errors.Is(err, biometric.ErrCanceled), errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		code, message = "biometric_canceled", "Biometric authentication was canceled. You can use your encryption password."
	case errors.Is(err, biometric.ErrUnavailable):
		code, message = "biometric_unavailable", "Biometric unlock is unavailable on this device. Use your encryption password."
	case errors.Is(err, biometric.ErrInvalidated):
		code, message = "biometric_invalidated", "Biometric enrollment has changed. Use your encryption password and enable biometrics again."
	case errors.Is(err, biometric.ErrNotEnrolled):
		code, message = "biometric_not_enrolled", "Enable biometric unlock in encryption settings first."
	case errors.Is(err, errBiometricBusy):
		code, message = "biometric_busy", "Complete or cancel the current biometric prompt first."
	}
	return OperationResult{Error: &OperationError{Code: code, Message: message, cause: fmt.Errorf("biometric unlock: %w", err)}}
}
