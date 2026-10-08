package file

import (
	"context"
	"time"
)

type uploadTimingKey struct{}

// WithUploadTiming observes upload stages without retaining source identities.
// The callback must be concurrency-safe and nonblocking. Stages from different
// files can overlap, so their durations must not be added as wall time.
func WithUploadTiming(ctx context.Context, report func(stage string, elapsed time.Duration)) context.Context {
	return context.WithValue(ctx, uploadTimingKey{}, report)
}

// WithBackupUploadTiming records bounded stage names, never source identities
// or paths. The callback must be concurrency-safe and nonblocking. Transfer
// durations include transport retries/backoff; encryption includes ciphertext
// staging I/O. Concurrent stages overlap and must not be summed as wall time.
func WithBackupUploadTiming(ctx context.Context, report func(stage string, elapsed time.Duration)) context.Context {
	return WithUploadTiming(ctx, report)
}

func reportBackupTiming(ctx context.Context, stage string, started time.Time) {
	reportUploadTiming(ctx, stage, started)
}

func uploadTimingReporter(ctx context.Context) func(string, time.Duration) {
	report, _ := ctx.Value(uploadTimingKey{}).(func(string, time.Duration))
	return report
}

func reportUploadTiming(ctx context.Context, stage string, started time.Time) {
	if report := uploadTimingReporter(ctx); report != nil {
		report(stage, time.Since(started))
	}
}
