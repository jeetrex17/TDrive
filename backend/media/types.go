// Package media contains the backend foundation for in-app media playback.
//
// The package intentionally starts below the player layer: it resolves a
// projected TDrive file into the ordered Telegram message bodies that contain
// its bytes. Native playback, loopback HTTP serving, and random-access crypto
// build on this stable model.
package media

import (
	"context"
	"errors"
)

var (
	ErrDBNotReady           = errors.New("media: db not ready")
	ErrFileNotFound         = errors.New("media: file not found")
	ErrIncompleteMultipart  = errors.New("media: multipart file is incomplete")
	ErrRangeClientNotReady  = errors.New("media: range client not ready")
	ErrPeerResolverNotReady = errors.New("media: peer resolver not ready")
	ErrEncryptedUnsupported = errors.New("media: encrypted playback format is unsupported")
	ErrKeyUnavailable       = errors.New("media: encryption key is unavailable")
	ErrUnsupportedMediaType = errors.New("media: unsupported media type")
	ErrSessionNotFound      = errors.New("media: session not found")
	ErrThumbnailPending     = errors.New("media: thumbnail pending")
	ErrThumbnailUnavailable = errors.New("media: thumbnail unavailable")
)

// MasterKeyProvider supplies a caller-owned copy of the in-memory master key
// for encrypted media. Service clears returned key bytes after creating the
// per-session decryptors.
type MasterKeyProvider interface {
	MasterKey(ctx context.Context, channelID int64) ([]byte, error)
}

// MasterKeyProviderFunc adapts a function to MasterKeyProvider.
type MasterKeyProviderFunc func(ctx context.Context, channelID int64) ([]byte, error)

func (provider MasterKeyProviderFunc) MasterKey(ctx context.Context, channelID int64) ([]byte, error) {
	if provider == nil {
		return nil, ErrKeyUnavailable
	}
	return provider(ctx, channelID)
}

type StreamKind string

const (
	StreamKindUnknown StreamKind = "unknown"
	StreamKindVideo   StreamKind = "video"
	StreamKindAudio   StreamKind = "audio"
	StreamKindPDF     StreamKind = "pdf"
	StreamKindText    StreamKind = "text"
)

// Segment is one stored Telegram document body in a logical TDrive file.
//
// For normal files there is exactly one segment and MsgID is the file's own
// message id. For multipart files, MsgID is a part message id while FileID on
// LogicalFile remains the manifest message id used by the UI.
type Segment struct {
	MsgID int64 `json:"msg_id"`
	Size  int64 `json:"size"`
}

// LogicalFile is the media subsystem's canonical view of a projected file.
//
// StoredSize is the total bytes stored in Telegram. For encrypted files that is
// ciphertext size; PlaintextSize is the size exposed to the player. Encryption
// spans the concatenated stored stream, so multipart encrypted files are still
// decrypted as one logical stream.
type LogicalFile struct {
	ChannelID         int64     `json:"channel_id"`
	FileID            int64     `json:"file_id"`
	Revision          int64     `json:"revision"`
	Name              string    `json:"name"`
	StoredSize        int64     `json:"stored_size"`
	PlaintextSize     int64     `json:"plaintext_size"`
	Encrypted         bool      `json:"encrypted"`
	EncryptionVersion int       `json:"encryption_version"`
	Multipart         bool      `json:"multipart"`
	Segments          []Segment `json:"segments"`
}

func (f LogicalFile) SegmentCount() int {
	return len(f.Segments)
}

type OpenResult struct {
	Token         string      `json:"token"`
	URL           string      `json:"url"`
	ThumbnailURL  string      `json:"thumbnail_url"`
	Name          string      `json:"name"`
	Kind          StreamKind  `json:"kind"`
	MimeType      string      `json:"mime_type"`
	SupportsRange bool        `json:"supports_range"`
	Info          LogicalFile `json:"info"`
}
