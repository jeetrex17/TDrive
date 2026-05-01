// Package crypto implements password-derived key wrapping and
// authenticated streaming file encryption for personal-drive encrypted
// uploads.
//
// # Threat model
//
// We treat the Telegram channel and any local Telegram cache as untrusted
// public storage. The user's password never leaves the device. We never
// persist the master key in plaintext: it is wrapped under a
// password-derived key-encryption-key (KEK) and decrypted only into
// process memory after the user enters the correct password.
//
// Building blocks: Argon2id (password → KEK), XChaCha20-Poly1305 (AEAD),
// HKDF-SHA256 (per-file subkey derivation).
package crypto

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"

	"golang.org/x/crypto/argon2"
	"golang.org/x/crypto/chacha20poly1305"
)

// Params controls the Argon2id cost. Stored as JSON so we can dial the
// cost upward in future versions without breaking existing encrypted files.
type Params struct {
	Memory      uint32 `json:"memory"`      // KiB
	Time        uint32 `json:"time"`        // iterations
	Parallelism uint8  `json:"parallelism"` // lanes
	KeyLen      uint32 `json:"key_len"`     // bytes
	SaltLen     int    `json:"salt_len"`    // bytes
}

// DefaultParams targets ~250 ms on a modern laptop and 64 MiB RAM. Tuned
// down from "interactive" libsodium presets so password entry is
// responsive on low-end machines too. Keys are 32 bytes for
// XChaCha20-Poly1305.
func DefaultParams() Params {
	return Params{
		Memory:      64 * 1024,
		Time:        3,
		Parallelism: 4,
		KeyLen:      32,
		SaltLen:     16,
	}
}

const (
	masterKeyLen = 32
)

// keyCheckPlaintext is decrypted whenever the password is entered.
// Anything fixed and nonempty works; the bytes themselves never need to
// be secret.
var keyCheckPlaintext = []byte("tdrive-key-check-v1")

var (
	ErrWrongPassword  = errors.New("crypto: wrong password")
	ErrCorruptKeyData = errors.New("crypto: corrupt encryption key data")
)

// DeriveKEK runs Argon2id against the password+salt with the supplied
// params and returns a key-encryption-key. Caller must zeroize when done.
func DeriveKEK(password, salt []byte, p Params) []byte {
	return argon2.IDKey(password, salt, p.Time, p.Memory, p.Parallelism, p.KeyLen)
}

// NewMasterKey returns a fresh 32-byte random key.
func NewMasterKey() ([]byte, error) {
	k := make([]byte, masterKeyLen)
	if _, err := rand.Read(k); err != nil {
		return nil, err
	}
	return k, nil
}

// NewSalt returns p.SaltLen random bytes.
func NewSalt(p Params) ([]byte, error) {
	salt := make([]byte, p.SaltLen)
	if _, err := rand.Read(salt); err != nil {
		return nil, err
	}
	return salt, nil
}

// MarshalParams encodes Params as JSON for storage.
func MarshalParams(p Params) (string, error) {
	b, err := json.Marshal(p)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// UnmarshalParams decodes Params previously written by MarshalParams.
func UnmarshalParams(s string) (Params, error) {
	var p Params
	if err := json.Unmarshal([]byte(s), &p); err != nil {
		return Params{}, fmt.Errorf("crypto: parse params: %w", err)
	}
	return p, nil
}

// WrapMasterKey encrypts master under kek with XChaCha20-Poly1305. Output
// layout: 24-byte nonce ‖ ciphertext ‖ tag.
func WrapMasterKey(master, kek []byte) ([]byte, error) {
	if len(master) != masterKeyLen {
		return nil, fmt.Errorf("crypto: wrap: master key must be %d bytes", masterKeyLen)
	}
	aead, err := chacha20poly1305.NewX(kek)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	out := make([]byte, 0, len(nonce)+len(master)+aead.Overhead())
	out = append(out, nonce...)
	out = aead.Seal(out, nonce, master, nil)
	return out, nil
}

// UnwrapMasterKey reverses WrapMasterKey. Returns ErrWrongPassword if the
// AEAD tag fails — the only way the wrapped blob can fail is wrong KEK.
func UnwrapMasterKey(wrapped, kek []byte) ([]byte, error) {
	aead, err := chacha20poly1305.NewX(kek)
	if err != nil {
		return nil, err
	}
	if len(wrapped) < aead.NonceSize()+aead.Overhead() {
		return nil, ErrCorruptKeyData
	}
	nonce := wrapped[:aead.NonceSize()]
	ct := wrapped[aead.NonceSize():]
	master, err := aead.Open(nil, nonce, ct, nil)
	if err != nil {
		return nil, ErrWrongPassword
	}
	if len(master) != masterKeyLen {
		return nil, ErrCorruptKeyData
	}
	return master, nil
}

// EncodeKeyCheck encrypts a fixed marker under the master key. Stored
// next to the wrapped master key so we can verify a candidate password
// without having to decrypt a real file.
func EncodeKeyCheck(master []byte) ([]byte, error) {
	aead, err := chacha20poly1305.NewX(master)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	out := make([]byte, 0, len(nonce)+len(keyCheckPlaintext)+aead.Overhead())
	out = append(out, nonce...)
	out = aead.Seal(out, nonce, keyCheckPlaintext, nil)
	return out, nil
}

// VerifyKeyCheck decrypts and compares the key-check blob. Wrong master
// key (or tampered blob) returns ErrWrongPassword.
func VerifyKeyCheck(master, blob []byte) error {
	aead, err := chacha20poly1305.NewX(master)
	if err != nil {
		return err
	}
	if len(blob) < aead.NonceSize()+aead.Overhead() {
		return ErrCorruptKeyData
	}
	nonce := blob[:aead.NonceSize()]
	ct := blob[aead.NonceSize():]
	pt, err := aead.Open(nil, nonce, ct, nil)
	if err != nil {
		return ErrWrongPassword
	}
	if string(pt) != string(keyCheckPlaintext) {
		return ErrCorruptKeyData
	}
	return nil
}
