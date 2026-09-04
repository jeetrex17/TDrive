package media

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"TDrive/backend/projection"
	"TDrive/backend/thumbnail"
)

func TestPublicThumbnailResponsesRemainCacheable(t *testing.T) {
	db := newResolverTestDB(t)
	body := testBytes(512)
	mustApplyOp(t, db, 10, projection.Op{
		Type:     projection.OpFileUpload,
		Parent:   projection.RootParent,
		Name:     "public.mp4",
		FileSize: int64(len(body)),
	})
	ranges := newMediaRangeFake(map[int64][]byte{10: body})
	thumbGen := &fakeVideoThumbGenerator{available: true}
	svc := NewService(Config{
		DB:             db,
		Peers:          staticPeerResolver{peer: ranges.peer},
		Ranges:         ranges,
		Thumbs:         thumbnail.NewCache(t.TempDir(), 1<<20),
		ThumbGenerator: thumbGen,
	})
	defer svc.Close()

	opened, err := svc.Open(context.Background(), testChannelID, 10)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	thumbURL := opened.ThumbnailURL + "?t=0"
	_ = waitForThumbnail(t, thumbURL)
	resp, err := http.Get(thumbURL)
	if err != nil {
		t.Fatalf("thumbnail GET: %v", err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("thumbnail status = %d, want 200", resp.StatusCode)
	}
	if got := resp.Header.Get("Cache-Control"); !strings.Contains(got, "private") || !strings.Contains(got, "max-age=86400") {
		t.Fatalf("Cache-Control = %q, want private max-age cache header", got)
	}
}
