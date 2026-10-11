package biometric

import (
	"bytes"
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
)

type fakeWindowsHello struct {
	key           *rsa.PrivateKey
	enrolled      bool
	supported     bool
	signs         int
	signErr       error
	supportedErr  error
	existsErr     error
	createErr     error
	deleteErr     error
	afterSign     func()
	lastSignature []byte
	lastMessage   []byte
}

func newFakeWindowsHello(t *testing.T) *fakeWindowsHello {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	return &fakeWindowsHello{key: key, supported: true}
}

func (f *fakeWindowsHello) Supported(context.Context) (bool, error) {
	return f.supported, f.supportedErr
}
func (f *fakeWindowsHello) Create(context.Context, string) error {
	f.enrolled = true
	return f.createErr
}
func (f *fakeWindowsHello) Exists(context.Context, string) (bool, error) {
	return f.enrolled, f.existsErr
}
func (f *fakeWindowsHello) Delete(context.Context, string) error {
	if f.deleteErr != nil {
		return f.deleteErr
	}
	f.enrolled = false
	return nil
}
func (f *fakeWindowsHello) Sign(_ context.Context, _ string, message []byte) ([]byte, []byte, error) {
	f.signs++
	f.lastMessage = bytes.Clone(message)
	if f.signErr != nil {
		return nil, nil, f.signErr
	}
	digest := sha256.Sum256(message)
	signature, err := rsa.SignPKCS1v15(rand.Reader, f.key, crypto.SHA256, digest[:])
	if err != nil {
		return nil, nil, err
	}
	public, err := x509.MarshalPKIXPublicKey(&f.key.PublicKey)
	if err != nil {
		return nil, nil, err
	}
	f.lastSignature = signature
	if f.afterSign != nil {
		f.afterSign()
	}
	return signature, public, nil
}

func TestWindowsHelloEnrollmentStoresOnlyCiphertextAndSignsEachUnlock(t *testing.T) {
	hello := newFakeWindowsHello(t)
	provider := newWindowsProvider(t.TempDir(), hello)
	scope := testScope()
	master := bytes.Repeat([]byte{0x41}, 32)
	if err := provider.Enroll(t.Context(), scope, master); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(master, bytes.Repeat([]byte{0x41}, 32)) {
		t.Fatal("enrollment cleared caller-owned master key")
	}
	onDisk, err := os.ReadFile(provider.recordPath(scope))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(onDisk, master) {
		t.Fatal("master key persisted in plaintext")
	}
	record, err := provider.readRecord(scope)
	if err != nil {
		t.Fatal(err)
	}
	expectedMessage := append([]byte("tdrive/windows-hello/envelope/v1\x00"+scope.ID()+"\x00"+scope.ConfigHash), record.Challenge...)
	if !bytes.Equal(hello.lastMessage, expectedMessage) {
		t.Fatal("native signing message omitted the envelope domain or vault binding")
	}
	if !bytes.Equal(hello.lastSignature, make([]byte, 256)) {
		t.Fatal("signature buffer was retained after enrollment")
	}
	status, err := provider.Status(t.Context(), scope)
	if err != nil || !status.Available || !status.Enrolled || status.Kind != "windows_hello" {
		t.Fatalf("enrollment status = %+v, %v", status, err)
	}
	for range 2 {
		key, err := provider.Unlock(t.Context(), scope)
		if err != nil || !bytes.Equal(key, master) {
			t.Fatalf("unlock key differs: %v", err)
		}
		clear(key)
	}
	if hello.signs != 3 {
		t.Fatalf("unlock reused cached signing output: %d signs", hello.signs)
	}
	if err := provider.Delete(t.Context(), scope); err != nil {
		t.Fatal(err)
	}
	if hello.enrolled {
		t.Fatal("native credential survived delete")
	}
	if _, err := os.Stat(provider.recordPath(scope)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("record survived delete: %v", err)
	}
}

func TestWindowsHelloRejectsScopeChangesTamperingAndMissingCredential(t *testing.T) {
	for _, scenario := range []string{"config", "tampering", "challenge", "salt", "nonce", "missing credential", "oversize record"} {
		t.Run(scenario, func(t *testing.T) {
			hello := newFakeWindowsHello(t)
			provider := newWindowsProvider(t.TempDir(), hello)
			scope := testScope()
			if err := provider.Enroll(t.Context(), scope, bytes.Repeat([]byte{0x42}, 32)); err != nil {
				t.Fatal(err)
			}
			switch scenario {
			case "config":
				scope.ConfigHash = strings.Repeat("cd", 32)
			case "missing credential":
				hello.signErr = ErrInvalidated
			case "oversize record":
				if err := os.WriteFile(provider.recordPath(scope), bytes.Repeat([]byte("x"), 4097), 0600); err != nil {
					t.Fatal(err)
				}
			case "tampering", "challenge", "salt", "nonce":
				record, err := provider.readRecord(scope)
				if err != nil {
					t.Fatal(err)
				}
				switch scenario {
				case "tampering":
					record.Ciphertext[0] ^= 1
				case "challenge":
					record.Challenge[0] ^= 1
				case "salt":
					record.Salt[0] ^= 1
				case "nonce":
					record.Nonce[0] ^= 1
				}
				encoded, err := json.Marshal(record)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(provider.recordPath(scope), encoded, 0600); err != nil {
					t.Fatal(err)
				}
			}
			key, err := provider.Unlock(t.Context(), scope)
			if key != nil || !errors.Is(err, ErrInvalidated) {
				t.Fatalf("invalid enrollment released key=%v err=%v", key != nil, err)
			}
		})
	}
}

func TestWindowsHelloCanceledAuthenticationNeverReleasesKey(t *testing.T) {
	hello := newFakeWindowsHello(t)
	provider := newWindowsProvider(t.TempDir(), hello)
	scope := testScope()
	if err := provider.Enroll(t.Context(), scope, bytes.Repeat([]byte{0x42}, 32)); err != nil {
		t.Fatal(err)
	}
	hello.signErr = ErrCanceled
	if key, err := provider.Unlock(t.Context(), scope); key != nil || !errors.Is(err, ErrCanceled) {
		t.Fatalf("canceled prompt key=%v err=%v", key != nil, err)
	}
	hello.signErr = nil
	ctx, cancel := context.WithCancel(t.Context())
	hello.afterSign = cancel
	if key, err := provider.Unlock(ctx, scope); key != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("late signing result key=%v err=%v", key != nil, err)
	}
	if !bytes.Equal(hello.lastSignature, make([]byte, 256)) {
		t.Fatal("late signature was not cleared")
	}
}

func TestWindowsHelloEnrollmentFailuresRemoveNativeCredential(t *testing.T) {
	hello := newFakeWindowsHello(t)
	provider := newWindowsProvider(t.TempDir(), hello)
	scope := testScope()
	hello.signErr = ErrCanceled
	if err := provider.Enroll(t.Context(), scope, bytes.Repeat([]byte{0x42}, 32)); !errors.Is(err, ErrCanceled) {
		t.Fatalf("enroll error = %v", err)
	}
	if hello.enrolled {
		t.Fatal("failed enrollment left native credential")
	}
	if _, err := os.Stat(provider.recordPath(scope)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("failed enrollment wrote record: %v", err)
	}
	hello.supported = false
	if err := provider.Enroll(t.Context(), scope, bytes.Repeat([]byte{0x42}, 32)); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("unsupported device enrolled: %v", err)
	}
}

func TestWindowsHelloDeleteFailurePreservesEnrollmentForRetry(t *testing.T) {
	hello := newFakeWindowsHello(t)
	provider := newWindowsProvider(t.TempDir(), hello)
	scope := testScope()
	if err := provider.Enroll(t.Context(), scope, bytes.Repeat([]byte{0x42}, 32)); err != nil {
		t.Fatal(err)
	}
	hello.deleteErr = errors.New("native deletion failed")
	if err := provider.Delete(t.Context(), scope); !errors.Is(err, hello.deleteErr) {
		t.Fatalf("delete error = %v", err)
	}
	if _, err := provider.readRecord(scope); err != nil {
		t.Fatalf("failed deletion lost record: %v", err)
	}
}

func TestWindowsHelloSignatureRejectsWrongAlgorithmsAndForgedOutput(t *testing.T) {
	hello := newFakeWindowsHello(t)
	message := []byte("independent synthetic challenge")
	signature, public, err := hello.Sign(t.Context(), "test", message)
	if err != nil {
		t.Fatal(err)
	}
	if err := verifyWindowsSignature(message, signature, public); err != nil {
		t.Fatal(err)
	}
	signature[0] ^= 1
	if err := verifyWindowsSignature(message, signature, public); !errors.Is(err, ErrInvalidated) {
		t.Fatalf("forged signature accepted: %v", err)
	}
	if err := verifyWindowsSignature(message, signature[:128], public); !errors.Is(err, ErrInvalidated) {
		t.Fatalf("wrong signature size accepted: %v", err)
	}
	if err := verifyWindowsSignature(message, signature, []byte("bad public key")); !errors.Is(err, ErrInvalidated) {
		t.Fatalf("invalid public key accepted: %v", err)
	}
}

func TestWindowsHelloStatusOffersPasswordAfterMissingOrInvalidEnrollment(t *testing.T) {
	hello := newFakeWindowsHello(t)
	provider := newWindowsProvider(t.TempDir(), hello)
	scope := testScope()
	status, err := provider.Status(t.Context(), scope)
	if err != nil || !status.Available || status.Enrolled {
		t.Fatalf("initial status = %+v, %v", status, err)
	}
	if key, err := provider.Unlock(t.Context(), scope); key != nil || !errors.Is(err, ErrNotEnrolled) {
		t.Fatalf("missing enrollment key=%v err=%v", key != nil, err)
	}
	hello.supported = false
	status, err = provider.Status(t.Context(), scope)
	if err != nil || status.Available || status.Reason != "unavailable" {
		t.Fatalf("unsupported status = %+v, %v", status, err)
	}
	hello.supported = true
	if err := provider.Enroll(t.Context(), scope, bytes.Repeat([]byte{0x42}, 32)); err != nil {
		t.Fatal(err)
	}
	hello.enrolled = false
	status, err = provider.Status(t.Context(), scope)
	if err != nil || !status.Enrolled || status.Reason != "invalidated" {
		t.Fatalf("missing native key status = %+v, %v", status, err)
	}
	changed := Scope{AccountID: scope.AccountID, ChannelID: scope.ChannelID, ConfigHash: strings.Repeat("cd", 32)}
	status, err = provider.Status(t.Context(), changed)
	if err != nil || !status.Enrolled || status.Reason != "invalidated" {
		t.Fatalf("old config status = %+v, %v", status, err)
	}
	hello.existsErr = errors.New("native query failed")
	if status, err := provider.Status(t.Context(), scope); err != nil || !status.Enrolled || status.Available || status.Reason != "unavailable" {
		t.Fatalf("native query failure must preserve deletion availability: %+v %v", status, err)
	}
	hello.supportedErr = errors.New("native support failed")
	if status, err := provider.Status(t.Context(), scope); err != nil || !status.Enrolled || status.Available || status.Reason != "unavailable" {
		t.Fatalf("support failure must preserve deletion availability: %+v %v", status, err)
	}
	if status, err := provider.Status(t.Context(), changed); err != nil || !status.Enrolled || status.Available || status.Reason != "unavailable" {
		t.Fatalf("stale enrollment must remain deletable while Hello is unavailable: %+v %v", status, err)
	}
	if err := provider.Delete(t.Context(), changed); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(provider.recordPath(scope)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("stale unavailable enrollment survived delete: %v", err)
	}
}

func TestWindowsHelloValidatesBeforeNativeCallsAndRejectsCanceledRequests(t *testing.T) {
	hello := newFakeWindowsHello(t)
	provider := newWindowsProvider(t.TempDir(), hello)
	invalid := Scope{}
	if _, err := provider.Status(t.Context(), invalid); err == nil {
		t.Fatal("invalid scope status accepted")
	}
	if err := provider.Enroll(t.Context(), invalid, make([]byte, 32)); err == nil {
		t.Fatal("invalid scope enrollment accepted")
	}
	if key, err := provider.Unlock(t.Context(), invalid); key != nil || err == nil {
		t.Fatal("invalid scope unlock accepted")
	}
	if err := provider.Delete(t.Context(), invalid); err == nil {
		t.Fatal("invalid scope delete accepted")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	scope := testScope()
	if _, err := provider.Status(ctx, scope); !errors.Is(err, context.Canceled) {
		t.Fatalf("status ignored cancellation: %v", err)
	}
	if err := provider.Enroll(ctx, scope, make([]byte, 32)); !errors.Is(err, context.Canceled) {
		t.Fatalf("enroll ignored cancellation: %v", err)
	}
	if key, err := provider.Unlock(ctx, scope); key != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("unlock ignored cancellation: %v", err)
	}
	if err := provider.Delete(ctx, scope); !errors.Is(err, context.Canceled) {
		t.Fatalf("delete ignored cancellation: %v", err)
	}
	if hello.signs != 0 || hello.enrolled {
		t.Fatal("invalid requests performed native crypto operations")
	}
}

func TestWindowsHelloFailedCreateAndPersistenceCleanNativeEnrollment(t *testing.T) {
	for _, scenario := range []string{"creation", "persistence", "native support", "replacement delete"} {
		t.Run(scenario, func(t *testing.T) {
			hello := newFakeWindowsHello(t)
			dir := t.TempDir()
			provider := newWindowsProvider(dir, hello)
			scope := testScope()
			switch scenario {
			case "creation":
				hello.createErr = ErrUnavailable
			case "native support":
				hello.supportedErr = ErrUnavailable
			case "replacement delete":
				hello.deleteErr = ErrUnavailable
			case "persistence":
				if err := os.WriteFile(provider.directory, []byte("block directory creation"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if err := provider.Enroll(t.Context(), scope, make([]byte, 32)); err == nil {
				t.Fatal("failed enrollment reported success")
			}
			if hello.enrolled {
				t.Fatal("failed enrollment retained native credential")
			}
		})
	}
}
