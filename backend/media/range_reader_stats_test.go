package media

import (
	"context"
	"testing"
	"time"

	"TDrive/backend/tgclient"
)

// The close-time summary attributes a slow stream from the log, so the counts
// behind it have to be right: one fetch, one cache hit, a measured latency.
func TestRangeReaderStatsCountFetchesAndCacheHits(t *testing.T) {
	data := testBytes(tgclient.RangeReadMaxBytes * 2)
	fake := newStrictRangeFake(data)
	fake.delay = 2 * time.Millisecond
	reader := NewRangeReader(RangeReaderConfig{Client: fake})
	defer reader.Close()
	ref := fake.ref()

	for range 2 {
		if _, err := reader.ReadStoredAt(context.Background(), ref, make([]byte, 64), openingChunkBytes); err != nil {
			t.Fatalf("ReadStoredAt: %v", err)
		}
	}

	stats := reader.Stats()
	if stats.Fetched != 1 || stats.CacheHits != 1 || stats.Cancelled != 0 || stats.PeakInFlight != 1 {
		t.Fatalf("stats = %+v, want one fetch, one hit, nothing cancelled, pipeline depth one", stats)
	}
	if stats.BlockP50 < fake.delay || stats.BlockP95 < stats.BlockP50 {
		t.Fatalf("latency percentiles = %v/%v, want at least the fake's %v delay", stats.BlockP50, stats.BlockP95, fake.delay)
	}
}
