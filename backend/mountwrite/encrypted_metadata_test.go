package mountwrite

import (
	"crypto/sha256"
	"math"
	"testing"

	tdcrypto "TDrive/backend/crypto"
)

func TestValidTDE1Metadata(t *testing.T) {
	t.Parallel()

	storedHash := sha256.Sum256([]byte("stored"))
	plaintextHash := sha256.Sum256([]byte("plaintext"))
	tests := []struct {
		name          string
		plaintextSize int64
		storedSize    int64
		plaintextHash [sha256.Size]byte
		storedHash    [sha256.Size]byte
		want          bool
	}{
		{
			name:          "valid",
			plaintextSize: 12,
			storedSize:    tdcrypto.CiphertextSize(12),
			storedHash:    storedHash,
			want:          true,
		},
		{
			name:          "plaintext hash present",
			plaintextSize: 12,
			storedSize:    tdcrypto.CiphertextSize(12),
			plaintextHash: plaintextHash,
			storedHash:    storedHash,
		},
		{
			name:          "stored hash missing",
			plaintextSize: 12,
			storedSize:    tdcrypto.CiphertextSize(12),
		},
		{
			name:          "stored size mismatch",
			plaintextSize: 12,
			storedSize:    tdcrypto.CiphertextSize(12) - 1,
			storedHash:    storedHash,
		},
		{
			name:          "plaintext exceeds format",
			plaintextSize: math.MaxInt64,
			storedSize:    math.MaxInt64,
			storedHash:    storedHash,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if got := validTDE1Metadata(
				test.plaintextSize,
				test.storedSize,
				test.plaintextHash,
				test.storedHash,
			); got != test.want {
				t.Fatalf("validTDE1Metadata() = %v, want %v", got, test.want)
			}
		})
	}
}
