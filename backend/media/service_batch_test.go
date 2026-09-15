package media

import (
	"context"
	"testing"

	"TDrive/backend/projection"
	"TDrive/backend/tgclient"
)

// batchingRangeFake counts how the media service resolves segments: one
// batch call for every part, or one single call per part.
type batchingRangeFake struct {
	*mediaRangeFake
	batches int
	singles int
}

func (f *batchingRangeFake) ResolveDocument(ctx context.Context, peer tgclient.InputPeer, msgID int64) (tgclient.DocumentRef, error) {
	f.singles++
	return f.mediaRangeFake.ResolveDocument(ctx, peer, msgID)
}

func (f *batchingRangeFake) ResolveDocuments(ctx context.Context, peer tgclient.InputPeer, msgIDs []int64) ([]tgclient.DocumentRef, error) {
	f.batches++
	refs := make([]tgclient.DocumentRef, 0, len(msgIDs))
	for _, msgID := range msgIDs {
		ref, err := f.mediaRangeFake.ResolveDocument(ctx, peer, msgID)
		if err != nil {
			return nil, err
		}
		refs = append(refs, ref)
	}
	return refs, nil
}

// A multipart file used to cost one Telegram round trip per part before the
// player even had a URL. With a client that can batch, opening it is one call.
func TestMediaOpenResolvesMultipartSegmentsInOneBatch(t *testing.T) {
	db := newResolverTestDB(t)
	parts := map[int64][]byte{101: testBytes(80), 102: testBytes(90), 103: testBytes(70)}
	applyMultipart(t, db, "upload-batch", []partSpec{
		{msgID: 101, size: 80},
		{msgID: 102, size: 90},
		{msgID: 103, size: 70},
	}, 200, projection.Op{
		Type:       projection.OpFileManifest,
		UploadUUID: "upload-batch",
		Parent:     projection.RootParent,
		Name:       "movie.mkv",
		FileSize:   240,
		PartCount:  3,
	})
	ranges := &batchingRangeFake{mediaRangeFake: newMediaRangeFake(parts)}
	svc := NewService(Config{DB: db, Peers: staticPeerResolver{peer: ranges.peer}, Ranges: ranges})
	defer svc.Close()

	opened, err := svc.Open(context.Background(), testChannelID, 200)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if ranges.batches != 1 || ranges.singles != 0 {
		t.Fatalf("batches/singles = %d/%d, want one batch and no single resolves", ranges.batches, ranges.singles)
	}
	if got := opened.Info.Segments; len(got) != 3 || got[0].MsgID != 101 || got[2].MsgID != 103 {
		t.Fatalf("segments = %+v, want parts 101, 102, 103 in order", got)
	}
}
