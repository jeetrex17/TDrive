package biometric

import (
	"context"
	"crypto"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Windows Hello's documented KeyCredential signs RSA2048/SHA256/PKCS1v1.5
// after a user gesture. The deterministic signature is PRIVATE key-derivation
// material here, never an authentication token: it must never be stored, logged
// or returned to JavaScript. A fresh RequestSignAsync is required on every unlock.
// See https://learn.microsoft.com/windows/apps/develop/security/windows-hello.
// Windows may permit its OS PIN, and TPM backing is determined by Windows policy.
type windowsHello interface {
	Supported(context.Context) (bool, error)
	Create(context.Context, string) error
	Exists(context.Context, string) (bool, error)
	Sign(context.Context, string, []byte) (signature, publicKey []byte, err error)
	Delete(context.Context, string) error
}

type windowsProvider struct {
	directory string
	hello     windowsHello
	mu        sync.Mutex
}

type windowsRecord struct {
	Version    int    `json:"version"`
	ConfigHash string `json:"configHash"`
	Challenge  []byte `json:"challenge"`
	Salt       []byte `json:"salt"`
	Nonce      []byte `json:"nonce"`
	Ciphertext []byte `json:"ciphertext"`
}

func newWindowsProvider(dataDir string, hello windowsHello) *windowsProvider {
	return &windowsProvider{directory: filepath.Join(dataDir, "biometric"), hello: hello}
}

func (p *windowsProvider) Status(ctx context.Context, scope Scope) (Status, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	status := Status{Kind: "windows_hello", Reason: "not_enrolled"}
	if err := scope.Valid(); err != nil {
		return status, err
	}
	if err := ctx.Err(); err != nil {
		return status, err
	}
	if _, err := os.Stat(p.recordPath(scope)); err == nil {
		status.Enrolled = true
	} else if !errors.Is(err, os.ErrNotExist) {
		return status, fmt.Errorf("inspect Windows Hello enrollment: %w", err)
	}
	available, err := p.hello.Supported(ctx)
	if err != nil || !available {
		if ctx.Err() != nil {
			return status, ctx.Err()
		}
		status.Reason = "unavailable"
		return status, nil
	}
	status.Available = true
	if !status.Enrolled {
		return status, nil
	}
	if _, err := p.readRecord(scope); err != nil {
		if errors.Is(err, ErrInvalidated) || errors.Is(err, ErrNotEnrolled) {
			status.Reason = "invalidated"
			return status, nil
		}
		return status, err
	}
	exists, err := p.hello.Exists(ctx, p.credentialName(scope))
	if err != nil {
		if ctx.Err() != nil {
			return status, ctx.Err()
		}
		status.Available = false
		status.Reason = "unavailable"
		return status, nil
	}
	if !exists {
		status.Reason = "invalidated"
	} else {
		status.Reason = ""
	}
	return status, nil
}

func (p *windowsProvider) Enroll(ctx context.Context, scope Scope, master []byte) (err error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	payload, err := EncodeSecret(scope, master)
	if err != nil {
		return err
	}
	defer clear(payload)
	if err := ctx.Err(); err != nil {
		return err
	}
	available, err := p.hello.Supported(ctx)
	if err != nil {
		return err
	}
	if !available {
		return ErrUnavailable
	}
	if err := p.deleteLocked(ctx, scope); err != nil {
		return err
	}
	defer func() {
		if err != nil {
			cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			defer cancel()
			err = errors.Join(err, p.deleteLocked(cleanupCtx, scope))
		}
	}()
	if err := p.hello.Create(ctx, p.credentialName(scope)); err != nil {
		return err
	}
	record := windowsRecord{Version: 1, ConfigHash: scope.ConfigHash, Challenge: make([]byte, 32), Salt: make([]byte, 32), Nonce: make([]byte, 12)}
	for _, buffer := range [][]byte{record.Challenge, record.Salt, record.Nonce} {
		if _, err := rand.Read(buffer); err != nil {
			return err
		}
	}
	box, err := p.signingBox(ctx, scope, record)
	if err != nil {
		return err
	}
	record.Ciphertext = box.Seal(nil, record.Nonce, payload, windowsAAD(scope))
	if err := ctx.Err(); err != nil {
		return err
	}
	return p.writeRecord(scope, record)
}

func (p *windowsProvider) Unlock(ctx context.Context, scope Scope) ([]byte, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := scope.Valid(); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	record, err := p.readRecord(scope)
	if err != nil {
		return nil, err
	}
	box, err := p.signingBox(ctx, scope, record)
	if err != nil {
		return nil, err
	}
	payload, err := box.Open(nil, record.Nonce, record.Ciphertext, windowsAAD(scope))
	if err != nil {
		return nil, ErrInvalidated
	}
	defer clear(payload)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return DecodeSecret(scope, payload)
}

func (p *windowsProvider) Delete(ctx context.Context, scope Scope) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := scope.Valid(); err != nil {
		return err
	}
	return p.deleteLocked(ctx, scope)
}

func (p *windowsProvider) deleteLocked(ctx context.Context, scope Scope) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := p.hello.Delete(ctx, p.credentialName(scope)); err != nil && !errors.Is(err, ErrNotEnrolled) && !errors.Is(err, ErrInvalidated) {
		return err
	}
	if err := os.Remove(p.recordPath(scope)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove Windows Hello enrollment: %w", err)
	}
	return nil
}

func (p *windowsProvider) signingBox(ctx context.Context, scope Scope, record windowsRecord) (cipher.AEAD, error) {
	message := append(windowsAAD(scope), record.Challenge...)
	signature, public, err := p.hello.Sign(ctx, p.credentialName(scope), message)
	defer clear(signature)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := verifyWindowsSignature(message, signature, public); err != nil {
		return nil, err
	}
	key, err := hkdf.Key(sha256.New, signature, record.Salt, "tdrive/windows-hello/wrap/v1\x00"+scope.ID()+"\x00"+scope.ConfigHash, 32)
	if err != nil {
		return nil, err
	}
	defer clear(key)
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

func verifyWindowsSignature(message, signature, public []byte) error {
	parsed, err := x509.ParsePKIXPublicKey(public)
	if err != nil {
		return ErrInvalidated
	}
	key, ok := parsed.(*rsa.PublicKey)
	if !ok || key.N.BitLen() != 2048 || key.E != 65537 || len(signature) != 256 {
		return ErrInvalidated
	}
	digest := sha256.Sum256(message)
	if err := rsa.VerifyPKCS1v15(key, crypto.SHA256, digest[:], signature); err != nil {
		return ErrInvalidated
	}
	return nil
}

func windowsAAD(scope Scope) []byte {
	return []byte("tdrive/windows-hello/envelope/v1\x00" + scope.ID() + "\x00" + scope.ConfigHash)
}

func (p *windowsProvider) credentialName(scope Scope) string { return "TDrive.Vault." + scope.ID() }
func (p *windowsProvider) recordPath(scope Scope) string {
	return filepath.Join(p.directory, scope.ID()+".windows-hello.json")
}

func (p *windowsProvider) readRecord(scope Scope) (windowsRecord, error) {
	file, err := os.Open(p.recordPath(scope))
	if errors.Is(err, os.ErrNotExist) {
		return windowsRecord{}, ErrNotEnrolled
	}
	if err != nil {
		return windowsRecord{}, fmt.Errorf("read Windows Hello enrollment: %w", err)
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, 4097))
	if err != nil {
		return windowsRecord{}, err
	}
	var record windowsRecord
	if len(data) > 4096 || json.Unmarshal(data, &record) != nil || record.Version != 1 || record.ConfigHash != scope.ConfigHash || len(record.Challenge) != 32 || len(record.Salt) != 32 || len(record.Nonce) != 12 || len(record.Ciphertext) != 80 {
		return windowsRecord{}, ErrInvalidated
	}
	return record, nil
}

func (p *windowsProvider) writeRecord(scope Scope, record windowsRecord) (err error) {
	if err := os.MkdirAll(p.directory, 0700); err != nil {
		return err
	}
	data, err := json.Marshal(record)
	if err != nil {
		return err
	}
	file, err := os.CreateTemp(p.directory, ".windows-hello-*")
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			closeErr := file.Close()
			if errors.Is(closeErr, os.ErrClosed) {
				closeErr = nil
			}
			removeErr := os.Remove(file.Name())
			if errors.Is(removeErr, os.ErrNotExist) {
				removeErr = nil
			}
			err = errors.Join(err, closeErr, removeErr)
		}
	}()
	if err := file.Chmod(0600); err != nil {
		return err
	}
	if _, err := file.Write(data); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(file.Name(), p.recordPath(scope))
}
