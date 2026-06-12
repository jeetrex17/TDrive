package file

import (
	"errors"
	"fmt"

	tdcrypto "TDrive/backend/crypto"
)

// Telegram caps a single uploaded file at 2 GB for standard accounts and 4 GB
// for Premium. We guard against it before uploading so an oversize file fails
// fast with a clear message (and folder/archive imports can skip it and report)
// instead of dying mid-stream with an opaque MTProto error.
const (
	MaxUploadBytesStandard int64 = 2 * 1024 * 1024 * 1024 // 2 GiB
	MaxUploadBytesPremium  int64 = 4 * 1024 * 1024 * 1024 // 4 GiB
)

// ErrFileTooLarge is wrapped into the error returned for an oversize file, so
// import can classify a skipped file with errors.Is.
var ErrFileTooLarge = errors.New("file exceeds the per-file upload limit")

// maxUploadBytes is the active per-file limit: the Service override when set
// (raised to the Premium cap once the account is known to be Premium), else the
// standard 2 GiB cap.
func (s *Service) maxUploadBytes() int64 {
	if s.MaxUploadBytes > 0 {
		return s.MaxUploadBytes
	}
	return MaxUploadBytesStandard
}

// uploadByteSize is the number of bytes actually sent to Telegram for a file of
// the given plaintext size: the ciphertext size when the upload is encrypted
// (the header + per-chunk tags can push a file just under the limit over it),
// otherwise the plaintext size itself.
func uploadByteSize(plaintextSize int64, encrypt bool) int64 {
	if encrypt {
		return tdcrypto.CiphertextSize(plaintextSize)
	}
	return plaintextSize
}

// checkUploadSize returns an ErrFileTooLarge-wrapped error (with a human-readable
// message) when the file would exceed the active per-file limit once uploaded.
func (s *Service) checkUploadSize(name string, plaintextSize int64, encrypt bool) error {
	limit := s.maxUploadBytes()
	if uploadByteSize(plaintextSize, encrypt) <= limit {
		return nil
	}
	return fmt.Errorf("%s (%s) exceeds the %s per-file limit: %w",
		name, humanByteSize(plaintextSize), humanByteSize(limit), ErrFileTooLarge)
}

// humanByteSize renders a byte count as a short human-readable string (e.g.
// "1.9 GB"), using binary units with decimal-style labels.
func humanByteSize(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for v := n / unit; v >= unit; v /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMGTPE"[exp])
}
