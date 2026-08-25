package tgclient

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"
)

const (
	defaultWriteFloodWaitRetries = 2
	defaultWriteFloodWaitMax     = 30 * time.Second
	defaultWriteFloodWaitTotal   = 60 * time.Second
)

var ErrInvalidFloodWaitRetryPolicy = errors.New("tgclient: invalid flood wait retry policy")

// FloodWaitRetryPolicy retries only Telegram FLOOD_WAIT failures. MaxRetries
// counts retries after the initial call. A zero-value policy performs one call
// and no retries; callers that want the bounded production defaults should use
// DefaultWriteFloodWaitRetryPolicy.
type FloodWaitRetryPolicy struct {
	MaxRetries   int
	MaxWait      time.Duration
	MaxTotalWait time.Duration
	Sleep        func(context.Context, time.Duration) error
}

func DefaultWriteFloodWaitRetryPolicy() FloodWaitRetryPolicy {
	return FloodWaitRetryPolicy{
		MaxRetries:   defaultWriteFloodWaitRetries,
		MaxWait:      defaultWriteFloodWaitMax,
		MaxTotalWait: defaultWriteFloodWaitTotal,
	}
}

func (p FloodWaitRetryPolicy) Do(ctx context.Context, action func() error) error {
	if ctx == nil {
		return fmt.Errorf("%w: context is required", ErrInvalidFloodWaitRetryPolicy)
	}
	if action == nil {
		return fmt.Errorf("%w: action is required", ErrInvalidFloodWaitRetryPolicy)
	}
	if p.MaxRetries < 0 || p.MaxWait < 0 || p.MaxTotalWait < 0 {
		return fmt.Errorf("%w: limits must be non-negative", ErrInvalidFloodWaitRetryPolicy)
	}
	if p.MaxRetries > 0 && (p.MaxWait == 0 || p.MaxTotalWait == 0) {
		return fmt.Errorf("%w: retrying requires per-wait and total-wait limits", ErrInvalidFloodWaitRetryPolicy)
	}

	sleep := p.Sleep
	if sleep == nil {
		sleep = sleepContext
	}
	var totalWait time.Duration
	for retries := 0; ; retries++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		err := action()
		if err == nil {
			return nil
		}
		wait, floodWait := FloodWaitDuration(err)
		if !floodWait || retries >= p.MaxRetries {
			if floodWait {
				slog.Warn("tgclient: FLOOD_WAIT retry budget exhausted, giving up", "retries", retries, "wait", wait, "max_retries", p.MaxRetries)
			}
			return err
		}
		if wait < 0 || (p.MaxWait > 0 && wait > p.MaxWait) {
			slog.Warn("tgclient: FLOOD_WAIT exceeds per-wait limit, giving up", "wait", wait, "max_wait", p.MaxWait)
			return err
		}
		if p.MaxTotalWait > 0 && (wait > p.MaxTotalWait-totalWait) {
			slog.Warn("tgclient: FLOOD_WAIT would exceed total-wait budget, giving up", "wait", wait, "total_wait_so_far", totalWait, "max_total_wait", p.MaxTotalWait)
			return err
		}
		slog.Warn("tgclient: FLOOD_WAIT, sleeping before retry", "attempt", retries+1, "wait", wait)
		if err := sleep(ctx, wait); err != nil {
			return err
		}
		totalWait += wait
	}
}

func sleepContext(ctx context.Context, wait time.Duration) error {
	if wait <= 0 {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
			return nil
		}
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
