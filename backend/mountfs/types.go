// Package mountfs implements TDrive's protocol-neutral, read-only filesystem
// semantics. Protocol and OS adapters translate their requests into this small
// API; the package has no dependency on WebDAV or core.Engine.
package mountfs

import (
	"context"
	"time"
)

// RootID is the stable parent ID used for a drive's root directory.
const RootID = ""

// Kind identifies the only node kinds supported by the read-only filesystem.
type Kind string

const (
	KindFile      Kind = "file"
	KindDirectory Kind = "directory"
)

// SourceEntry is an immutable projection record supplied by DirectorySource.
// ContentRef is opaque to mountfs and may identify a Telegram message,
// multipart manifest, or another content revision understood by ContentOpener.
type SourceEntry struct {
	ID          string
	ParentID    string
	Name        string
	Kind        Kind
	Size        int64
	ModTime     time.Time
	Encrypted   bool
	ContentRef  string
	ContentHash string
	Revision    int64
	UploadUUID  string
	PartCount   int
}

// Entry is the portable metadata exposed to filesystem adapters. Name is the
// exported, collision-free component; SourceName preserves projection data for
// diagnostics and future platform metadata.
type Entry struct {
	ChannelID   int64
	ID          string
	ParentID    string
	Name        string
	SourceName  string
	Kind        Kind
	Size        int64
	ModTime     time.Time
	Encrypted   bool
	ContentRef  string
	ContentHash string
	Revision    int64
	UploadUUID  string
	PartCount   int
}

// DirectorySource lists the direct children of one projected directory.
// Implementations must honor cancellation and should return a consistent
// point-in-time result for each call.
type DirectorySource interface {
	ListDirectory(ctx context.Context, channelID int64, parentID string) ([]SourceEntry, error)
}

// RandomAccessContent is an opened immutable content revision. ReadAt must
// honor context cancellation; callers may issue reads concurrently.
type RandomAccessContent interface {
	ReadAt(ctx context.Context, buffer []byte, offset int64) (int, error)
	Close() error
}

// ContentOpener opens the immutable content revision represented by entry.
// mountfs only invokes it for validated file entries.
type ContentOpener interface {
	OpenContent(ctx context.Context, channelID int64, entry SourceEntry) (RandomAccessContent, error)
}
