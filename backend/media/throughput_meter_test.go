package media

import (
	"context"
	"testing"
	"time"

	"TDrive/backend/tgclient"
)

type meterClock struct {
	now time.Time
}

func newMeterClock() *meterClock {
	return &meterClock{now: time.Unix(1_700_000_000, 0)}
}

func (c *meterClock) time() time.Time {
	return c.now
}

func (c *meterClock) advance(d time.Duration) {
	c.now = c.now.Add(d)
}

func TestThroughputMeterWarmWindowAndExpiry(t *testing.T) {
	clock := newMeterClock()
	meter := newThroughputMeterWithClock(4*time.Second, 200*time.Millisecond, clock.time)

	meter.Add(1_000)
	if got := meter.Stats().BytesPerSecond; got != 0 {
		t.Fatalf("rate after one sample = %d, want 0", got)
	}

	clock.advance(time.Second)
	meter.Add(1_000)
	if got := meter.Stats().BytesPerSecond; got != 2_000 {
		t.Fatalf("warm rate = %d, want 2000", got)
	}

	clock.advance(5 * time.Second)
	if got := meter.Stats().BytesPerSecond; got != 0 {
		t.Fatalf("expired rate = %d, want 0", got)
	}
}

func TestThroughputMeterSustainedStreamingDoesNotOverReport(t *testing.T) {
	clock := newMeterClock()
	meter := newThroughputMeterWithClock(4*time.Second, 200*time.Millisecond, clock.time)

	const trueRate = int64(1_000)
	for second := 0; second <= 10; second++ {
		meter.Add(int(trueRate))
		if second >= 5 {
			got := meter.Stats().BytesPerSecond
			if got < 900 || got > 1_250 {
				t.Fatalf("rate at second %d = %d, want close to %d", second, got, trueRate)
			}
		}
		clock.advance(time.Second)
	}
}

func TestThroughputMeterFloodWaitRecentDecay(t *testing.T) {
	clock := newMeterClock()
	meter := newThroughputMeterWithClock(4*time.Second, 200*time.Millisecond, clock.time)

	meter.NoteFloodWait(3 * time.Second)
	stats := meter.Stats()
	if !stats.RecentFloodWait {
		t.Fatal("RecentFloodWait = false, want true")
	}
	if stats.LastFloodWaitSeconds != 3 {
		t.Fatalf("LastFloodWaitSeconds = %.1f, want 3", stats.LastFloodWaitSeconds)
	}

	clock.advance(throughputFloodWaitRecent + time.Millisecond)
	if stats := meter.Stats(); stats.RecentFloodWait {
		t.Fatal("RecentFloodWait still true after decay window")
	}
}

func TestSessionStatsKeepPlaybackAndThumbnailMetersIndependent(t *testing.T) {
	clock := newMeterClock()
	playback := newThroughputMeterWithClock(4*time.Second, 200*time.Millisecond, clock.time)
	thumbs := newThroughputMeterWithClock(4*time.Second, 200*time.Millisecond, clock.time)
	session := &Session{
		reader:      &RangeReader{meter: playback},
		thumbReader: &RangeReader{meter: thumbs},
	}

	playback.Add(1_000)
	thumbs.Add(4_000)
	clock.advance(time.Second)
	playback.Add(1_000)
	thumbs.Add(4_000)

	stats := session.Stats()
	if stats.Playback.BytesPerSecond != 2_000 {
		t.Fatalf("playback rate = %d, want 2000", stats.Playback.BytesPerSecond)
	}
	if stats.Thumbnails.BytesPerSecond != 8_000 {
		t.Fatalf("thumbnail rate = %d, want 8000", stats.Thumbnails.BytesPerSecond)
	}
}

func TestServerStatsUnknownTokenIsZero(t *testing.T) {
	stats := NewServer(nil).Stats("missing")
	if stats.Playback.BytesPerSecond != 0 || stats.Thumbnails.BytesPerSecond != 0 {
		t.Fatalf("stats = %+v, want zero", stats)
	}
	if stats.Playback.RecentFloodWait || stats.Thumbnails.RecentFloodWait {
		t.Fatalf("stats = %+v, want no flood wait", stats)
	}
}

func TestRangeReaderThroughputCountsNetworkFetchesOnly(t *testing.T) {
	data := testBytes(tgclient.RangeReadMaxBytes)
	fake := newStrictRangeFake(data)
	reader := NewRangeReader(RangeReaderConfig{Client: fake})
	defer reader.Close()
	ref := fake.ref()

	buf := make([]byte, 64)
	if _, err := reader.ReadStoredAt(context.Background(), ref, buf, 100); err != nil {
		t.Fatalf("first read: %v", err)
	}
	if _, err := reader.ReadStoredAt(context.Background(), ref, buf, 128); err != nil {
		t.Fatalf("cached read: %v", err)
	}

	if calls := fake.calls(); len(calls) != 1 {
		t.Fatalf("network calls = %+v, want one block fetch", calls)
	}
	if got := throughputMeterBytes(reader.meter); got != int64(tgclient.RangeReadMaxBytes) {
		t.Fatalf("meter bytes = %d, want one fetched block", got)
	}
}

func throughputMeterBytes(meter *throughputMeter) int64 {
	meter.mu.Lock()
	defer meter.mu.Unlock()
	var total int64
	for _, bucket := range meter.buckets {
		total += bucket.bytes
	}
	return total
}
