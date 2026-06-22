package media

import (
	"sync"
	"time"
)

const (
	throughputWindow          = 4 * time.Second
	throughputBucketDuration  = 200 * time.Millisecond
	throughputFloodWaitRecent = 10 * time.Second
)

type ThroughputStats struct {
	BytesPerSecond       int64   `json:"bytes_per_second"`
	RecentFloodWait      bool    `json:"recent_flood_wait"`
	LastFloodWaitSeconds float64 `json:"last_flood_wait_seconds"`
}

type throughputBucket struct {
	epoch   int64
	bytes   int64
	samples int64
}

type throughputMeter struct {
	mu      sync.Mutex
	now     func() time.Time
	window  time.Duration
	bucket  time.Duration
	buckets []throughputBucket
	first   time.Time
	lastFW  time.Time
	lastFWD time.Duration
}

func newThroughputMeter() *throughputMeter {
	return newThroughputMeterWithClock(throughputWindow, throughputBucketDuration, time.Now)
}

func newThroughputMeterWithClock(window, bucket time.Duration, now func() time.Time) *throughputMeter {
	if window <= 0 {
		window = throughputWindow
	}
	if bucket <= 0 {
		bucket = throughputBucketDuration
	}
	count := int(window / bucket)
	if count < 1 {
		count = 1
	}
	if now == nil {
		now = time.Now
	}
	return &throughputMeter{
		now:     now,
		window:  window,
		bucket:  bucket,
		buckets: make([]throughputBucket, count),
	}
}

func (m *throughputMeter) Add(n int) {
	if m == nil || n <= 0 {
		return
	}
	now := m.now()
	epoch := now.UnixNano() / int64(m.bucket)
	idx := int(epoch % int64(len(m.buckets)))

	m.mu.Lock()
	defer m.mu.Unlock()
	if m.buckets[idx].epoch != epoch {
		m.buckets[idx] = throughputBucket{epoch: epoch}
	}
	// Keep the origin stable. Stats caps elapsed to the rolling window; resetting
	// first during sustained reads would divide a full window by a tiny elapsed
	// interval and wildly over-report throughput.
	if m.first.IsZero() {
		m.first = now
	}
	m.buckets[idx].bytes += int64(n)
	m.buckets[idx].samples++
}

func (m *throughputMeter) NoteFloodWait(d time.Duration) {
	if m == nil {
		return
	}
	m.mu.Lock()
	m.lastFW = m.now()
	m.lastFWD = d
	m.mu.Unlock()
}

func (m *throughputMeter) Stats() ThroughputStats {
	if m == nil {
		return ThroughputStats{}
	}
	now := m.now()
	currentEpoch := now.UnixNano() / int64(m.bucket)
	windowBuckets := int64(len(m.buckets))

	var bytes int64
	var samples int64
	m.mu.Lock()
	for _, bucket := range m.buckets {
		if bucket.bytes <= 0 || currentEpoch-bucket.epoch >= windowBuckets {
			continue
		}
		bytes += bucket.bytes
		samples += bucket.samples
	}
	elapsed := now.Sub(m.first)
	lastFW := m.lastFW
	lastFWD := m.lastFWD
	m.mu.Unlock()

	var bps int64
	if bytes > 0 && samples >= 2 && elapsed > 0 {
		if elapsed > m.window {
			elapsed = m.window
		}
		bps = int64(float64(bytes) / elapsed.Seconds())
	}
	return ThroughputStats{
		BytesPerSecond:       bps,
		RecentFloodWait:      !lastFW.IsZero() && now.Sub(lastFW) <= throughputFloodWaitRecent,
		LastFloodWaitSeconds: lastFWD.Seconds(),
	}
}
