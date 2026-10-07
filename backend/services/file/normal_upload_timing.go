package file

import (
	"log/slog"
	"sync"
	"time"
)

// Logarithmic buckets bound memory independently of batch size. Quantiles are
// upper bounds; overlapping file stages are not additive wall-clock time.
type uploadBatchTimings struct {
	mu     sync.Mutex
	stages [7]uploadStageTiming
}

var uploadStageNames = [...]string{
	"slot_wait", "source_prepare", "encrypt", "transfer",
	"manifest_commit", "receipt_wait", "projection",
}

type uploadStageTiming struct {
	count   int64
	total   time.Duration
	max     time.Duration
	buckets [24]int64
}

func (t *uploadBatchTimings) observe(stage string, duration time.Duration) {
	for i, name := range uploadStageNames {
		if stage != name {
			continue
		}
		t.mu.Lock()
		p := &t.stages[i]
		p.count++
		p.total += duration
		p.max = max(p.max, duration)
		bucket, limit := 0, time.Millisecond
		for duration > limit && bucket < len(p.buckets)-1 {
			bucket++
			limit *= 2
		}
		p.buckets[bucket]++
		t.mu.Unlock()
		return
	}
}

func (p uploadStageTiming) quantileUpper(percent int64) time.Duration {
	target := (p.count*percent + 99) / 100
	var count int64
	for i, n := range p.buckets {
		count += n
		if count >= target {
			if i == len(p.buckets)-1 {
				return max(time.Millisecond<<i, p.max)
			}
			return time.Millisecond << i
		}
	}
	return 0
}

func (t *uploadBatchTimings) log(elapsed time.Duration, files int) {
	t.mu.Lock()
	stages := t.stages
	t.mu.Unlock()
	slog.Debug("file: upload batch timing", "files", files, "elapsed_ms", elapsed.Milliseconds())
	for i, name := range uploadStageNames {
		p := stages[i]
		if p.count == 0 {
			continue
		}
		slog.Debug("file: upload stage timing", "stage", name, "samples", p.count,
			"total_ms", p.total.Milliseconds(), "max_ms", p.max.Milliseconds(),
			"p50_upper_ms", p.quantileUpper(50).Milliseconds(),
			"p95_upper_ms", p.quantileUpper(95).Milliseconds())
	}
}
