package media

import (
	"context"
	"sync"
	"testing"

	"TDrive/backend/projection"
	"TDrive/backend/tgclient"
)

// The first file read of a run pays a data-center hop that later reads do not.
// Warming picks live files and reads a few kilobytes on each pooled
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

// multiDCRangeClient places each document on a data center of its own and
// records which documents were read.
type multiDCRangeClient struct {
	dcByMsg map[int64]int
	mu      sync.Mutex
	reads   map[int64]int
}

func (c *multiDCRangeClient) ResolveDocument(_ context.Context, peer tgclient.InputPeer, msgID int64) (tgclient.DocumentRef, error) {
	return tgclient.DocumentRef{Peer: peer, MsgID: msgID, DocumentID: msgID, Size: 4096, DCID: c.dcByMsg[msgID]}, nil
}

func (c *multiDCRangeClient) ResolveDocuments(ctx context.Context, peer tgclient.InputPeer, msgIDs []int64) ([]tgclient.DocumentRef, error) {
	refs := make([]tgclient.DocumentRef, 0, len(msgIDs))
	for _, msgID := range msgIDs {
		ref, _ := c.ResolveDocument(ctx, peer, msgID)
		refs = append(refs, ref)
	}
	return refs, nil
}

func (c *multiDCRangeClient) ReadDocumentRange(_ context.Context, ref tgclient.DocumentRef, _ int64, dst []byte) (int, error) {
	c.mu.Lock()
	c.reads[ref.MsgID]++
	c.mu.Unlock()
	return len(dst), nil
}

// A drive's files can live on several data centers, and only the pool for a
// file's own center makes its first read fast. Warming dials each center the
// newest files use, through one file per center.
func TestWarmTransportDialsEveryDataCenterTheNewestFilesUse(t *testing.T) {
	db := newResolverTestDB(t)
	for msgID := int64(10); msgID <= 13; msgID++ {
		mustApplyOp(t, db, msgID, projection.Op{Type: projection.OpFileUpload, Parent: projection.RootParent, Name: "clip.mp4", FileSize: 4096})
	}
	ranges := &multiDCRangeClient{dcByMsg: map[int64]int{10: 2, 11: 4, 12: 4, 13: 5}, reads: make(map[int64]int)}
	service := NewService(Config{Peers: staticPeerResolver{}, Ranges: ranges, DB: db})

	service.WarmTransport(context.Background(), testChannelID)

	// Newest first, so data center 4 is represented by message 12, not 11.
	if got := ranges.reads; len(got) != 3 || got[13] != 1 || got[12] != 1 || got[10] != 1 {
		t.Fatalf("warm reads by message = %v, want one read on one file per data center", got)
	}
}
