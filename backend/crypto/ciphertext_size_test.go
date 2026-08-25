package crypto

import (
	"bytes"
	"crypto/rand"
	"errors"
	"io"
	"math"
	"testing"
)

func TestCiphertextSizeMatchesEncryptStream(t *testing.T) {
	master, err := NewMasterKey()
	if err != nil {
		t.Fatalf("NewMasterKey: %v", err)
	}

	sizes := []int64{
		0,
		1,
		1000,
		65535,
		65536,
		65537,
		131072,
		200000,
		5000000,
	}
	for _, n := range sizes {
		plain := make([]byte, n)
		if _, err := rand.Read(plain); err != nil {
			t.Fatalf("size=%d rand: %v", n, err)
		}

		var enc bytes.Buffer
		if err := EncryptStream(bytes.NewReader(plain), &enc, master, n); err != nil {
			t.Fatalf("size=%d encrypt: %v", n, err)
		}

		want := int(CiphertextSize(n))
		if enc.Len() != want {
			t.Fatalf("size=%d: CiphertextSize=%d, EncryptStream wrote %d", n, want, enc.Len())
		}
	}
}

func TestCiphertextSizeSaturatesOnOverflow(t *testing.T) {
	if got := CiphertextSize(math.MaxInt64); got != math.MaxInt64 {
		t.Fatalf("CiphertextSize(MaxInt64) = %d, want MaxInt64", got)
	}
}

func TestValidatePlaintextSizeEnforcesTDE1CounterCapacity(t *testing.T) {
	if finalIndex := maxPlaintextSize / int64(chunkSizePlain); finalIndex >= int64(finalChunkBit) {
		t.Fatalf("maximum plaintext final chunk index = %d, must keep the final-marker bit clear", finalIndex)
	}
	tests := []struct {
		name string
		size int64
		want error
	}{
		{name: "negative", size: -1, want: ErrNegativePlaintextSize},
		{name: "empty", size: 0},
		{name: "maximum", size: maxPlaintextSize},
		{name: "above maximum", size: maxPlaintextSize + 1, want: ErrPlaintextTooLarge},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidatePlaintextSize(tt.size)
			if !errors.Is(err, tt.want) || (tt.want == nil && err != nil) {
				t.Fatalf("ValidatePlaintextSize(%d) = %v, want %v", tt.size, err, tt.want)
			}
		})
	}
	if got := CiphertextSize(maxPlaintextSize + 1); got != math.MaxInt64 {
		t.Fatalf("CiphertextSize(above capacity) = %d, want MaxInt64", got)
	}
	key := bytes.Repeat([]byte{1}, 32)
	if err := EncryptStream(bytes.NewReader(nil), io.Discard, key, maxPlaintextSize+1); !errors.Is(err, ErrPlaintextTooLarge) {
		t.Fatalf("EncryptStream(above capacity) error = %v, want ErrPlaintextTooLarge", err)
	}
}

func TestEncryptStreamRejectsSourceLengthMismatch(t *testing.T) {
	key := bytes.Repeat([]byte{1}, 32)
	tests := []struct {
		name         string
		plaintext    []byte
		declaredSize int64
	}{
		{
			name:         "shorter than declared",
			plaintext:    bytes.Repeat([]byte{0x12}, chunkSizePlain-1),
			declaredSize: chunkSizePlain,
		},
		{
			name:         "longer than declared",
			plaintext:    append(bytes.Repeat([]byte{0x24}, chunkSizePlain), 0x25),
			declaredSize: chunkSizePlain,
		},
		{
			name:         "longer than declared partial final chunk",
			plaintext:    bytes.Repeat([]byte{0x36}, 10),
			declaredSize: 9,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := EncryptStream(bytes.NewReader(tt.plaintext), io.Discard, key, tt.declaredSize)
			if !errors.Is(err, ErrPlaintextSizeMismatch) {
				t.Fatalf("EncryptStream length mismatch error = %v, want ErrPlaintextSizeMismatch", err)
			}
		})
	}
}
