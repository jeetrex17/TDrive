package crypto

import (
	"bytes"
	"crypto/rand"
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
