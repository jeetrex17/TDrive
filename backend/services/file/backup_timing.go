package file

import (
	"context"
	"time"
)

type backupTimingKey struct{}

// WithBackupUploadTiming records bounded stage names, never source identities
// or paths. The callback must be concurrency-safe and nonblocking. Transfer
// durations include transport retries/backoff; encryption includes ciphertext
// staging I/O. Concurrent stages overlap and must not be summed as wall time.
func WithBackupUploadTiming(ctx context.Context, report func(stage string, elapsed time.Duration)) context.Context {
	return context.WithValue(ctx, backupTimingKey{}, report)
}

func reportBackupTiming(ctx context.Context, stage string, started time.Time) {
	if report, ok := ctx.Value(backupTimingKey{}).(func(string, time.Duration)); ok && report != nil {
		report(stage, time.Since(started))
	}
}
