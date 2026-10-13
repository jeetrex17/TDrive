package encryption

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"TDrive/backend/biometric"
	tdcrypto "TDrive/backend/crypto"
)

const testAccountID int64 = 123

func TestBiometricScopeBindsCryptoConfigAndIgnoresHint(t *testing.T) {
	svc, db := newTestService(t)
	if _, err := svc.BiometricScope(testAccountID); !errors.Is(err, ErrVaultNotConfigured) {
		t.Fatalf("unconfigured scope error = %v", err)
	}
	if err := svc.CreatePassword("password-1", "first hint"); err != nil {
		t.Fatal(err)
	}
	scope, err := svc.BiometricScope(testAccountID)
	if err != nil || scope.AccountID != testAccountID || scope.ChannelID != testChannelID || scope.Valid() != nil {
		t.Fatalf("BiometricScope = %+v, %v", scope, err)
	}
	if _, err := db.Exec(`UPDATE encryption SET hint = ?, created_at = created_at + 1 WHERE channel_id = ?`, "new hint", testChannelID); err != nil {
		t.Fatal(err)
	}
	afterHint, err := svc.BiometricScope(testAccountID)
	if err != nil || afterHint != scope {
		t.Fatalf("hint-only update changed biometric binding: %+v, %v", afterHint, err)
	}
	if _, err := db.Exec(`UPDATE encryption SET kdf_params_json = ? WHERE channel_id = ?`, `{"salt_len":16,"key_len":32,"parallelism":4,"time":3,"memory":65536}`, testChannelID); err != nil {
		t.Fatal(err)
	}
	afterCanonical, err := svc.BiometricScope(testAccountID)
	if err != nil || afterCanonical != scope {
		t.Fatalf("equivalent KDF JSON changed biometric binding: %+v, %v", afterCanonical, err)
	}
	otherAccount, err := svc.BiometricScope(testAccountID + 1)
	if err != nil || otherAccount.ConfigHash == scope.ConfigHash {
		t.Fatalf("different account scope = %+v, %v", otherAccount, err)
	}
	if err := svc.ChangePassword("password-1", "password-2", "new hint"); err != nil {
		t.Fatal(err)
	}
	afterPassword, err := svc.BiometricScope(testAccountID)
	if err != nil || afterPassword.ConfigHash == scope.ConfigHash || afterPassword.ID() != scope.ID() {
		t.Fatalf("password change scope = %+v, %v", afterPassword, err)
	}
	if err := svc.VerifyBiometricKey(scope, bytes.Repeat([]byte{0x42}, 32)); !errors.Is(err, biometric.ErrInvalidated) {
		t.Fatalf("stale scope error = %v", err)
	}
}

func TestBiometricEnrollmentRequiresFreshPasswordWithoutUnlockingSession(t *testing.T) {
	svc, _ := newTestService(t)
	if err := svc.CreatePassword("password-1", ""); err != nil {
		t.Fatal(err)
	}
	scope, err := svc.BiometricScope(testAccountID)
	if err != nil {
		t.Fatal(err)
	}
	if key, err := svc.BiometricEnrollmentKey(scope, "wrong-password"); !errors.Is(err, ErrWrongPassword) || key != nil {
		t.Fatalf("unlocked session bypassed enrollment password: key=%v, err=%v", key != nil, err)
	}
	svc.Clear()
	if key, err := svc.BiometricEnrollmentKey(scope, "wrong-password"); !errors.Is(err, ErrWrongPassword) || key != nil {
		t.Fatalf("wrong enrollment password returned key=%v, err=%v", key != nil, err)
	}
	key, err := svc.BiometricEnrollmentKey(scope, "password-1")
	if err != nil {
		t.Fatal(err)
	}
	defer clear(key)
	if _, loaded := svc.LoadedMasterKey(); loaded {
		t.Fatal("preparing enrollment unlocked the app session")
	}
	var encrypted bytes.Buffer
	plaintext := []byte("existing encrypted file remains readable after biometric unlock")
	if err := tdcrypto.EncryptStream(bytes.NewReader(plaintext), &encrypted, key, int64(len(plaintext))); err != nil {
		t.Fatal(err)
	}
	if err := svc.VerifyBiometricKey(scope, key); err != nil {
		t.Fatal(err)
	}
	clear(key)
	loaded, err := svc.RequireMasterKeyForChannel(testChannelID, true)
	if err != nil {
		t.Fatal(err)
	}
	defer clear(loaded)
	var decrypted bytes.Buffer
	if _, err := tdcrypto.DecryptStream(&encrypted, &decrypted, loaded); err != nil || !bytes.Equal(decrypted.Bytes(), plaintext) {
		t.Fatalf("biometric session could not decrypt existing bytes: %v", err)
	}
	retained := svc.masterKey
	svc.Clear()
	if !bytes.Equal(retained, make([]byte, 32)) {
		t.Fatal("locking session did not wipe retained biometric master key")
	}
}

func TestBiometricKeyRejectsWrongKeyAndScopeWithoutInstalling(t *testing.T) {
	svc, _ := newTestService(t)
	if err := svc.CreatePassword("password-1", ""); err != nil {
		t.Fatal(err)
	}
	scope, err := svc.BiometricScope(testAccountID)
	if err != nil {
		t.Fatal(err)
	}
	key, err := svc.BiometricEnrollmentKey(scope, "password-1")
	if err != nil {
		t.Fatal(err)
	}
	defer clear(key)
	svc.Clear()
	for _, wrong := range []biometric.Scope{
		{AccountID: scope.AccountID + 1, ChannelID: scope.ChannelID, ConfigHash: scope.ConfigHash},
		{AccountID: scope.AccountID, ChannelID: scope.ChannelID + 1, ConfigHash: scope.ConfigHash},
		{AccountID: scope.AccountID, ChannelID: scope.ChannelID, ConfigHash: strings.Repeat("00", 32)},
	} {
		if err := svc.VerifyBiometricKey(wrong, key); !errors.Is(err, biometric.ErrInvalidated) {
			t.Fatalf("wrong scope error = %v", err)
		}
	}
	for _, wrongKey := range [][]byte{nil, bytes.Repeat([]byte{0x42}, 31), bytes.Repeat([]byte{0x42}, 32)} {
		if err := svc.VerifyBiometricKey(scope, wrongKey); err == nil {
			t.Fatal("wrong master key accepted")
		}
	}
	if _, loaded := svc.LoadedMasterKey(); loaded {
		t.Fatal("rejected biometric key unlocked the session")
	}
	svc.personalChannelID = func() int64 { return testChannelID + 1 }
	if err := svc.VerifyBiometricKey(scope, key); !errors.Is(err, biometric.ErrInvalidated) {
		t.Fatalf("drive switch error = %v", err)
	}
}

func TestBiometricEnrollmentRejectsDriveChangeWhileDerivingPassword(t *testing.T) {
	svc, _ := newTestService(t)
	if err := svc.CreatePassword("password-1", ""); err != nil {
		t.Fatal(err)
	}
	scope, err := svc.BiometricScope(testAccountID)
	if err != nil {
		t.Fatal(err)
	}
	svc.Clear()
	svc.deriveKEK = func(password, salt []byte, params tdcrypto.Params) ([]byte, error) {
		key, err := tdcrypto.DeriveKEK(password, salt, params)
		svc.personalChannelID = func() int64 { return testChannelID + 1 }
		return key, err
	}
	if key, err := svc.BiometricEnrollmentKey(scope, "password-1"); !errors.Is(err, biometric.ErrInvalidated) || key != nil {
		t.Fatalf("drive changed during derivation returned key=%v, err=%v", key != nil, err)
	}
}

func TestBiometricEnrollmentRejectsConfigChangeAndClearsDerivationBuffers(t *testing.T) {
	svc, db := newTestService(t)
	if err := svc.CreatePassword("password-1", ""); err != nil {
		t.Fatal(err)
	}
	scope, err := svc.BiometricScope(testAccountID)
	if err != nil {
		t.Fatal(err)
	}
	svc.Clear()
	var passwordBuffer, derivedBuffer []byte
	var updateErr error
	svc.deriveKEK = func(password, salt []byte, params tdcrypto.Params) ([]byte, error) {
		passwordBuffer = password
		key, err := tdcrypto.DeriveKEK(password, salt, params)
		derivedBuffer = key
		_, updateErr = db.Exec(`UPDATE encryption SET kdf_salt = ? WHERE channel_id = ?`, bytes.Repeat([]byte{0x99}, 16), testChannelID)
		return key, err
	}
	if key, err := svc.BiometricEnrollmentKey(scope, "password-1"); !errors.Is(err, biometric.ErrInvalidated) || key != nil {
		t.Fatalf("config changed during derivation returned key=%v, err=%v", key != nil, err)
	}
	if updateErr != nil {
		t.Fatal(updateErr)
	}
	if !bytes.Equal(passwordBuffer, make([]byte, len("password-1"))) || !bytes.Equal(derivedBuffer, make([]byte, 32)) {
		t.Fatal("enrollment retained password or key-encryption-key bytes")
	}
	if _, loaded := svc.LoadedMasterKey(); loaded {
		t.Fatal("failed enrollment preparation installed a master key")
	}
}

func TestBiometricScopeRejectsDisabledAndInvalidConfig(t *testing.T) {
	for _, change := range []string{
		`UPDATE encryption SET enabled = 0`,
		`UPDATE encryption SET version = 2`,
		`UPDATE encryption SET kdf_params_json = 'broken'`,
	} {
		t.Run(change, func(t *testing.T) {
			svc, db := newTestService(t)
			if err := svc.CreatePassword("password-1", ""); err != nil {
				t.Fatal(err)
			}
			if _, err := db.Exec(change); err != nil {
				t.Fatal(err)
			}
			if _, err := svc.BiometricScope(testAccountID); err == nil {
				t.Fatal("invalid or disabled vault accepted for enrollment")
			}
		})
	}
	if _, err := NewService(Config{}).BiometricScope(testAccountID); err == nil {
		t.Fatal("missing database accepted")
	}
	svc, _ := newTestService(t)
	if _, err := svc.BiometricScope(0); err == nil {
		t.Fatal("missing account accepted")
	}
}

func TestBiometricRequestsRejectRemovedVaultAndBlankEnrollmentPassword(t *testing.T) {
	svc, db := newTestService(t)
	if err := svc.CreatePassword("password-1", ""); err != nil {
		t.Fatal(err)
	}
	scope, err := svc.BiometricScope(testAccountID)
	if err != nil {
		t.Fatal(err)
	}
	key, err := svc.BiometricEnrollmentKey(scope, "password-1")
	if err != nil {
		t.Fatal(err)
	}
	defer clear(key)
	svc.Clear()
	if returned, err := svc.BiometricEnrollmentKey(scope, " \t "); err == nil || returned != nil {
		t.Fatalf("blank enrollment password returned key=%v, err=%v", returned != nil, err)
	}
	if _, err := db.Exec(`UPDATE encryption SET enabled = 0 WHERE channel_id = ?`, testChannelID); err != nil {
		t.Fatal(err)
	}
	if err := svc.VerifyBiometricKey(scope, key); !errors.Is(err, biometric.ErrInvalidated) {
		t.Fatalf("disabled vault accepted biometric key: %v", err)
	}
	if _, err := db.Exec(`DELETE FROM encryption WHERE channel_id = ?`, testChannelID); err != nil {
		t.Fatal(err)
	}
	if returned, err := svc.BiometricEnrollmentKey(scope, "password-1"); !errors.Is(err, biometric.ErrInvalidated) || returned != nil {
		t.Fatalf("removed vault returned enrollment key=%v, err=%v", returned != nil, err)
	}
	if err := svc.VerifyBiometricKey(scope, key); !errors.Is(err, biometric.ErrInvalidated) {
		t.Fatalf("removed vault accepted biometric key: %v", err)
	}
	if _, loaded := svc.LoadedMasterKey(); loaded {
		t.Fatal("invalid biometric request unlocked the session")
	}
}
