//go:build (darwin || ios) && cgo

package biometric

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
)

type fakeAppleNative struct {
	statusValue Status
	secret      []byte
	enrolled    []byte
	account     string
	unlocked    int
	deleted     int
	cancel      context.CancelFunc
	err         error
}

func (f *fakeAppleNative) status(id string) (Status, error) {
	f.account = id
	return f.statusValue, f.err
}

func (f *fakeAppleNative) enroll(_ context.Context, id string, secret []byte) error {
	f.account, f.enrolled = id, bytes.Clone(secret)
	return f.err
}

func (f *fakeAppleNative) unlock(_ context.Context, id string) ([]byte, error) {
	f.account = id
	f.unlocked++
	if f.cancel != nil {
		f.cancel()
	}
	return f.secret, f.err
}

func (f *fakeAppleNative) delete(_ context.Context, id string) error {
	f.account = id
	f.deleted++
	return f.err
}

func appleTestScope() Scope {
	return Scope{AccountID: 101, ChannelID: 202, ConfigHash: strings.Repeat("a", 64)}
}

func TestAppleEnrollmentPassesScopedSecretWithoutChangingKey(t *testing.T) {
	scope := appleTestScope()
	key := bytes.Repeat([]byte{42}, 32)
	bridge := &fakeAppleNative{}
	provider := &appleProvider{native: bridge}
	if err := provider.Enroll(t.Context(), scope, key); err != nil {
		t.Fatal(err)
	}
	if bridge.account != scope.ID() {
		t.Fatalf("stored account = %q, want scoped identifier %q", bridge.account, scope.ID())
	}
	decoded, err := DecodeSecret(scope, bridge.enrolled)
	if err != nil || !bytes.Equal(decoded, key) {
		t.Fatalf("stored secret does not contain the scoped key: %v", err)
	}
	clear(decoded)
	if !bytes.Equal(key, bytes.Repeat([]byte{42}, 32)) {
		t.Fatal("enrollment changed caller's key")
	}
}

func TestAppleUnlockClearsNativeSecretAndRejectsChangedVault(t *testing.T) {
	scope := appleTestScope()
	key := bytes.Repeat([]byte{42}, 32)
	for _, changed := range []bool{false, true} {
		t.Run(map[bool]string{false: "same vault", true: "changed vault"}[changed], func(t *testing.T) {
			secret, err := EncodeSecret(scope, key)
			if err != nil {
				t.Fatal(err)
			}
			bridge := &fakeAppleNative{secret: secret}
			provider := &appleProvider{native: bridge}
			if changed {
				scope.ConfigHash = strings.Repeat("b", 64)
			}
			got, err := provider.Unlock(t.Context(), scope)
			if changed && !errors.Is(err, ErrInvalidated) {
				t.Fatalf("changed vault error = %v, want ErrInvalidated", err)
			}
			if !changed && (err != nil || !bytes.Equal(got, key)) {
				t.Fatalf("unlocked key = %x, error = %v", got, err)
			}
			clear(got)
			if !bytes.Equal(secret, make([]byte, len(secret))) {
				t.Fatal("native secret was retained after unlock")
			}
		})
	}
}

func TestAppleUnlockRejectsLateAuthenticationAfterCancellation(t *testing.T) {
	scope := appleTestScope()
	secret, err := EncodeSecret(scope, bytes.Repeat([]byte{42}, 32))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	bridge := &fakeAppleNative{secret: secret, cancel: cancel}
	provider := &appleProvider{native: bridge}
	key, err := provider.Unlock(ctx, scope)
	if key != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("late authentication returned key %x, error %v", key, err)
	}
	if !bytes.Equal(secret, make([]byte, len(secret))) {
		t.Fatal("late authentication retained the native secret")
	}
}

func TestAppleProviderRejectsInvalidScopeAndCanceledWorkBeforeNativeAccess(t *testing.T) {
	bridge := &fakeAppleNative{}
	provider := &appleProvider{native: bridge}
	if _, err := provider.Status(t.Context(), Scope{}); err == nil {
		t.Fatal("invalid scope accepted")
	}
	if _, err := provider.Unlock(t.Context(), Scope{}); err == nil {
		t.Fatal("invalid unlock scope accepted")
	}
	if err := provider.Delete(t.Context(), Scope{}); err == nil {
		t.Fatal("invalid delete scope accepted")
	}
	if err := provider.Enroll(t.Context(), appleTestScope(), []byte{1}); err == nil {
		t.Fatal("invalid key accepted")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := provider.Status(ctx, appleTestScope()); !errors.Is(err, context.Canceled) {
		t.Fatalf("status error = %v", err)
	}
	if _, err := provider.Unlock(ctx, appleTestScope()); !errors.Is(err, context.Canceled) {
		t.Fatalf("unlock error = %v", err)
	}
	if err := provider.Delete(ctx, appleTestScope()); !errors.Is(err, context.Canceled) {
		t.Fatalf("delete error = %v", err)
	}
	if err := provider.Enroll(ctx, appleTestScope(), make([]byte, 32)); !errors.Is(err, context.Canceled) {
		t.Fatalf("enroll error = %v", err)
	}
	if bridge.account != "" || bridge.unlocked != 0 || bridge.deleted != 0 {
		t.Fatal("invalid or canceled work reached the native provider")
	}
}

func TestAppleStatusAndDeletePreserveNativeErrors(t *testing.T) {
	scope := appleTestScope()
	bridge := &fakeAppleNative{statusValue: Status{Available: true, Enrolled: true, Kind: "Touch ID"}}
	provider := &appleProvider{native: bridge}
	status, err := provider.Status(t.Context(), scope)
	if err != nil || status != bridge.statusValue {
		t.Fatalf("status = %+v, error = %v", status, err)
	}
	bridge.err = ErrUnavailable
	if err := provider.Delete(t.Context(), scope); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("delete error = %v, want ErrUnavailable", err)
	}
	bridge.secret = []byte{42}
	if _, err := provider.Unlock(t.Context(), scope); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("unlock error = %v, want ErrUnavailable", err)
	}
	if bridge.secret[0] != 0 {
		t.Fatal("failed native read retained secret bytes")
	}
}

func TestAppleErrorMapping(t *testing.T) {
	for _, tc := range []struct {
		name string
		code int
		want error
	}{
		{"success", 0, nil},
		{"unavailable", 1, ErrUnavailable},
		{"canceled", 2, ErrCanceled},
		{"lockout", 3, ErrUnavailable},
		{"invalidated", 4, ErrInvalidated},
		{"not enrolled", 5, ErrNotEnrolled},
		{"missing signing", 6, ErrUnavailable},
		{"native failure", 7, ErrUnavailable},
		{"unknown result", 999, ErrUnavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := appleError(tc.code); !errors.Is(err, tc.want) {
				t.Fatalf("mapped error = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestAppleStatusPreservesEnrollmentDuringTemporaryFailures(t *testing.T) {
	for _, tc := range []struct {
		name   string
		code   int
		reason string
	}{
		{"available", 0, ""},
		{"hardware unavailable", 1, "unavailable"},
		{"prompt canceled", 2, "unavailable"},
		{"locked out", 3, "locked_out"},
		{"invalidated", 4, "invalidated"},
		{"missing record", 5, "not_enrolled"},
		{"missing provisioned identity", 6, "signing_required"},
		{"unknown platform error", 999, "unavailable"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			status := appleStatus(tc.code, true, 1)
			if status.Available != (tc.code == 0) || !status.Enrolled || status.Kind != "touch_id" || status.Reason != tc.reason {
				t.Fatalf("status = %+v, want enrolled Touch ID with reason %q", status, tc.reason)
			}
		})
	}
	if status := appleStatus(0, false, 2); status.Kind != "face_id" || status.Reason != "not_enrolled" {
		t.Fatalf("Face ID without local enrollment = %+v", status)
	}
	if status := appleStatus(1, false, 0); status.Kind != "biometric" {
		t.Fatalf("unknown hardware kind = %q", status.Kind)
	}
}

func TestAppleNativeProviderFailsClosedWithoutProvisionedIdentity(t *testing.T) {
	// go test's executable has no provisioned app identity. This exercises the
	// real native capability gate without reading personal Keychain items or
	// showing a biometric prompt.
	provider := NewProvider(t.TempDir())
	status, err := provider.Status(t.Context(), appleTestScope())
	if err != nil || status.Available || status.Enrolled || status.Reason != "signing_required" {
		t.Fatalf("unprovisioned test executable status = %+v, error = %v", status, err)
	}
	bridge := appleKeychain{}
	if err := bridge.delete(t.Context(), appleTestScope().ID()); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("unprovisioned delete error = %v, want ErrUnavailable", err)
	}
}

func TestAppleNativeCanceledOperationsDoNotRequestAuthentication(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	bridge := appleKeychain{}
	secret := bytes.Repeat([]byte{42}, 64)
	if err := bridge.enroll(ctx, appleTestScope().ID(), secret); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled native enrollment error = %v", err)
	}
	if key, err := bridge.unlock(ctx, appleTestScope().ID()); key != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled native unlock returned %x, error %v", key, err)
	}
	if err := bridge.delete(ctx, appleTestScope().ID()); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled native delete error = %v", err)
	}
	if !bytes.Equal(secret, bytes.Repeat([]byte{42}, 64)) {
		t.Fatal("canceled native enrollment modified caller's bytes")
	}
}

func TestAppleAuthenticationContextCleanupJoinsCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	_, cleanup := appleAuthenticationContext(ctx)
	cancel()
	// Invalidation runs in a cancellation callback; cleanup must join it before
	// releasing LAContext. The race detector covers concurrent cleanup ownership.
	cleanup()
}
