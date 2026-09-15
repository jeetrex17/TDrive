package tgclient

import "context"

const (
	// RangeReadAlignment is Telegram's upload.getFile offset alignment.
	RangeReadAlignment int64 = 4 * 1024

	// RangeReadMaxBytes is the largest single range request TDrive asks the
	// low-level range client to issue. RangeReader splits larger app reads.
	RangeReadMaxBytes = 1024 * 1024
)

// DocumentRef is the stable Telegram document identity needed for ranged
// reads. Implementations may refresh FileReference internally when Telegram
// expires it; callers should treat this as an opaque descriptor.
type DocumentRef struct {
	Peer          InputPeer
	MsgID         int64
	Size          int64
	Name          string
	DocumentID    int64
	AccessHash    int64
	FileReference []byte
	// DCID is the data center holding the document's bytes. Range reads go
	// straight there over a pooled connection; zero means unknown, which falls
	// back to the primary connection and Telegram's FILE_MIGRATE redirect.
	DCID int
}

// DocumentBatchResolver resolves several messages of one channel in a single
// Telegram call. A multipart file has one segment per part, and opening it
// should not cost a round trip per part. Refs come back in msgIDs order; a
// missing or non-document message fails the whole batch.
type DocumentBatchResolver interface {
	ResolveDocuments(ctx context.Context, peer InputPeer, msgIDs []int64) ([]DocumentRef, error)
}

// RangeClient is the low-level byte-range surface used by media playback. It
// is intentionally separate from Client because the existing Client interface
// is whole-file oriented and used by mature upload/download/sync paths.
//
// ReadDocumentRange receives one RangeReader block read: offset is 4 KiB
// aligned, len(dst) is >0 and <=1 MiB, and the read does not cross a 1 MiB
// upload.getFile boundary. len(dst) is the number of bytes the caller wants;
// the final block may be smaller than 4 KiB or not a 4 KiB multiple.
//
// Real Telegram implementations must round their upload.getFile limit up to a
// valid Telegram value when needed, handle Telegram's EOF short-read at the
// document tail, and copy only the requested bytes into dst. On success they
// should return len(dst). Alignment, 1 MiB splitting, retries, caching, and
// duplicate suppression live in backend/media.RangeReader.
type RangeClient interface {
	ResolveDocument(ctx context.Context, peer InputPeer, msgID int64) (DocumentRef, error)
	ReadDocumentRange(ctx context.Context, ref DocumentRef, offset int64, dst []byte) (int, error)
}
