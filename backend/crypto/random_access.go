package crypto

import (
	"context"
	"encoding/binary"
	"errors"
	"io"
	"math"
	"sync"
	"sync/atomic"

	"golang.org/x/crypto/chacha20poly1305"
)

var (
	// ErrCiphertextSize reports disagreement between the TDE1 header and the
	// immutable stored object size. The exact size is part of the format's
	// truncation detection: every stream includes an authenticated final chunk.
	ErrCiphertextSize  = errors.New("crypto: encrypted stream size does not match its header")
	ErrDecryptorClosed = errors.New("crypto: random-access decryptor is closed")
	ErrNilContext      = errors.New("crypto: context is required")
)

// ContextReaderAt is the storage boundary used by RandomAccessDecryptor.
// Implementations must honor cancellation and follow io.ReaderAt's short-read
// contract.
type ContextReaderAt interface {
	ReadAt(ctx context.Context, dst []byte, offset int64) (int, error)
}

// RandomAccessDecryptor exposes authenticated plaintext ranges from one TDE1
// ciphertext stream. It retains only the per-file subkey and immutable header
// metadata; each read allocates at most one ciphertext chunk (64 KiB + tag).
// It is safe for concurrent ReadAt calls and Close.
type RandomAccessDecryptor struct {
	mu             sync.RWMutex
	closeOnce      sync.Once
	closed         atomic.Bool
	lifetime       context.Context
	cancelLifetime context.CancelFunc
	source         ContextReaderAt
	plaintextSize  int64
	storedSize     int64
	noncePrefix    []byte
	subkey         []byte
}

// NewRandomAccessDecryptor validates the TDE1 header and stored size, derives a
// per-file key, and authenticates the explicit final chunk. Authenticating the
// final chunk during open detects a wrong master key and truncated tail before
// a filesystem handle is published.
func NewRandomAccessDecryptor(
	ctx context.Context,
	source ContextReaderAt,
	storedSize int64,
	masterKey []byte,
) (*RandomAccessDecryptor, error) {
	if ctx == nil {
		return nil, ErrNilContext
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(masterKey) != chacha20poly1305.KeySize {
		return nil, ErrInvalidMasterKey
	}
	if source == nil {
		return nil, errors.New("crypto: ciphertext reader is required")
	}
	if storedSize < streamHeaderLen {
		return nil, ErrShortHeader
	}

	header := make([]byte, streamHeaderLen)
	if err := readContextAtFull(ctx, source, header, 0); err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		return nil, ErrShortHeader
	}
	plaintextSize, salt, noncePrefix, err := parseRandomAccessHeader(header, storedSize)
	clear(header)
	if err != nil {
		return nil, err
	}

	subkey, err := deriveSubkey(masterKey, salt)
	clear(salt)
	if err != nil {
		return nil, err
	}
	valid := false
	defer func() {
		if !valid {
			clear(subkey)
		}
	}()

	if err := authenticateFinalChunk(ctx, source, plaintextSize, noncePrefix, subkey); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	lifetime, cancelLifetime := context.WithCancel(context.Background())
	valid = true
	return &RandomAccessDecryptor{
		lifetime:       lifetime,
		cancelLifetime: cancelLifetime,
		source:         source,
		plaintextSize:  plaintextSize,
		storedSize:     storedSize,
		noncePrefix:    noncePrefix,
		subkey:         subkey,
	}, nil
}

func parseRandomAccessHeader(header []byte, storedSize int64) (int64, []byte, []byte, error) {
	if len(header) != streamHeaderLen {
		return 0, nil, nil, ErrShortHeader
	}
	if string(header[0:4]) != streamMagic {
		return 0, nil, nil, ErrBadMagic
	}
	if header[4] != 0 || header[5] != byte(chunkSizeLog2) {
		return 0, nil, nil, ErrUnsupported
	}
	declaredSize := binary.LittleEndian.Uint64(header[42:50])
	if declaredSize > uint64(math.MaxInt64) || declaredSize > uint64(maxPlaintextSize) {
		return 0, nil, nil, ErrUnsupported
	}
	plaintextSize := int64(declaredSize)
	if CiphertextSize(plaintextSize) != storedSize {
		return 0, nil, nil, ErrCiphertextSize
	}
	salt := append([]byte(nil), header[6:6+streamFileSaltLen]...)
	noncePrefix := append([]byte(nil), header[22:22+streamPrefixLen]...)
	return plaintextSize, salt, noncePrefix, nil
}

func authenticateFinalChunk(
	ctx context.Context,
	source ContextReaderAt,
	plaintextSize int64,
	noncePrefix, subkey []byte,
) error {
	finalIndex := plaintextSize / int64(chunkSizePlain)
	finalPlaintextSize := plaintextSize % int64(chunkSizePlain)
	finalCiphertextSize := finalPlaintextSize + int64(streamTagLen)
	finalOffset, ok := ciphertextChunkOffset(finalIndex)
	if !ok {
		return ErrCiphertextSize
	}

	ciphertext := make([]byte, int(finalCiphertextSize))
	defer clear(ciphertext)
	if err := readContextAtFull(ctx, source, ciphertext, finalOffset); err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		return ErrAuthFailed
	}
	aead, err := chacha20poly1305.NewX(subkey)
	if err != nil {
		return err
	}
	nonce := buildNonce(noncePrefix, uint32(finalIndex), true)
	defer clear(nonce)
	plaintext, err := aead.Open(ciphertext[:0], nonce, ciphertext, nil)
	if err != nil || int64(len(plaintext)) != finalPlaintextSize {
		return ErrAuthFailed
	}
	return nil
}

// Size returns the authenticated plaintext size declared by the TDE1 stream.
func (decryptor *RandomAccessDecryptor) Size() int64 {
	if decryptor == nil {
		return 0
	}
	return decryptor.plaintextSize
}

// ReadAt follows io.ReaderAt's EOF contract. Every touched chunk is
// authenticated before any bytes from that chunk are copied to dst.
func (decryptor *RandomAccessDecryptor) ReadAt(
	ctx context.Context,
	dst []byte,
	offset int64,
) (int, error) {
	if ctx == nil {
		return 0, ErrNilContext
	}
	if decryptor == nil {
		return 0, ErrDecryptorClosed
	}
	if offset < 0 {
		return 0, errors.New("crypto: negative read offset")
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if decryptor.closed.Load() || decryptor.lifetime == nil {
		return 0, ErrDecryptorClosed
	}

	readCtx, cancelRead := context.WithCancel(ctx)
	stopLifetimeLink := context.AfterFunc(decryptor.lifetime, cancelRead)
	defer func() {
		stopLifetimeLink()
		cancelRead()
	}()
	if decryptor.lifetime.Err() != nil || decryptor.closed.Load() {
		return 0, ErrDecryptorClosed
	}

	decryptor.mu.RLock()
	defer decryptor.mu.RUnlock()
	if decryptor.closed.Load() || decryptor.source == nil || len(decryptor.subkey) == 0 {
		return 0, ErrDecryptorClosed
	}
	if offset >= decryptor.plaintextSize {
		return 0, io.EOF
	}
	if len(dst) == 0 {
		return 0, nil
	}

	want := len(dst)
	if remaining := decryptor.plaintextSize - offset; int64(want) > remaining {
		want = int(remaining)
	}
	aead, err := chacha20poly1305.NewX(decryptor.subkey)
	if err != nil {
		return 0, err
	}
	chunkBuffer := make([]byte, chunkSizePlain+streamTagLen)
	defer clear(chunkBuffer)

	done := 0
	for done < want {
		if err := readCtx.Err(); err != nil {
			if decryptor.closed.Load() {
				return done, ErrDecryptorClosed
			}
			return done, err
		}
		absolute := offset + int64(done)
		chunkIndex := absolute / int64(chunkSizePlain)
		withinChunk := absolute % int64(chunkSizePlain)
		final := chunkIndex == decryptor.plaintextSize/int64(chunkSizePlain)
		plaintextInChunk := int64(chunkSizePlain)
		if final {
			plaintextInChunk = decryptor.plaintextSize % int64(chunkSizePlain)
		}
		ciphertextSize := int(plaintextInChunk) + streamTagLen
		ciphertextOffset, ok := ciphertextChunkOffset(chunkIndex)
		if !ok || ciphertextOffset+int64(ciphertextSize) > decryptor.storedSize {
			return done, ErrCiphertextSize
		}

		ciphertext := chunkBuffer[:ciphertextSize]
		if readErr := readContextAtFull(readCtx, decryptor.source, ciphertext, ciphertextOffset); readErr != nil {
			if decryptor.closed.Load() {
				return done, ErrDecryptorClosed
			}
			if ctxErr := readCtx.Err(); ctxErr != nil {
				return done, ctxErr
			}
			if errors.Is(readErr, io.EOF) || errors.Is(readErr, io.ErrUnexpectedEOF) || errors.Is(readErr, io.ErrNoProgress) {
				return done, ErrAuthFailed
			}
			return done, readErr
		}
		nonce := buildNonce(decryptor.noncePrefix, uint32(chunkIndex), final)
		plaintext, openErr := aead.Open(ciphertext[:0], nonce, ciphertext, nil)
		clear(nonce)
		if openErr != nil || int64(len(plaintext)) != plaintextInChunk {
			return done, ErrAuthFailed
		}

		need := want - done
		if available := int(plaintextInChunk - withinChunk); need > available {
			need = available
		}
		copy(dst[done:done+need], plaintext[withinChunk:withinChunk+int64(need)])
		done += need
	}
	if done < len(dst) {
		return done, io.EOF
	}
	return done, nil
}

// Close waits for in-flight reads, clears the retained derived key, and makes
// all future reads fail. It is idempotent.
func (decryptor *RandomAccessDecryptor) Close() error {
	if decryptor == nil {
		return nil
	}
	decryptor.closeOnce.Do(func() {
		decryptor.closed.Store(true)
		if decryptor.cancelLifetime != nil {
			decryptor.cancelLifetime()
		}
		decryptor.mu.Lock()
		clear(decryptor.subkey)
		decryptor.subkey = nil
		clear(decryptor.noncePrefix)
		decryptor.noncePrefix = nil
		decryptor.source = nil
		decryptor.mu.Unlock()
	})
	return nil
}

func ciphertextChunkOffset(chunkIndex int64) (int64, bool) {
	if chunkIndex < 0 {
		return 0, false
	}
	stride := int64(chunkSizePlain + streamTagLen)
	if chunkIndex > (math.MaxInt64-int64(streamHeaderLen))/stride {
		return 0, false
	}
	return int64(streamHeaderLen) + chunkIndex*stride, true
}

func readContextAtFull(
	ctx context.Context,
	source ContextReaderAt,
	dst []byte,
	offset int64,
) error {
	read := 0
	for read < len(dst) {
		if err := ctx.Err(); err != nil {
			return err
		}
		n, err := source.ReadAt(ctx, dst[read:], offset+int64(read))
		if n < 0 || n > len(dst)-read {
			return io.ErrUnexpectedEOF
		}
		read += n
		if read == len(dst) {
			return nil
		}
		if err != nil {
			return err
		}
		if n == 0 {
			return io.ErrNoProgress
		}
	}
	return nil
}

var _ ContextReaderAt = (*RandomAccessDecryptor)(nil)
