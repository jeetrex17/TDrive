package media

import (
	"context"
	"testing"

	"TDrive/backend/projection"
)

// The first file read of a run pays a data-center hop that later reads do not.
// Warming picks any live file and reads a few kilobytes on each pooled
// connection so a viewer never waits on it; a file with room for fewer
// aligned reads than the pool has connections gets only what fits.
func TestWarmTransportReadsASmallRangePerPooledConnection(t *testing.T) {
	db := newResolverTestDB(t)
	mustApplyOp(t, db, 10, projection.Op{Type: projection.OpFileUpload, Parent: projection.RootParent, Name: "old.mp4", FileSize: 4096})
	mustApplyOp(t, db, 11, projection.Op{Type: projection.OpFileUpload, Parent: projection.RootParent, Name: "new.mp4", FileSize: 8192})

	ranges := newRecordingRangeClient(8192)
	service := NewService(Config{
		Peers:  staticPeerResolver{},
		Ranges: ranges,
		DB:     db,
	})

	service.WarmTransport(context.Background(), testChannelID)

	offsets := map[int64]bool{}
	for range 2 {
		offsets[ranges.awaitRead(t)] = true
	}
	if !offsets[0] || !offsets[4096] {
		t.Fatalf("warm read offsets = %v, want the two aligned reads the file has room for", offsets)
	}
	if got := len(ranges.reads); got != 0 {
		t.Fatalf("extra warm reads = %d, want none beyond the file", got)
	}
	if got := ranges.lastLength(); got != warmReadBytes {
		t.Fatalf("warm read length = %d, want %d", got, warmReadBytes)
	}
}

// Nothing to warm with is normal on an empty drive and must not be fatal.
func TestWarmTransportIsQuietWithoutFiles(t *testing.T) {
	db := newResolverTestDB(t)
	ranges := newRecordingRangeClient(4096)
	service := NewService(Config{
		Peers:  staticPeerResolver{},
		Ranges: ranges,
		DB:     db,
	})

	service.WarmTransport(context.Background(), testChannelID)

	if got := len(ranges.reads); got != 0 {
		t.Fatalf("range reads = %d, want none", got)
	}
}
