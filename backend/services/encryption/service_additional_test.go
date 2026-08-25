package encryption

import (
	"bytes"
	"errors"
	"io"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	tdcrypto "TDrive/backend/crypto"
	"TDrive/backend/projection"
)

func TestWriteCiphertextTempRoundTripAndCleanupOnFailure(t *testing.T) {
	svc, _ := newTestService(t)
	master, err := tdcrypto.NewMasterKey()
	if err != nil {
		t.Fatalf("NewMasterKey: %v", err)
	}
	plaintext := []byte("encrypted mount upload")

	tmp, err := svc.WriteCiphertextTemp(bytes.NewReader(plaintext), int64(len(plaintext)), master)
	if err != nil {
		t.Fatalf("WriteCiphertextTemp: %v", err)
	}
	t.Cleanup(func() {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
	})
	var decrypted bytes.Buffer
	if _, err := tdcrypto.DecryptStream(tmp, &decrypted, master); err != nil {
		t.Fatalf("DecryptStream: %v", err)
	}
	if !bytes.Equal(decrypted.Bytes(), plaintext) {
		t.Fatalf("decrypted = %q, want %q", decrypted.Bytes(), plaintext)
	}

	if failed, err := svc.WriteCiphertextTemp(errorReader{}, 1, master); err == nil || failed != nil {
		t.Fatalf("WriteCiphertextTemp(errorReader) = %#v, %v; want nil, error", failed, err)
	}
}

func TestServiceRejectsUnavailableConfiguration(t *testing.T) {
	t.Run("database not ready", func(t *testing.T) {
		svc := NewService(Config{})
		if _, err := svc.Status(); err == nil {
			t.Fatal("Status unexpectedly succeeded")
		}
		if err := svc.CreatePassword("password-1", ""); err == nil {
			t.Fatal("CreatePassword unexpectedly succeeded")
		}
		if lease, err := svc.AcquireMasterKeyLease(); err == nil || lease != nil {
			t.Fatalf("AcquireMasterKeyLease = %#v, %v; want error", lease, err)
		}
	})

	t.Run("personal drive not initialised", func(t *testing.T) {
		_, db := newTestService(t)
		svc := NewService(Config{DB: db})
		status, err := svc.Status()
		if err != nil || status.Available {
			t.Fatalf("Status = %+v, %v; want unavailable", status, err)
		}
		if err := svc.CreatePassword("password-1", ""); err == nil {
			t.Fatal("CreatePassword unexpectedly succeeded")
		}
		if err := svc.UsePassword("password-1"); err == nil {
			t.Fatal("UsePassword unexpectedly succeeded")
		}
		if err := svc.ChangePassword("password-1", "password-2", ""); err == nil {
			t.Fatal("ChangePassword unexpectedly succeeded")
		}
	})

	t.Run("emitter not ready", func(t *testing.T) {
		_, db := newTestService(t)
		svc := NewService(Config{
			DB:                db,
			PersonalChannelID: func() int64 { return testChannelID },
		})
		if err := svc.CreatePassword("password-1", ""); err == nil {
			t.Fatal("CreatePassword unexpectedly succeeded")
		}
		if _, ok := svc.LoadedMasterKey(); ok {
			t.Fatal("failed password creation retained a master key")
		}
	})
}

func TestPasswordValidationAndMissingVaultErrors(t *testing.T) {
	svc, _ := newTestService(t)
	for _, password := range []string{"", "   ", "short"} {
		if err := svc.CreatePassword(password, ""); err == nil {
			t.Fatalf("CreatePassword(%q) unexpectedly succeeded", password)
		}
	}
	if err := svc.UsePassword("   "); err == nil {
		t.Fatal("UsePassword with blank password unexpectedly succeeded")
	}
	if err := svc.UsePassword("password-1"); err == nil {
		t.Fatal("UsePassword without a vault unexpectedly succeeded")
	}
	if err := svc.ChangePassword("", "password-2", ""); err == nil {
		t.Fatal("ChangePassword with blank current password unexpectedly succeeded")
	}
	if err := svc.ChangePassword("password-1", "short", ""); err == nil {
		t.Fatal("ChangePassword with short new password unexpectedly succeeded")
	}
	if err := svc.ChangePassword("password-1", "password-2", ""); err == nil {
		t.Fatal("ChangePassword without a vault unexpectedly succeeded")
	}

	if err := svc.CreatePassword("password-1", ""); err != nil {
		t.Fatalf("CreatePassword: %v", err)
	}
	if err := svc.CreatePassword("password-2", ""); err == nil {
		t.Fatal("duplicate CreatePassword unexpectedly succeeded")
	}
}

func TestAcquireMasterKeyLeaseRejectsDisabledVault(t *testing.T) {
	svc, db := newTestService(t)
	if err := svc.CreatePassword("password-1", ""); err != nil {
		t.Fatalf("CreatePassword: %v", err)
	}
	if _, err := db.Exec(`UPDATE encryption SET enabled = 0 WHERE channel_id = ?`, testChannelID); err != nil {
		t.Fatalf("disable vault: %v", err)
	}

	lease, err := svc.AcquireMasterKeyLease()
	if !errors.Is(err, ErrVaultNotConfigured) || lease != nil {
		t.Fatalf("AcquireMasterKeyLease = %#v, %v; want ErrVaultNotConfigured", lease, err)
	}
}

func TestMasterKeyGatesRejectWrongChannel(t *testing.T) {
	svc, _ := newTestService(t)
	if _, err := svc.MasterKeyForUpload(testChannelID+1, true); err == nil {
		t.Fatal("encrypted upload outside personal drive unexpectedly succeeded")
	}
	if _, err := svc.MasterKeyForUpload(0, true); err == nil {
		t.Fatal("encrypted upload without a channel unexpectedly succeeded")
	}
	if key, err := svc.RequireMasterKeyForFile(false); err != nil || key != nil {
		t.Fatalf("plaintext file key = %x, %v; want nil, nil", key, err)
	}
}

func TestNilMasterKeyLeaseIsClosed(t *testing.T) {
	var lease *MasterKeyLease
	key, err := lease.Key()
	if key != nil || !errors.Is(err, ErrMasterKeyLeaseClosed) {
		t.Fatalf("nil lease Key = %x, %v; want ErrMasterKeyLeaseClosed", key, err)
	}
	lease.Close()

	zeroValue := &MasterKeyLease{}
	key, err = zeroValue.Key()
	if key != nil || !errors.Is(err, ErrMasterKeyLeaseClosed) {
		t.Fatalf("zero-value lease Key = %x, %v; want ErrMasterKeyLeaseClosed", key, err)
	}
	zeroValue.Close()
}

func TestConfigOpNormalizesVersionAndCopiesBuffers(t *testing.T) {
	cfg := testEncryptionConfig()
	cfg.Version = 0
	op := configOp(cfg)
	if op.ConfigVersion != 1 {
		t.Fatalf("ConfigVersion = %d, want 1", op.ConfigVersion)
	}
	clear(cfg.KDFSalt)
	clear(cfg.WrappedMasterKey)
	clear(cfg.KeyCheck)
	if bytes.Equal(op.KDFSalt, cfg.KDFSalt) || bytes.Equal(op.WrappedMasterKey, cfg.WrappedMasterKey) || bytes.Equal(op.KeyCheck, cfg.KeyCheck) {
		t.Fatal("configOp retained caller-owned buffers")
	}
}

func TestUnwrapMasterKeyRejectsInvalidConfig(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*projection.EncryptionConfig)
		want   error
	}{
		{name: "malformed kdf", mutate: func(c *projection.EncryptionConfig) { c.KDFParamsJSON = "not-json" }, want: tdcrypto.ErrInvalidKDFParams},
		{name: "extreme kdf", mutate: func(c *projection.EncryptionConfig) {
			c.KDFParamsJSON = `{"memory":4294967295,"time":3,"parallelism":4,"key_len":32,"salt_len":16}`
		}, want: tdcrypto.ErrInvalidKDFParams},
		{name: "unsupported kdf", mutate: func(c *projection.EncryptionConfig) {
			c.KDFParamsJSON = `{"kdf":"scrypt","memory":65536,"time":3,"parallelism":4,"key_len":32,"salt_len":16}`
		}, want: tdcrypto.ErrUnsupportedKDF},
		{name: "mismatched salt", mutate: func(c *projection.EncryptionConfig) { c.KDFSalt = c.KDFSalt[:len(c.KDFSalt)-1] }, want: tdcrypto.ErrInvalidKDFParams},
		{name: "future config version", mutate: func(c *projection.EncryptionConfig) { c.Version = 2 }, want: projection.ErrUnsupportedEncryptionConfigVersion},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			svc, _ := newTestService(t)
			cfg := validServiceVaultConfig()
			test.mutate(&cfg)
			if err := svc.RememberPassword(cfg, "password-1"); !errors.Is(err, test.want) {
				t.Fatalf("RememberPassword error = %v, want %v", err, test.want)
			}
			if _, ok := svc.LoadedMasterKey(); ok {
				t.Fatal("invalid vault metadata loaded a master key")
			}
		})
	}
}

func TestServiceSerializesArgon2Derivations(t *testing.T) {
	svc, _ := newTestService(t)
	started := make(chan struct{}, 2)
	release := make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	t.Cleanup(unblock)
	var active atomic.Int32
	var peak atomic.Int32
	sentinel := errors.New("test kdf stopped")
	svc.deriveKEK = func([]byte, []byte, tdcrypto.Params) ([]byte, error) {
		current := active.Add(1)
		for observed := peak.Load(); current > observed && !peak.CompareAndSwap(observed, current); observed = peak.Load() {
		}
		started <- struct{}{}
		<-release
		active.Add(-1)
		return nil, sentinel
	}

	cfg := validServiceVaultConfig()
	errorsOut := make(chan error, 2)
	for range 2 {
		go func() {
			errorsOut <- svc.RememberPassword(cfg, "password-1")
		}()
	}

	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("first KDF did not start")
	}
	select {
	case <-started:
		t.Fatal("second KDF entered before the first derivation completed")
	case <-time.After(50 * time.Millisecond):
	}
	unblock()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("second KDF did not start after the first completed")
	}

	for range 2 {
		if err := <-errorsOut; !errors.Is(err, sentinel) {
			t.Fatalf("RememberPassword error = %v, want sentinel", err)
		}
	}
	if got := peak.Load(); got != 1 {
		t.Fatalf("peak concurrent KDFs = %d, want 1", got)
	}
}

type errorReader struct{}

func (errorReader) Read([]byte) (int, error) {
	return 0, io.ErrClosedPipe
}

func testEncryptionConfig() projection.EncryptionConfig {
	return projection.EncryptionConfig{
		ChannelID:        testChannelID,
		Enabled:          true,
		KDFSalt:          []byte("salt"),
		KDFParamsJSON:    strings.Repeat("x", 4),
		WrappedMasterKey: []byte("wrapped"),
		KeyCheck:         []byte("check"),
		Hint:             " hint ",
	}
}

func validServiceVaultConfig() projection.EncryptionConfig {
	return projection.EncryptionConfig{
		ChannelID:        testChannelID,
		Enabled:          true,
		KDFSalt:          bytes.Repeat([]byte{0x11}, 16),
		KDFParamsJSON:    `{"memory":65536,"time":3,"parallelism":4,"key_len":32,"salt_len":16}`,
		WrappedMasterKey: bytes.Repeat([]byte{0x22}, 72),
		KeyCheck:         bytes.Repeat([]byte{0x33}, 59),
		Version:          1,
	}
}
