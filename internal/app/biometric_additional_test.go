package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"TDrive/backend"
	"TDrive/backend/auth"
	"TDrive/backend/biometric"
	"TDrive/backend/datadir"
	"TDrive/backend/projection"
)

type biometricProviderBoundary struct {
	*fakeBiometricProvider
	status func(context.Context, biometric.Scope) (biometric.Status, error)
	remove func(context.Context, biometric.Scope) error
}

func (p *biometricProviderBoundary) Status(ctx context.Context, scope biometric.Scope) (biometric.Status, error) {
	if p.status != nil {
		return p.status(ctx, scope)
	}
	return p.fakeBiometricProvider.Status(ctx, scope)
}

func (p *biometricProviderBoundary) Delete(ctx context.Context, scope biometric.Scope) error {
	if p.remove != nil {
		return p.remove(ctx, scope)
	}
	return p.fakeBiometricProvider.Delete(ctx, scope)
}

func waitBiometricResult(t *testing.T, done <-chan OperationResult) OperationResult {
	t.Helper()
	select {
	case result := <-done:
		return result
	case <-time.After(5 * time.Second):
		t.Fatal("biometric operation did not finish")
		return OperationResult{}
	}
}

// startDelayedBiometricUnlock models a native callback that ignores cancellation.
// Cleanup always releases and joins it before the engine/database is closed.
func startDelayedBiometricUnlock(t *testing.T, app *App, provider *fakeBiometricProvider, key []byte) (func(), <-chan OperationResult, []byte) {
	t.Helper()
	entered, release, finished := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	returned := bytes.Clone(key)
	provider.unlock = func(context.Context, biometric.Scope) ([]byte, error) {
		close(entered)
		<-release
		return returned, nil
	}
	done := make(chan OperationResult, 1)
	go func() {
		done <- app.encryption.UseBiometricUnlock()
		close(finished)
	}()
	t.Cleanup(func() {
		unblock()
		waitLifecycleSignal(t, finished, "native callback cleanup")
	})
	waitLifecycleSignal(t, entered, "native unlock callback")
	return unblock, done, returned
}

func TestBiometricWrongPasswordChangePreservesEnrollment(t *testing.T) {
	app, provider := setupBiometricApp(t)
	if result := app.encryption.EnableBiometricUnlock("vault-password"); !result.OK {
		t.Fatal(result.Err())
	}
	if result := app.encryption.ChangeEncryptionPassword("incorrect-password", "replacement-password", ""); result.OK || result.Error.Code != OperationCodeInvalidEncryptionPassword {
		t.Fatalf("wrong password change = %+v", result)
	}
	status, err := app.encryption.BiometricStatus()
	if err != nil || !status.Enrolled {
		t.Fatalf("wrong password removed enrollment: %+v, %v", status, err)
	}
	if err := app.lockEncryptionSession(); err != nil {
		t.Fatal(err)
	}
	if result := app.encryption.UseBiometricUnlock(); !result.OK {
		t.Fatalf("unchanged biometric enrollment no longer unlocks: %v", result.Err())
	}
	provider.mu.Lock()
	deletions := provider.deleted
	provider.mu.Unlock()
	if deletions != 0 {
		t.Fatal("failed password authorization removed device enrollment")
	}
}

func TestBiometricInvalidReplacementPasswordPreservesEnrollment(t *testing.T) {
	app, _ := setupBiometricApp(t)
	if result := app.encryption.EnableBiometricUnlock("vault-password"); !result.OK {
		t.Fatal(result.Err())
	}
	if result := app.encryption.ChangeEncryptionPassword("vault-password", "short", ""); result.OK {
		t.Fatal("invalid replacement password was accepted")
	}
	status, err := app.encryption.BiometricStatus()
	if err != nil || !status.Enrolled {
		t.Fatalf("invalid replacement password removed enrollment: %+v, %v", status, err)
	}
	if err := app.lockEncryptionSession(); err != nil {
		t.Fatal(err)
	}
	if result := app.encryption.UseBiometricUnlock(); !result.OK {
		t.Fatalf("failed password change broke existing biometric unlock: %v", result.Err())
	}
}

func TestBiometricLateCallbackRejectsRewrappedConfig(t *testing.T) {
	app, provider := setupBiometricApp(t)
	key, _ := app.engine.EncryptionService().LoadedMasterKey()
	defer clear(key)
	app.clearEncryptionSession()
	unblock, done, returned := startDelayedBiometricUnlock(t, app, provider, key)
	// Change through the backend to model a configuration replacement arriving
	// while native UI is open, without invoking the app's prompt cancellation.
	if err := app.engine.EncryptionService().ChangePassword("vault-password", "replacement-password", ""); err != nil {
		t.Fatal(err)
	}
	app.engine.EncryptionService().Clear()
	unblock()
	if result := waitBiometricResult(t, done); result.OK || result.Error.Code != "biometric_invalidated" {
		t.Fatalf("native success for previous config = %+v", result)
	}
	if loaded, ok := app.engine.EncryptionService().LoadedMasterKey(); ok {
		clear(loaded)
		t.Fatal("stale native success unlocked the replacement config")
	}
	if !bytes.Equal(returned, make([]byte, 32)) {
		t.Fatal("stale native result retained master-key bytes")
	}
	if result := app.encryption.UseEncryptionPassword("replacement-password"); !result.OK {
		t.Fatalf("new password fallback failed: %v", result.Err())
	}
}

func TestBiometricWrongKeyPreservesAuthorizedSession(t *testing.T) {
	app, provider := setupBiometricApp(t)
	want, _ := app.engine.EncryptionService().LoadedMasterKey()
	defer clear(want)
	wrongKey := bytes.Repeat([]byte{0x42}, 32)
	provider.unlock = func(context.Context, biometric.Scope) ([]byte, error) { return wrongKey, nil }
	if result := app.encryption.UseBiometricUnlock(); result.OK || result.Error.Code != "biometric_invalidated" {
		t.Fatalf("wrong native master key = %+v", result)
	}
	got, ok := app.engine.EncryptionService().LoadedMasterKey()
	defer clear(got)
	if !ok || !bytes.Equal(got, want) {
		t.Fatal("failed biometric authentication replaced the authorized session key")
	}
	if !bytes.Equal(wrongKey, make([]byte, 32)) {
		t.Fatal("rejected native master key was not cleared")
	}
}

func TestBiometricDisableRejectsLateNativeSuccess(t *testing.T) {
	app, provider := setupBiometricApp(t)
	if result := app.encryption.EnableBiometricUnlock("vault-password"); !result.OK {
		t.Fatal(result.Err())
	}
	key, _ := app.engine.EncryptionService().LoadedMasterKey()
	defer clear(key)
	app.clearEncryptionSession()
	unblock, done, _ := startDelayedBiometricUnlock(t, app, provider, key)
	if result := app.encryption.DisableBiometricUnlock(); !result.OK {
		t.Fatal(result.Err())
	}
	unblock()
	if result := waitBiometricResult(t, done); result.OK || result.Error.Code != "biometric_canceled" {
		t.Fatalf("disabled enrollment late callback = %+v", result)
	}
	status, err := app.encryption.BiometricStatus()
	if err != nil || status.Enrolled {
		t.Fatalf("disabled enrollment remains visible: %+v, %v", status, err)
	}
	if key, loaded := app.engine.EncryptionService().LoadedMasterKey(); loaded {
		clear(key)
		t.Fatal("disabled enrollment's late result unlocked the vault")
	}
}

func TestBiometricLogoutRejectsLateNativeSuccess(t *testing.T) {
	app, provider := setupBiometricApp(t)
	if result := app.encryption.EnableBiometricUnlock("vault-password"); !result.OK {
		t.Fatal(result.Err())
	}
	key, _ := app.engine.EncryptionService().LoadedMasterKey()
	defer clear(key)
	app.clearEncryptionSession()
	unblock, done, _ := startDelayedBiometricUnlock(t, app, provider, key)
	if err := app.runWithClosedMountForLogout(func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	unblock()
	if result := waitBiometricResult(t, done); result.OK || result.Error.Code != "biometric_canceled" {
		t.Fatalf("logout late callback = %+v", result)
	}
	if key, loaded := app.engine.EncryptionService().LoadedMasterKey(); loaded {
		clear(key)
		t.Fatal("late biometric result resurrected the logged-out session")
	}
	status, _ := provider.Status(t.Context(), biometric.Scope{})
	if status.Enrolled {
		t.Fatal("logout retained device enrollment")
	}
}

func TestBiometricPasswordFallbackCancelsPendingNativeResult(t *testing.T) {
	app, provider := setupBiometricApp(t)
	want, _ := app.engine.EncryptionService().LoadedMasterKey()
	defer clear(want)
	app.clearEncryptionSession()
	unblock, done, returned := startDelayedBiometricUnlock(t, app, provider, want)
	if result := app.encryption.UseEncryptionPassword("vault-password"); !result.OK {
		t.Fatal(result.Err())
	}
	unblock()
	if result := waitBiometricResult(t, done); result.OK || result.Error.Code != "biometric_canceled" {
		t.Fatalf("native result after password fallback = %+v", result)
	}
	got, loaded := app.engine.EncryptionService().LoadedMasterKey()
	defer clear(got)
	if !loaded || !bytes.Equal(got, want) || !bytes.Equal(returned, make([]byte, 32)) {
		t.Fatal("late result changed password-unlocked session or retained key bytes")
	}
}

func TestBiometricPersonalDriveSwitchRejectsPendingNativeKey(t *testing.T) {
	app, provider := setupBiometricApp(t)
	key, _ := app.engine.EncryptionService().LoadedMasterKey()
	defer clear(key)
	app.clearEncryptionSession()
	unblock, done, _ := startDelayedBiometricUnlock(t, app, provider, key)
	if err := projection.MigratePersonalChannel(backend.DB, testEncryptionChannelID+1); err != nil {
		t.Fatal(err)
	}
	if err := auth.SaveConfig(testEncryptionChannelID + 1); err != nil {
		t.Fatal(err)
	}
	unblock()
	if result := waitBiometricResult(t, done); result.OK {
		t.Fatal("previous personal drive's native key unlocked a different drive")
	}
	if loaded, ok := app.engine.EncryptionService().LoadedMasterKey(); ok {
		clear(loaded)
		t.Fatal("personal-drive switch accepted the previous drive's native key")
	}
}

func TestBiometricBackendRemovalRejectsPendingNativeKey(t *testing.T) {
	app, provider := setupBiometricApp(t)
	engine := app.engine
	key, _ := engine.EncryptionService().LoadedMasterKey()
	defer clear(key)
	app.clearEncryptionSession()
	unblock, done, returned := startDelayedBiometricUnlock(t, app, provider, key)
	app.engine = nil
	unblock()
	if result := waitBiometricResult(t, done); result.OK || result.Error.Code != "biometric_invalidated" {
		t.Fatalf("previous backend's native callback = %+v", result)
	}
	if loaded, ok := engine.EncryptionService().LoadedMasterKey(); ok {
		clear(loaded)
		t.Fatal("detached backend accepted the native key")
	}
	if !bytes.Equal(returned, make([]byte, 32)) {
		t.Fatal("detached backend's native key was not cleared")
	}
}

func TestBiometricUnavailableStatusKeepsPasswordFallback(t *testing.T) {
	app, provider := setupBiometricApp(t)
	app.encryption.biometricProviderOverride = &biometricProviderBoundary{
		fakeBiometricProvider: provider,
		status: func(context.Context, biometric.Scope) (biometric.Status, error) {
			return biometric.Status{Kind: "Windows Hello", Reason: "unsupported_device"}, biometric.ErrUnavailable
		},
	}
	status, err := app.encryption.BiometricStatus()
	if err != nil || status.Available || status.Kind != "Windows Hello" || status.Reason != "unsupported_device" {
		t.Fatalf("unavailable capability should remain readable: %+v, %v", status, err)
	}
	app.clearEncryptionSession()
	if result := app.encryption.UseEncryptionPassword("vault-password"); !result.OK {
		t.Fatalf("unsupported biometric device lost password fallback: %v", result.Err())
	}
}

func TestBiometricCanceledEnrollmentReportsRequiredCleanup(t *testing.T) {
	app, provider := setupBiometricApp(t)
	provider.enroll = func(_ context.Context, scope biometric.Scope, key []byte) error {
		provider.mu.Lock()
		provider.key, provider.scope = bytes.Clone(key), scope
		provider.mu.Unlock()
		app.encryption.CancelBiometricUnlock()
		return nil
	}
	provider.deleteErr = errors.New("private native rollback failure")
	result := app.encryption.EnableBiometricUnlock("vault-password")
	if result.OK || result.Error.Code != "biometric_cleanup_required" {
		t.Fatalf("leftover native enrollment did not require cleanup: %+v", result)
	}
	if !strings.Contains(result.Error.Message, "disable biometric unlock") || strings.Contains(result.Error.Message, provider.deleteErr.Error()) {
		t.Fatalf("unsafe or missing cleanup guidance: %q", result.Error.Message)
	}
	status, err := app.encryption.BiometricStatus()
	if err != nil || !status.Enrolled {
		t.Fatalf("remaining enrollment was hidden: %+v, %v", status, err)
	}
	provider.deleteErr = nil
	if result := app.encryption.DisableBiometricUnlock(); !result.OK {
		t.Fatal(result.Err())
	}
}

func TestBiometricUnavailableWithoutEnrollmentPermitsPasswordChangeAndLogout(t *testing.T) {
	app, provider := setupBiometricApp(t)
	app.encryption.biometricProviderOverride = &biometricProviderBoundary{
		fakeBiometricProvider: provider,
		status: func(context.Context, biometric.Scope) (biometric.Status, error) {
			return biometric.Status{Kind: "Windows Hello", Reason: "unsupported_device"}, biometric.ErrUnavailable
		},
	}
	if result := app.encryption.ChangeEncryptionPassword("vault-password", "replacement-password", ""); !result.OK {
		t.Fatalf("optional unavailable biometrics blocked password change: %v", result.Err())
	}
	app.clearEncryptionSession()
	if result := app.encryption.UseEncryptionPassword("replacement-password"); !result.OK {
		t.Fatalf("changed password fallback failed: %v", result.Err())
	}
	cleaned := false
	if err := app.runWithClosedMountForLogout(func() error { cleaned = true; return nil }); err != nil || !cleaned {
		t.Fatalf("optional unavailable biometrics blocked logout: cleanup=%v, err=%v", cleaned, err)
	}
}

func TestBiometricUnavailableWithEnrollmentDoesNotSkipRequiredCleanup(t *testing.T) {
	app, provider := setupBiometricApp(t)
	if result := app.encryption.EnableBiometricUnlock("vault-password"); !result.OK {
		t.Fatal(result.Err())
	}
	provider.deleteErr = errors.New("native key storage cannot be removed")
	app.encryption.biometricProviderOverride = &biometricProviderBoundary{
		fakeBiometricProvider: provider,
		status: func(context.Context, biometric.Scope) (biometric.Status, error) {
			return biometric.Status{Enrolled: true, Reason: "temporarily_unavailable"}, biometric.ErrUnavailable
		},
	}
	cleaned := false
	err := app.runWithClosedMountForLogout(func() error { cleaned = true; return nil })
	if err == nil || cleaned || app.mountLifecycleTerminal {
		t.Fatal("unavailable sensor skipped cleanup of existing native key storage")
	}
	status, _ := provider.Status(t.Context(), biometric.Scope{})
	if !status.Enrolled {
		t.Fatal("fixture failed to retain enrollment while cleanup was unavailable")
	}
	if key, loaded := app.engine.EncryptionService().LoadedMasterKey(); loaded {
		clear(key)
		t.Fatal("cleanup failure left encryption unlocked")
	}
}

func TestBiometricWireStatusAndFailuresExcludeSecrets(t *testing.T) {
	app, provider := setupBiometricApp(t)
	if result := app.encryption.EnableBiometricUnlock("vault-password"); !result.OK {
		t.Fatal(result.Err())
	}
	status, err := app.encryption.BiometricStatus()
	if err != nil || !status.Enrolled || !status.Available {
		t.Fatalf("enrolled status = %+v, %v", status, err)
	}
	encoded, err := json.Marshal(status)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &fields); err != nil || len(fields) != 4 {
		t.Fatalf("capability wire data contains extra fields: %s, %v", encoded, err)
	}
	for _, field := range []string{"available", "enrolled", "kind", "reason"} {
		if _, present := fields[field]; !present {
			t.Fatalf("capability field %q missing", field)
		}
	}
	privateDetail := "secret-native-key-material /private/device/vault"
	cases := []struct {
		name string
		err  error
		code OperationErrorCode
	}{
		{"unavailable", biometric.ErrUnavailable, "biometric_unavailable"},
		{"canceled", biometric.ErrCanceled, "biometric_canceled"},
		{"expired", context.DeadlineExceeded, "biometric_canceled"},
		{"invalidated", biometric.ErrInvalidated, "biometric_invalidated"},
		{"not enrolled", biometric.ErrNotEnrolled, "biometric_not_enrolled"},
		{"native failure", errors.New(privateDetail), "biometric_failed"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			provider.unlock = func(context.Context, biometric.Scope) ([]byte, error) {
				return nil, errors.Join(test.err, errors.New(privateDetail))
			}
			result := app.encryption.UseBiometricUnlock()
			if result.OK || result.Error.Code != test.code || !errors.Is(result.Err(), test.err) {
				t.Fatalf("native failure result = %+v", result)
			}
			encoded, err := json.Marshal(result)
			if err != nil || strings.Contains(string(encoded), privateDetail) || strings.Contains(string(encoded), "vault-password") {
				t.Fatalf("native details entered frontend response: %s, %v", encoded, err)
			}
		})
	}
}

func TestBiometricCanceledEnrollmentCleanupBlocksReplacement(t *testing.T) {
	app, provider := setupBiometricApp(t)
	enrollEntered, enrollFinish := make(chan struct{}), make(chan struct{})
	deleteEntered, deleteFinish := make(chan struct{}), make(chan struct{})
	finished := make(chan struct{})
	var enrollOnce, deleteOnce sync.Once
	finishEnroll := func() { enrollOnce.Do(func() { close(enrollFinish) }) }
	finishDelete := func() { deleteOnce.Do(func() { close(deleteFinish) }) }
	t.Cleanup(func() {
		finishEnroll()
		finishDelete()
		waitLifecycleSignal(t, finished, "enrollment cleanup completion")
	})
	provider.enroll = func(_ context.Context, scope biometric.Scope, key []byte) error {
		close(enrollEntered)
		<-enrollFinish
		provider.mu.Lock()
		provider.key, provider.scope = bytes.Clone(key), scope
		provider.mu.Unlock()
		return nil
	}
	app.encryption.biometricProviderOverride = &biometricProviderBoundary{
		fakeBiometricProvider: provider,
		remove: func(ctx context.Context, scope biometric.Scope) error {
			close(deleteEntered)
			<-deleteFinish
			return provider.Delete(ctx, scope)
		},
	}
	done := make(chan OperationResult, 1)
	go func() {
		done <- app.encryption.EnableBiometricUnlock("vault-password")
		close(finished)
	}()
	waitLifecycleSignal(t, enrollEntered, "enrollment callback")
	app.encryption.CancelBiometricUnlock()
	finishEnroll()
	waitLifecycleSignal(t, deleteEntered, "canceled native enrollment cleanup")
	if result := app.encryption.EnableBiometricUnlock("vault-password"); result.OK || result.Error.Code != "biometric_busy" {
		t.Fatalf("replacement overlapped previous cleanup: %+v", result)
	}
	finishDelete()
	if result := waitBiometricResult(t, done); result.OK || result.Error.Code != "biometric_canceled" {
		t.Fatalf("canceled enrollment result = %+v", result)
	}
	status, _ := provider.Status(t.Context(), biometric.Scope{})
	if status.Enrolled {
		t.Fatal("native cleanup left the canceled enrollment usable")
	}
	provider.enroll = nil
	app.encryption.biometricProviderOverride = provider
	if result := app.encryption.EnableBiometricUnlock("vault-password"); !result.OK {
		t.Fatalf("enrollment remained blocked after cleanup: %v", result.Err())
	}
}

func TestBiometricCapabilityBeforeVaultOrBackendIsReady(t *testing.T) {
	app, _ := setupEncryptionApp(t)
	t.Cleanup(app.engine.Close)
	app.ctx = t.Context()
	app.encryption.biometricProviderOverride = &fakeBiometricProvider{}
	status, err := app.encryption.BiometricStatus()
	if err != nil || status.Enrolled || status.Reason != "not_enrolled" {
		t.Fatalf("vault setup capability = %+v, %v", status, err)
	}
	if result := app.encryption.EnableBiometricUnlock("vault-password"); result.OK {
		t.Fatal("device enrollment created a vault before password setup")
	}
	app.engine = nil
	if _, err := app.encryption.BiometricStatus(); !errors.Is(err, errBackendUnavailable) {
		t.Fatalf("missing backend status error = %v", err)
	}
	if result := app.encryption.UseBiometricUnlock(); result.OK || result.Error.Code != OperationCodeBackendUnavailable {
		t.Fatalf("missing backend unlock = %+v", result)
	}
	if result := app.encryption.DisableBiometricUnlock(); !result.OK {
		t.Fatalf("missing backend should permit local no-op disable: %v", result.Err())
	}
}

func TestBiometricNativeProviderStorageCanRecoverWithoutPrompting(t *testing.T) {
	app, _ := setupBiometricApp(t)
	app.encryption.biometricProviderOverride = nil
	privateFile := filepath.Join(t.TempDir(), "private-device-storage")
	if err := os.WriteFile(privateFile, []byte("synthetic blocked storage"), 0o600); err != nil {
		t.Fatal(err)
	}
	datadir.Set(privateFile)
	t.Cleanup(func() { datadir.Set("") })
	if provider, err := app.encryption.nativeBiometricProvider(); err == nil || provider != nil {
		t.Fatalf("unusable native storage returned provider=%v, err=%v", provider != nil, err)
	}
	datadir.Set("")
	first, err := app.encryption.nativeBiometricProvider()
	if err != nil || first == nil {
		t.Fatalf("native provider did not recover: provider=%v, err=%v", first != nil, err)
	}
	second, err := app.encryption.nativeBiometricProvider()
	if err != nil || first != second {
		t.Fatal("native provider recreation lost the existing native owner")
	}
	if result := app.encryption.UseEncryptionPassword("vault-password"); !result.OK {
		t.Fatalf("recovered native storage broke password fallback: %v", result.Err())
	}
}
