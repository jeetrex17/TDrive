// Package crypto implements password-derived key wrapping and
// authenticated streaming file encryption for personal-drive encrypted
// uploads.
//
// # Threat model
//
// We treat the Telegram channel and any local Telegram cache as untrusted
// public storage for confidentiality. The user's password never leaves the
// device. We never persist the master key in plaintext: it is wrapped under a
// password-derived key-encryption-key (KEK) and decrypted only into process
// memory after the user enters the correct password.
//
// TDE1 authenticates each encrypted stream, including its length and final
// chunk, so in-stream modification and truncation fail closed. It does not
// authenticate TDrive's plaintext namespace/control metadata or bind a whole
// valid ciphertext stream to one file identity. Metadata disclosure,
// deletion/replay, and cross-object substitution within the same encrypted
// drive are outside the v1 threat model and require a versioned format with
// authenticated object identity.
//
// Building blocks: Argon2id (password → KEK), XChaCha20-Poly1305 (AEAD),
// HKDF-SHA256 (per-file subkey derivation).
package crypto

import (
	"bytes"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"golang.org/x/crypto/argon2"
	"golang.org/x/crypto/chacha20poly1305"
)

// Params controls the Argon2id cost. Stored as JSON so we can dial the
// cost upward in future versions without breaking existing encrypted files.
type Params struct {
	KDF         string `json:"kdf,omitempty"`
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
		KDF:         KDFArgon2id,
		Memory:      64 * 1024,
		Time:        3,
		Parallelism: 4,
		KeyLen:      32,
		SaltLen:     16,
	}
}

const (
	KDFArgon2id = "argon2id"

	masterKeyLen        = 32
	minArgon2MemoryKiB  = 16 * 1024
	maxArgon2MemoryKiB  = 256 * 1024
	minArgon2Time       = 1
	maxArgon2Time       = 10
	minArgon2Parallel   = 1
	maxArgon2Parallel   = 16
	minSaltLen          = 16
	maxSaltLen          = 64
	maxParamsJSONLen    = 1024
	wrappedMasterKeyLen = chacha20poly1305.NonceSizeX + masterKeyLen + chacha20poly1305.Overhead
	keyCheckEnvelopeLen = chacha20poly1305.NonceSizeX + len("tdrive-key-check-v1") + chacha20poly1305.Overhead
)

// keyCheckPlaintext is decrypted whenever the password is entered.
// Anything fixed and nonempty works; the bytes themselves never need to
// be secret.
var keyCheckPlaintext = []byte("tdrive-key-check-v1")

var (
	ErrWrongPassword    = errors.New("crypto: wrong password")
	ErrCorruptKeyData   = errors.New("crypto: corrupt encryption key data")
	ErrInvalidKDFParams = errors.New("crypto: invalid KDF parameters")
	ErrUnsupportedKDF   = errors.New("crypto: unsupported KDF")
)

// DeriveKEK runs Argon2id against the password+salt with the supplied
// params and returns a key-encryption-key. Caller must zeroize when done.
func DeriveKEK(password, salt []byte, p Params) ([]byte, error) {
	params, err := normalizeParams(p)
	if err != nil {
		return nil, err
	}
	if err := validateSalt(salt, params); err != nil {
		return nil, ErrInvalidKDFParams
	}
	return argon2.IDKey(password, salt, params.Time, params.Memory, params.Parallelism, params.KeyLen), nil
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
	params, err := normalizeParams(p)
	if err != nil {
		return nil, err
	}
	salt := make([]byte, params.SaltLen)
	if _, err := rand.Read(salt); err != nil {
		return nil, err
	}
	return salt, nil
}

// MarshalParams encodes Params as JSON for storage.
func MarshalParams(p Params) (string, error) {
	params, err := normalizeParams(p)
	if err != nil {
		return "", err
	}
	b, err := json.Marshal(params)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// UnmarshalParams decodes Params previously written by MarshalParams.
func UnmarshalParams(s string) (Params, error) {
	if len(s) == 0 || len(s) > maxParamsJSONLen {
		return Params{}, ErrInvalidKDFParams
	}
	var wire struct {
		KDF         json.RawMessage `json:"kdf"`
		Memory      uint32          `json:"memory"`
		Time        uint32          `json:"time"`
		Parallelism uint8           `json:"parallelism"`
		KeyLen      uint32          `json:"key_len"`
		SaltLen     int             `json:"salt_len"`
	}
	decoder := json.NewDecoder(bytes.NewReader([]byte(s)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&wire); err != nil {
		return Params{}, ErrInvalidKDFParams
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return Params{}, ErrInvalidKDFParams
	}
	kdf := KDFArgon2id
	if len(wire.KDF) != 0 {
		if bytes.Equal(wire.KDF, []byte("null")) {
			return Params{}, ErrUnsupportedKDF
		}
		if err := json.Unmarshal(wire.KDF, &kdf); err != nil {
			return Params{}, ErrInvalidKDFParams
		}
		if kdf == "" {
			return Params{}, ErrUnsupportedKDF
		}
	}
	p := Params{
		KDF:         kdf,
		Memory:      wire.Memory,
		Time:        wire.Time,
		Parallelism: wire.Parallelism,
		KeyLen:      wire.KeyLen,
		SaltLen:     wire.SaltLen,
	}
	params, err := normalizeParams(p)
	if err != nil {
		return Params{}, err
	}
	return params, nil
}

// ValidateVaultMaterial validates all allocation-sensitive vault metadata
// received from an untrusted projection without running Argon2 or decrypting.
func ValidateVaultMaterial(salt []byte, p Params, wrappedMasterKey, keyCheck []byte) error {
	params, err := normalizeParams(p)
	if err != nil {
		return err
	}
	if err := validateSalt(salt, params); err != nil {
		return err
	}
	if len(wrappedMasterKey) != wrappedMasterKeyLen || len(keyCheck) != keyCheckEnvelopeLen {
		return ErrCorruptKeyData
	}
	return nil
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
	if len(wrapped) != wrappedMasterKeyLen {
		return nil, ErrCorruptKeyData
	}
	aead, err := chacha20poly1305.NewX(kek)
	if err != nil {
		return nil, err
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
	if len(blob) != keyCheckEnvelopeLen {
		return ErrCorruptKeyData
	}
	aead, err := chacha20poly1305.NewX(master)
	if err != nil {
		return err
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

func normalizeParams(p Params) (Params, error) {
	if p.KDF == "" {
		p.KDF = KDFArgon2id
	}
	if p.KDF != KDFArgon2id {
		return Params{}, ErrUnsupportedKDF
	}
	if p.Memory < minArgon2MemoryKiB || p.Memory > maxArgon2MemoryKiB ||
		p.Time < minArgon2Time || p.Time > maxArgon2Time ||
		p.Parallelism < minArgon2Parallel || p.Parallelism > maxArgon2Parallel ||
		p.KeyLen != masterKeyLen ||
		p.SaltLen < minSaltLen || p.SaltLen > maxSaltLen {
		return Params{}, ErrInvalidKDFParams
	}
	return p, nil
}

func validateSalt(salt []byte, p Params) error {
	if len(salt) != p.SaltLen || len(salt) < minSaltLen || len(salt) > maxSaltLen {
		return ErrInvalidKDFParams
	}
	return nil
}
