package file

import (
	"errors"
	"fmt"

	tdcrypto "TDrive/backend/crypto"
)

// Telegram caps a single uploaded document at ~2 GiB. A file whose stored
// (ciphertext) size fits in one message uploads as one message; a larger file
// is split into multiple part messages plus a manifest (see uploadMultipart).
// The limits are uniform for every account — we don't special-case Premium.
const (
	// MaxPartBytes is the largest stored (ciphertext) size we put in a single
	// Telegram message. Kept a hair under Telegram's 2 GiB document limit so
	// encryption overhead never pushes a part over the wire limit.
	MaxPartBytes int64 = 1900 * 1024 * 1024 // ~1.855 GiB

	// LargeFileMaxBytes is the largest stored size we will split and upload.
	// Beyond this a file is rejected rather than producing an unwieldy number
	// of parts.
	LargeFileMaxBytes int64 = 40 * 1024 * 1024 * 1024 // 40 GiB

	// MaxParts bounds the parts per file (defense in depth alongside
	// LargeFileMaxBytes; 40 GiB / ~1.86 GiB ≈ 22).
	MaxParts = 32
)

// ErrFileTooLarge is wrapped into the error returned for a file beyond the hard
// cap, so import can classify a skipped file with errors.Is.
var ErrFileTooLarge = errors.New("file exceeds the maximum upload size")

// maxPartBytes is the active per-part ceiling: the Service override when set
// (tests force splitting at small sizes), else MaxPartBytes.
func (s *Service) maxPartBytes() int64 {
	if s.MaxUploadBytes > 0 {
		return s.MaxUploadBytes
	}
	return MaxPartBytes
}

// largeFileMaxBytes is the active hard cap on a file's stored size. With a part
// override set (tests), scale to MaxParts parts so the two limits stay
// consistent; otherwise use LargeFileMaxBytes.
func (s *Service) largeFileMaxBytes() int64 {
	if s.MaxUploadBytes > 0 {
		return s.MaxUploadBytes * int64(MaxParts)
	}
	return LargeFileMaxBytes
}

// uploadByteSize is the number of bytes actually sent to Telegram for a file of
// the given plaintext size: the ciphertext size when encrypting (the header +
// per-chunk tags can push a file just under a limit over it), else the plaintext
// size itself.
func uploadByteSize(plaintextSize int64, encrypt bool) int64 {
	if encrypt {
		return tdcrypto.CiphertextSize(plaintextSize)
	}
	return plaintextSize
}

// planUpload decides how a file is stored. It returns the stored (on-Telegram)
// size, whether the file must be split into multiple parts, and an
// ErrFileTooLarge-wrapped error when the file is beyond the hard cap.
func (s *Service) planUpload(name string, plaintextSize int64, encrypt bool) (storedSize int64, multipart bool, err error) {
	storedSize = uploadByteSize(plaintextSize, encrypt)
	if storedSize > s.largeFileMaxBytes() {
		return 0, false, fmt.Errorf("%s (%s) exceeds the %s maximum upload size: %w",
			name, humanByteSize(plaintextSize), humanByteSize(s.largeFileMaxBytes()), ErrFileTooLarge)
	}
	return storedSize, storedSize > s.maxPartBytes(), nil
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
