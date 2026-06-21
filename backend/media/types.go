// Package media contains the backend foundation for in-app media playback.
//
// The package intentionally starts below the player layer: it resolves a
// projected TDrive file into the ordered Telegram message bodies that contain
// its bytes. Native playback, loopback HTTP serving, and random-access crypto
// build on this stable model.
package media

import "errors"

var (
	ErrDBNotReady           = errors.New("media: db not ready")
	ErrFileNotFound         = errors.New("media: file not found")
	ErrIncompleteMultipart  = errors.New("media: multipart file is incomplete")
	ErrRangeClientNotReady  = errors.New("media: range client not ready")
	ErrPeerResolverNotReady = errors.New("media: peer resolver not ready")
	ErrEncryptedUnsupported = errors.New("media: encrypted playback is not implemented yet")
	ErrSessionNotFound      = errors.New("media: session not found")
)

// Segment is one stored Telegram document body in a logical TDrive file.
//
// For normal files there is exactly one segment and MsgID is the file's own
// message id. For multipart files, MsgID is a part message id while FileID on
// LogicalFile remains the manifest message id used by the UI.
type Segment struct {
	MsgID int64
	Size  int64
}

// LogicalFile is the media subsystem's canonical view of a projected file.
//
// StoredSize is the total bytes stored in Telegram. For encrypted files that is
// ciphertext size; PlaintextSize is the size exposed to the player. Encryption
// spans the concatenated stored stream, so multipart encrypted files are still
// decrypted as one logical stream.
type LogicalFile struct {
	ChannelID         int64
	FileID            int64
	Name              string
	StoredSize        int64
	PlaintextSize     int64
	Encrypted         bool
	EncryptionVersion int
	Multipart         bool
	Segments          []Segment
}

func (f LogicalFile) SegmentCount() int {
	return len(f.Segments)
}

type OpenResult struct {
	Token string      `json:"token"`
	URL   string      `json:"url"`
	Name  string      `json:"name"`
	Info  LogicalFile `json:"info"`
}
