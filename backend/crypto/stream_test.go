package crypto

import (
	"bytes"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"testing"
)

func TestStreamRoundTrip(t *testing.T) {
	master, err := NewMasterKey()
	if err != nil {
		t.Fatalf("NewMasterKey: %v", err)
	}

	sizes := []int{
		0,
		1,
		chunkSizePlain - 1,
		chunkSizePlain,
		chunkSizePlain + 1,
		chunkSizePlain*3 + 17,
		1 << 20,
	}
	for _, n := range sizes {
		plain := make([]byte, n)
		if _, err := rand.Read(plain); err != nil {
			t.Fatalf("rand: %v", err)
		}

		var enc bytes.Buffer
		if err := EncryptStream(bytes.NewReader(plain), &enc, master, int64(n)); err != nil {
			t.Fatalf("size=%d encrypt: %v", n, err)
		}

		var dec bytes.Buffer
		size, err := DecryptStream(&enc, &dec, master)
		if err != nil {
			t.Fatalf("size=%d decrypt: %v", n, err)
		}
		if size != int64(n) {
			t.Fatalf("size=%d: declared plaintext size %d", n, size)
		}
		if !bytes.Equal(dec.Bytes(), plain) {
			t.Fatalf("size=%d: round trip mismatch", n)
		}
	}
}

func TestStreamRejectsWrongKey(t *testing.T) {
	master, _ := NewMasterKey()
	other, _ := NewMasterKey()
	plain := []byte("hello world")

	var enc bytes.Buffer
	if err := EncryptStream(bytes.NewReader(plain), &enc, master, int64(len(plain))); err != nil {
		t.Fatal(err)
	}
	var dec bytes.Buffer
	if _, err := DecryptStream(&enc, &dec, other); err != ErrAuthFailed {
		t.Fatalf("want ErrAuthFailed, got %v", err)
	}
}

func TestStreamRejectsTamperedChunk(t *testing.T) {
	master, _ := NewMasterKey()
	plain := bytes.Repeat([]byte{0xAB}, chunkSizePlain*2+5)

	var enc bytes.Buffer
	if err := EncryptStream(bytes.NewReader(plain), &enc, master, int64(len(plain))); err != nil {
		t.Fatal(err)
	}
	b := enc.Bytes()
	// Flip a byte in the first chunk's ciphertext (well past the header).
	b[streamHeaderLen+1] ^= 0x80

	var dec bytes.Buffer
	if _, err := DecryptStream(bytes.NewReader(b), &dec, master); err != ErrAuthFailed {
		t.Fatalf("want ErrAuthFailed, got %v", err)
	}
}

func TestStreamRejectsTruncated(t *testing.T) {
	master, _ := NewMasterKey()
	plain := bytes.Repeat([]byte{0xCD}, chunkSizePlain*3)

	var enc bytes.Buffer
	if err := EncryptStream(bytes.NewReader(plain), &enc, master, int64(len(plain))); err != nil {
		t.Fatal(err)
	}
	b := enc.Bytes()
	truncated := b[:len(b)-32] // chop off part of the final chunk

	var dec bytes.Buffer
	if _, err := DecryptStream(bytes.NewReader(truncated), &dec, master); err != ErrAuthFailed {
		t.Fatalf("want ErrAuthFailed, got %v", err)
	}
}

func TestStreamLargeFile(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping large-file test in -short mode")
	}
	master, _ := NewMasterKey()
	const size = 50 * 1024 * 1024 // 50 MiB
	src := io.LimitReader(zeroReader{}, size)

	var enc bytes.Buffer
	if err := EncryptStream(src, &enc, master, size); err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	var sink countWriter
	got, err := DecryptStream(&enc, &sink, master)
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	if got != size {
		t.Fatalf("plaintext size %d", got)
	}
	if sink.n != size {
		t.Fatalf("written bytes %d", sink.n)
	}
}

func TestEncryptStreamRejectsCounterOverflow(t *testing.T) {
	master, _ := NewMasterKey()
	var enc bytes.Buffer
	err := EncryptStream(bytes.NewReader(nil), &enc, master, maxPlaintextSize+1)
	if err == nil {
		t.Fatal("EncryptStream accepted plaintext beyond the stream counter capacity")
	}
}

func TestStreamRejectsInvalidMasterKeyLengths(t *testing.T) {
	t.Parallel()

	for _, size := range []int{0, 31, 33, 128} {
		t.Run(fmt.Sprintf("key-%d", size), func(t *testing.T) {
			t.Parallel()
			key := bytes.Repeat([]byte{1}, size)
			var ciphertext bytes.Buffer
			if err := EncryptStream(bytes.NewReader(nil), &ciphertext, key, 0); !errors.Is(err, ErrInvalidMasterKey) {
				t.Fatalf("EncryptStream key length %d error = %v, want ErrInvalidMasterKey", size, err)
			}
			if _, err := DecryptStream(bytes.NewReader(make([]byte, streamHeaderLen)), io.Discard, key); !errors.Is(err, ErrInvalidMasterKey) {
				t.Fatalf("DecryptStream key length %d error = %v, want ErrInvalidMasterKey", size, err)
			}
		})
	}
}

func TestStreamRejectsShortWrites(t *testing.T) {
	t.Parallel()

	key := bytes.Repeat([]byte{1}, 32)
	if err := EncryptStream(bytes.NewReader(nil), shortNilWriter{}, key, 0); !errors.Is(err, io.ErrShortWrite) {
		t.Fatalf("EncryptStream short header write error = %v, want io.ErrShortWrite", err)
	}

	plaintext := []byte("secret")
	var ciphertext bytes.Buffer
	if err := EncryptStream(bytes.NewReader(plaintext), &ciphertext, key, int64(len(plaintext))); err != nil {
		t.Fatalf("EncryptStream fixture: %v", err)
	}
	if _, err := DecryptStream(bytes.NewReader(ciphertext.Bytes()), shortNilWriter{}, key); !errors.Is(err, io.ErrShortWrite) {
		t.Fatalf("DecryptStream short plaintext write error = %v, want io.ErrShortWrite", err)
	}
}

func TestDecryptStreamRejectsUnsupportedFlagsAndImpossibleSize(t *testing.T) {
	t.Parallel()

	key := bytes.Repeat([]byte{2}, 32)
	var encrypted bytes.Buffer
	if err := EncryptStream(bytes.NewReader(nil), &encrypted, key, 0); err != nil {
		t.Fatalf("EncryptStream fixture: %v", err)
	}

	tests := []struct {
		name   string
		mutate func([]byte)
	}{
		{name: "reserved flags", mutate: func(stream []byte) { stream[4] = 1 }},
		{name: "uint64 size", mutate: func(stream []byte) { binary.LittleEndian.PutUint64(stream[42:50], math.MaxUint64) }},
		{name: "counter capacity", mutate: func(stream []byte) { binary.LittleEndian.PutUint64(stream[42:50], uint64(maxPlaintextSize)+1) }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			stream := append([]byte(nil), encrypted.Bytes()...)
			test.mutate(stream)
			var plaintext bytes.Buffer
			if _, err := DecryptStream(bytes.NewReader(stream), &plaintext, key); !errors.Is(err, ErrUnsupported) {
				t.Fatalf("DecryptStream error = %v, want ErrUnsupported", err)
			}
			if plaintext.Len() != 0 {
				t.Fatalf("DecryptStream wrote %d bytes before metadata rejection", plaintext.Len())
			}
		})
	}
}

type shortNilWriter struct{}

func (shortNilWriter) Write(buffer []byte) (int, error) {
	if len(buffer) == 0 {
		return 0, nil
	}
	return len(buffer) - 1, nil
}

type zeroReader struct{}

func (zeroReader) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = 0
	}
	return len(p), nil
}

type countWriter struct{ n int64 }

func (w *countWriter) Write(p []byte) (int, error) {
	w.n += int64(len(p))
	return len(p), nil
}
