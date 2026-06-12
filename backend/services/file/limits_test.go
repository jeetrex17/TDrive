package file

import (
	"errors"
	"testing"

	tdcrypto "TDrive/backend/crypto"
)

func TestUploadByteSize(t *testing.T) {
	if got := uploadByteSize(1000, false); got != 1000 {
		t.Errorf("plain size = %d, want 1000", got)
	}
	if got, want := uploadByteSize(1000, true), tdcrypto.CiphertextSize(1000); got != want {
		t.Errorf("encrypted size = %d, want %d (ciphertext)", got, want)
	}
}

func TestCheckUploadSize(t *testing.T) {
	s := &Service{MaxUploadBytes: 1000}

	if err := s.checkUploadSize("ok.bin", 900, false); err != nil {
		t.Errorf("900 under 1000 cap should pass, got %v", err)
	}
	if err := s.checkUploadSize("edge.bin", 1000, false); err != nil {
		t.Errorf("exactly at the cap should pass, got %v", err)
	}

	err := s.checkUploadSize("big.bin", 1001, false)
	if err == nil || !errors.Is(err, ErrFileTooLarge) {
		t.Errorf("1001 over 1000 cap should be ErrFileTooLarge, got %v", err)
	}

	// Encryption overhead pushes a file that fits in plaintext over the cap.
	if err := s.checkUploadSize("enc.bin", 1000, true); err == nil || !errors.Is(err, ErrFileTooLarge) {
		t.Errorf("encrypted 1000 should exceed a 1000 cap, got %v", err)
	}

	// With no override, the standard 2 GiB cap applies: 1 GiB is fine.
	if err := (&Service{}).checkUploadSize("g.bin", 1<<30, false); err != nil {
		t.Errorf("1 GiB under the default cap should pass, got %v", err)
	}
}
