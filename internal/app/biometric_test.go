package app

import (
	"bytes"
	"context"
	"errors"
	"sync"
	"testing"

	"TDrive/backend/biometric"
)

type fakeBiometricProvider struct {
	mu        sync.Mutex
	key       []byte
	scope     biometric.Scope
	unlock    func(context.Context, biometric.Scope) ([]byte, error)
	enroll    func(context.Context, biometric.Scope, []byte) error
	deleteErr error
	deleted   int
}

func (p *fakeBiometricProvider) Status(_ context.Context, scope biometric.Scope) (biometric.Status, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return biometric.Status{Available: true, Enrolled: p.key != nil, Kind: "biometric"}, nil
}

func (p *fakeBiometricProvider) Enroll(ctx context.Context, scope biometric.Scope, key []byte) error {
	if p.enroll != nil {
		return p.enroll(ctx, scope, key)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.key = bytes.Clone(key)
	p.scope = scope
	return nil
}

func (p *fakeBiometricProvider) Unlock(ctx context.Context, scope biometric.Scope) ([]byte, error) {
	if p.unlock != nil {
		return p.unlock(ctx, scope)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.key == nil {
		return nil, biometric.ErrNotEnrolled
	}
	if scope != p.scope {
		return nil, biometric.ErrInvalidated
	}
	return bytes.Clone(p.key), nil
}

func (p *fakeBiometricProvider) Delete(context.Context, biometric.Scope) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.deleted++
	if p.deleteErr != nil {
		return p.deleteErr
	}
	clear(p.key)
	p.key = nil
	return nil
}

func setupBiometricApp(t *testing.T) (*App, *fakeBiometricProvider) {
	t.Helper()
	app, _ := setupEncryptionApp(t)
	t.Cleanup(app.engine.Close)
	app.ctx = t.Context()
	provider := &fakeBiometricProvider{}
	app.encryption.biometricProviderOverride = provider
	if result := app.encryption.CreateEncryptionPassword("vault-password", ""); !result.OK {
		t.Fatal(result.Err())
	}
	return app, provider
}

func TestBiometricUnlockUsesExistingSessionKey(t *testing.T) {
	app, provider := setupBiometricApp(t)
	want, _ := app.engine.EncryptionService().LoadedMasterKey()
	defer clear(want)
	if result := app.encryption.EnableBiometricUnlock("wrong-password"); result.OK {
		t.Fatal("wrong password enrolled")
	}
	if status, _ := provider.Status(t.Context(), biometric.Scope{}); status.Enrolled {
		t.Fatal("failed enrollment persisted a key")
	}
	if result := app.encryption.EnableBiometricUnlock("vault-password"); !result.OK {
		t.Fatal(result.Err())
	}
	if err := app.lockEncryptionSession(); err != nil {
		t.Fatal(err)
	}
	if result := app.encryption.UseBiometricUnlock(); !result.OK {
		t.Fatal(result.Err())
	}
	got, ok := app.engine.EncryptionService().LoadedMasterKey()
	defer clear(got)
	if !ok || !bytes.Equal(got, want) {
		t.Fatal("biometric unlock did not install the original vault key")
	}
	if result := app.encryption.DisableBiometricUnlock(); !result.OK {
		t.Fatal(result.Err())
	}
	if err := app.lockEncryptionSession(); err != nil {
		t.Fatal(err)
	}
	if result := app.encryption.UseEncryptionPassword("vault-password"); !result.OK {
		t.Fatal("password fallback failed", result.Err())
	}
}

func TestBiometricLateSuccessCannotReopenLockedVault(t *testing.T) {
	app, provider := setupBiometricApp(t)
	if result := app.encryption.EnableBiometricUnlock("vault-password"); !result.OK {
		t.Fatal(result.Err())
	}
	app.clearEncryptionSession()
	entered, finish := make(chan struct{}), make(chan struct{})
	provider.unlock = func(context.Context, biometric.Scope) ([]byte, error) {
		key := bytes.Clone(provider.key)
		if !app.mountLifecycle.TryLock() {
			return nil, errors.New("native prompt held mount gate")
		}
		app.mountLifecycle.Unlock()
		close(entered)
		<-finish // Model a native API which delivers success despite cancellation.
		return key, nil
	}
	done := make(chan OperationResult, 1)
	go func() { done <- app.encryption.UseBiometricUnlock() }()
	waitLifecycleSignal(t, entered, "biometric prompt")
	if result := app.encryption.UseBiometricUnlock(); result.OK || result.Error.Code != "biometric_busy" {
		t.Fatalf("concurrent prompt = %+v", result)
	}
	if err := app.lockEncryptionSession(); err != nil {
		t.Fatal(err)
	}
	close(finish)
	result := <-done
	if result.OK || result.Error.Code != "biometric_canceled" {
		t.Fatalf("late native success = %+v", result)
	}
	if key, loaded := app.engine.EncryptionService().LoadedMasterKey(); loaded {
		clear(key)
		t.Fatal("late prompt reopened vault")
	}
}

func TestBiometricCancellationRemovesLateEnrollment(t *testing.T) {
	app, provider := setupBiometricApp(t)
	entered, finish := make(chan struct{}), make(chan struct{})
	provider.enroll = func(_ context.Context, scope biometric.Scope, key []byte) error {
		close(entered)
		<-finish
		provider.mu.Lock()
		provider.key, provider.scope = bytes.Clone(key), scope
		provider.mu.Unlock()
		return nil
	}
	done := make(chan OperationResult, 1)
	go func() { done <- app.encryption.EnableBiometricUnlock("vault-password") }()
	waitLifecycleSignal(t, entered, "enrollment prompt")
	app.encryption.CancelBiometricUnlock()
	close(finish)
	if result := <-done; result.OK {
		t.Fatal("canceled enrollment reported success")
	}
	status, _ := provider.Status(t.Context(), biometric.Scope{})
	if status.Enrolled {
		t.Fatal("late enrollment persisted after cancellation")
	}
}

func TestBiometricPasswordChangeDeletesEnrollment(t *testing.T) {
	app, provider := setupBiometricApp(t)
	if result := app.encryption.EnableBiometricUnlock("vault-password"); !result.OK {
		t.Fatal(result.Err())
	}
	if result := app.encryption.ChangeEncryptionPassword("vault-password", "replacement-password", ""); !result.OK {
		t.Fatal(result.Err())
	}
	status, _ := provider.Status(t.Context(), biometric.Scope{})
	if status.Enrolled {
		t.Fatal("password change left native enrollment")
	}
	if result := app.encryption.UseEncryptionPassword("replacement-password"); !result.OK {
		t.Fatal(result.Err())
	}
}

func TestBiometricLogoutCleanupFailureKeepsLogoutRetryable(t *testing.T) {
	app, provider := setupBiometricApp(t)
	if result := app.encryption.EnableBiometricUnlock("vault-password"); !result.OK {
		t.Fatal(result.Err())
	}
	provider.deleteErr = errors.New("native deletion failed")
	cleaned := false
	err := app.runWithClosedMountForLogout(func() error { cleaned = true; return nil })
	if err == nil || cleaned || app.mountLifecycleTerminal {
		t.Fatal("logout discarded account state after failed biometric cleanup")
	}
	if key, loaded := app.engine.EncryptionService().LoadedMasterKey(); loaded {
		clear(key)
		t.Fatal("logout cleanup failure left vault unlocked")
	}
	provider.deleteErr = nil
	if err := app.runWithClosedMountForLogout(func() error { cleaned = true; return nil }); err != nil {
		t.Fatal(err)
	}
	if !cleaned || !app.mountLifecycleTerminal {
		t.Fatal("successful retry did not complete logout")
	}
}
