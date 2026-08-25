package crypto

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"io"
	"math"

	"golang.org/x/crypto/chacha20poly1305"
	"golang.org/x/crypto/hkdf"
)

// On-wire layout written at the start of every encrypted file:
//
//	magic(4)       "TDE1"
//	flags(1)       reserved, currently 0
//	chunk_log2(1)  log2 of the plaintext chunk size; v1 = 16 (= 64 KiB)
//	file_salt(16)  random per-file salt for HKDF
//	nonce_prefix(20) random per-file nonce prefix; per-chunk nonce =
//	               prefix ‖ counter_be32 (last bit of counter = "final"
//	               marker so a truncated stream fails authentication)
//	plaintext_size(8) little-endian; informational, used as a sanity
//	               check vs. what we actually decrypt
//
// Total: 50 bytes header + 16 bytes AEAD tag per chunk.
const (
	streamMagic       = "TDE1"
	streamHeaderLen   = 4 + 1 + 1 + 16 + 20 + 8
	chunkSizeLog2     = 16
	chunkSizePlain    = 1 << chunkSizeLog2 // 64 KiB
	streamFileSaltLen = 16
	streamPrefixLen   = 20
	streamTagLen      = chacha20poly1305.Overhead // 16, AEAD tag per chunk
	finalChunkBit     = uint32(1) << 31
	// Keep every raw chunk counter below finalChunkBit so setting that bit is
	// an unambiguous final-chunk domain marker, including for the largest file.
	maxPlaintextSize = int64(finalChunkBit)*int64(chunkSizePlain) - 1
	hkdfInfo         = "tdrive/file/v1"
)

var (
	ErrShortHeader           = errors.New("crypto: encrypted stream truncated header")
	ErrBadMagic              = errors.New("crypto: not a TDrive encrypted stream")
	ErrUnsupported           = errors.New("crypto: unsupported stream version")
	ErrAuthFailed            = errors.New("crypto: ciphertext failed authentication")
	ErrNegativePlaintextSize = errors.New("crypto: negative plaintext size")
	ErrPlaintextTooLarge     = errors.New("crypto: plaintext size exceeds TDE1 stream capacity")
	ErrPlaintextSizeMismatch = errors.New("crypto: plaintext size does not match declared length")
	ErrInvalidMasterKey      = errors.New("crypto: master key must be 32 bytes")
)

// ValidatePlaintextSize checks whether a plaintext length is representable by
// the TDE1 31-bit chunk counter plus its explicit final chunk.
func ValidatePlaintextSize(plaintextSize int64) error {
	if plaintextSize < 0 {
		return ErrNegativePlaintextSize
	}
	if plaintextSize > maxPlaintextSize {
		return ErrPlaintextTooLarge
	}
	return nil
}

// EncryptStream reads plaintext from src and writes the TDrive encrypted
// stream to dst. plaintextSize is required (we record it in the header).
// The caller can read the same value back from DecryptStream's plaintext
// size return.
//
// Memory footprint is ~chunkSizePlain (64 KiB) regardless of file size.
func EncryptStream(src io.Reader, dst io.Writer, masterKey []byte, plaintextSize int64) error {
	if len(masterKey) != chacha20poly1305.KeySize {
		return ErrInvalidMasterKey
	}
	if err := ValidatePlaintextSize(plaintextSize); err != nil {
		return err
	}
	salt := make([]byte, streamFileSaltLen)
	defer clear(salt)
	if _, err := rand.Read(salt); err != nil {
		return err
	}
	noncePrefix := make([]byte, streamPrefixLen)
	defer clear(noncePrefix)
	if _, err := rand.Read(noncePrefix); err != nil {
		return err
	}

	subkey, err := deriveSubkey(masterKey, salt)
	if err != nil {
		return err
	}
	defer clear(subkey)
	aead, err := chacha20poly1305.NewX(subkey)
	if err != nil {
		return err
	}

	header := make([]byte, streamHeaderLen)
	defer clear(header)
	copy(header[0:4], streamMagic)
	header[4] = 0                   // flags
	header[5] = byte(chunkSizeLog2) // chunk size
	copy(header[6:6+streamFileSaltLen], salt)
	copy(header[22:22+streamPrefixLen], noncePrefix)
	binary.LittleEndian.PutUint64(header[42:50], uint64(plaintextSize))
	if err := writeExact(dst, header); err != nil {
		return err
	}

	buf := make([]byte, chunkSizePlain)
	defer clear(buf)
	var counter uint32
	remaining := plaintextSize
	for remaining > 0 {
		want := chunkSizePlain
		if remaining < int64(want) {
			want = int(remaining)
		}
		n, readErr := io.ReadFull(src, buf[:want])
		if readErr != nil {
			if errors.Is(readErr, io.EOF) || errors.Is(readErr, io.ErrUnexpectedEOF) {
				return ErrPlaintextSizeMismatch
			}
			return readErr
		}
		remaining -= int64(n)
		isFinal := remaining == 0 && n < chunkSizePlain
		if isFinal {
			if err := ensureNoExtraPlaintext(src); err != nil {
				return err
			}
		}

		nonce := buildNonce(noncePrefix, counter, isFinal)
		ct := aead.Seal(nil, nonce, buf[:n], nil)
		writeErr := writeExact(dst, ct)
		clear(nonce)
		clear(ct)
		if writeErr != nil {
			return writeErr
		}
		counter++

		if isFinal {
			return nil
		}
	}
	if err := ensureNoExtraPlaintext(src); err != nil {
		return err
	}
	nonce := buildNonce(noncePrefix, counter, true)
	ct := aead.Seal(nil, nonce, nil, nil)
	writeErr := writeExact(dst, ct)
	clear(nonce)
	clear(ct)
	return writeErr
}

// CiphertextSize returns the exact number of bytes EncryptStream writes for a
// plaintext of the given size: the fixed header plus a 16-byte tag per 64 KiB
// chunk (including the always-present final chunk). Callers use it to check the
// real upload size against Telegram's per-file limit before encrypting. It
// returns 0 for negative input and math.MaxInt64 above TDE1's counter capacity.
func CiphertextSize(plaintextSize int64) int64 {
	if plaintextSize < 0 {
		return 0
	}
	if plaintextSize > maxPlaintextSize {
		return math.MaxInt64
	}
	chunks := plaintextSize/chunkSizePlain + 1
	base := int64(streamHeaderLen) + plaintextSize
	if base < plaintextSize {
		return math.MaxInt64
	}
	maxTags := (math.MaxInt64 - base) / int64(streamTagLen)
	if chunks > maxTags {
		return math.MaxInt64
	}
	return base + chunks*int64(streamTagLen)
}

// DecryptStream is the inverse. It returns the plaintext size declared
// in the header so the caller can sanity-check or stream into a sized
// container.
func DecryptStream(src io.Reader, dst io.Writer, masterKey []byte) (int64, error) {
	if len(masterKey) != chacha20poly1305.KeySize {
		return 0, ErrInvalidMasterKey
	}
	header := make([]byte, streamHeaderLen)
	defer clear(header)
	if _, err := io.ReadFull(src, header); err != nil {
		return 0, ErrShortHeader
	}
	if string(header[0:4]) != streamMagic {
		return 0, ErrBadMagic
	}
	if header[4] != 0 || header[5] != byte(chunkSizeLog2) {
		return 0, ErrUnsupported
	}
	salt := header[6 : 6+streamFileSaltLen]
	noncePrefix := header[22 : 22+streamPrefixLen]
	declaredSize := binary.LittleEndian.Uint64(header[42:50])
	if declaredSize > math.MaxInt64 {
		return 0, ErrUnsupported
	}
	plaintextSize := int64(declaredSize)
	if err := ValidatePlaintextSize(plaintextSize); err != nil {
		return 0, ErrUnsupported
	}

	subkey, err := deriveSubkey(masterKey, salt)
	if err != nil {
		return 0, err
	}
	defer clear(subkey)
	aead, err := chacha20poly1305.NewX(subkey)
	if err != nil {
		return 0, err
	}

	chunkCipherCap := chunkSizePlain + aead.Overhead()
	buf := make([]byte, chunkCipherCap)
	defer clear(buf)
	var counter uint32
	var written int64
	for {
		n, readErr := io.ReadFull(src, buf)
		if readErr == io.EOF {
			// We always end with an explicit final chunk — running out
			// before seeing one means the stream is truncated.
			return 0, ErrAuthFailed
		}
		isFinal := readErr == io.ErrUnexpectedEOF
		if !isFinal && readErr != nil {
			return 0, readErr
		}
		ct := buf[:n]

		nonce := buildNonce(noncePrefix, counter, isFinal)
		pt, err := aead.Open(buf[:0], nonce, ct, nil)
		clear(nonce)
		if err != nil {
			return 0, ErrAuthFailed
		}
		plaintextLength := len(pt)
		writeErr := writeExact(dst, pt)
		clear(pt)
		if writeErr != nil {
			return 0, writeErr
		}
		written += int64(plaintextLength)
		counter++

		if isFinal {
			if written != plaintextSize {
				return 0, ErrAuthFailed
			}
			return plaintextSize, nil
		}
	}
}

func deriveSubkey(masterKey, salt []byte) ([]byte, error) {
	if len(masterKey) != chacha20poly1305.KeySize {
		return nil, ErrInvalidMasterKey
	}
	r := hkdf.New(sha256.New, masterKey, salt, []byte(hkdfInfo))
	subkey := make([]byte, chacha20poly1305.KeySize)
	if _, err := io.ReadFull(r, subkey); err != nil {
		clear(subkey)
		return nil, err
	}
	return subkey, nil
}

func buildNonce(prefix []byte, counter uint32, final bool) []byte {
	nonce := make([]byte, chacha20poly1305.NonceSizeX) // 24
	copy(nonce, prefix)
	c := counter
	if final {
		c |= finalChunkBit
	}
	binary.BigEndian.PutUint32(nonce[20:], c)
	return nonce
}

func ensureNoExtraPlaintext(src io.Reader) error {
	var extra [1]byte
	defer clear(extra[:])
	n, err := src.Read(extra[:])
	if n > 0 {
		return ErrPlaintextSizeMismatch
	}
	if err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	return nil
}

func writeExact(dst io.Writer, buffer []byte) error {
	written, err := dst.Write(buffer)
	if err != nil {
		return err
	}
	if written != len(buffer) {
		return io.ErrShortWrite
	}
	return nil
}
