package media

import (
	"bytes"
	"context"
	"testing"

	"TDrive/backend/tgclient"
)

// Telegram expires file references mid-session. The first rejected block pays
// one resolve; every block after it must use the fresh reference straight
// away rather than paying a rejection and a resolve of its own.
func TestRangeReaderKeepsRefreshedFileReference(t *testing.T) {
	block := int64(tgclient.RangeReadMaxBytes)
	data := testBytes(int(block) * 3)
	fake := newStrictRangeFake(data)
	reader := NewRangeReader(RangeReaderConfig{Client: fake})
	defer reader.Close()

	stale := fake.ref()
	fake.expireReference()

	for _, off := range []int64{block, 2 * block} {
		buf := make([]byte, 64)
		if _, err := reader.ReadStoredAt(context.Background(), stale, buf, off); err != nil {
			t.Fatalf("read at %d: %v", off, err)
		}
		if !bytes.Equal(buf, data[off:off+64]) {
			t.Fatalf("bytes at %d mismatch", off)
		}
	}
	if calls := fake.calls(); len(calls) != 3 {
		t.Fatalf("calls = %+v, want a rejected read, its retry, and one clean read", calls)
	}
	if resolves := fake.resolveCount(); resolves != 1 {
		t.Fatalf("resolves = %d, want the reference refreshed once", resolves)
	}
}
