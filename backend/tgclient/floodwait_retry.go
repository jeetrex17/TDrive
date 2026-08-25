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

	// Moderate transient-transport budget: a dropped shared connection should
	// recover transparently (#74/#73) without masking real outages.
	defaultTransientRetries    = 4
	defaultTransientBackoff    = 2 * time.Second
	defaultTransientMaxBackoff = 30 * time.Second
)

var ErrInvalidFloodWaitRetryPolicy = errors.New("tgclient: invalid flood wait retry policy")

// FloodWaitRetryPolicy bounds retries for long Telegram transfers. It covers
// two failure classes:
//
//   - FLOOD_WAIT: Telegram rate limiting, retried after the server-requested
//     wait under MaxRetries/MaxWait/MaxTotalWait budgets.
//   - Transient transport failures (IsTransientTransport): the shared
//     connection scope died or the TCP link was reset mid-transfer, which
//     fails every in-flight RPC with "engine forcibly closed" style errors.
//     These are retried after a capped doubling backoff that gives the
//     liveConn scope time to re-establish before the next attempt.
//
// MaxRetries counts FLOOD_WAIT retries after the initial call and
// MaxTransientRetries counts transport retries; both are independent budgets.
// A zero-value policy performs one call and no retries; callers that want the
// bounded production defaults should use DefaultWriteFloodWaitRetryPolicy.
type FloodWaitRetryPolicy struct {
	MaxRetries   int
	MaxWait      time.Duration
	MaxTotalWait time.Duration
	Sleep        func(context.Context, time.Duration) error

	// MaxTransientRetries bounds transport-failure retries after the initial
	// attempt. Zero disables them.
	MaxTransientRetries int
	// TransientBackoff is the first backoff; it doubles each attempt up to
	// MaxTransientBackoff. Required when MaxTransientRetries > 0.
	TransientBackoff time.Duration
	// MaxTransientBackoff caps the doubling backoff. When zero, backoff stays
	// constant at TransientBackoff.
	MaxTransientBackoff time.Duration
}

func DefaultWriteFloodWaitRetryPolicy() FloodWaitRetryPolicy {
	return FloodWaitRetryPolicy{
		MaxRetries:          defaultWriteFloodWaitRetries,
		MaxWait:             defaultWriteFloodWaitMax,
		MaxTotalWait:        defaultWriteFloodWaitTotal,
		MaxTransientRetries: defaultTransientRetries,
		TransientBackoff:    defaultTransientBackoff,
		MaxTransientBackoff: defaultTransientMaxBackoff,
	}
}

// Validate reports whether p has bounded, non-negative retry settings.
func (p FloodWaitRetryPolicy) Validate() error {
	if p.MaxRetries < 0 || p.MaxWait < 0 || p.MaxTotalWait < 0 ||
		p.MaxTransientRetries < 0 || p.TransientBackoff < 0 || p.MaxTransientBackoff < 0 {
		return fmt.Errorf("%w: limits must be non-negative", ErrInvalidFloodWaitRetryPolicy)
	}
	if p.MaxRetries > 0 && (p.MaxWait == 0 || p.MaxTotalWait == 0) {
		return fmt.Errorf("%w: retrying requires per-wait and total-wait limits", ErrInvalidFloodWaitRetryPolicy)
	}
	if p.MaxTransientRetries > 0 && p.TransientBackoff == 0 {
		return fmt.Errorf("%w: retrying transient failures requires a backoff", ErrInvalidFloodWaitRetryPolicy)
	}
	return nil
}

func (p FloodWaitRetryPolicy) Do(ctx context.Context, action func() error) error {
	if ctx == nil {
		return fmt.Errorf("%w: context is required", ErrInvalidFloodWaitRetryPolicy)
	}
	if action == nil {
		return fmt.Errorf("%w: action is required", ErrInvalidFloodWaitRetryPolicy)
	}
	if err := p.Validate(); err != nil {
		return err
	}

	sleep := p.Sleep
	if sleep == nil {
		sleep = sleepContext
	}
	var totalWait time.Duration
	var floodRetries, transientRetries int
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		err := action()
		if err == nil {
			return nil
		}
		wait, floodWait := FloodWaitDuration(err)
		if floodWait {
			if floodRetries >= p.MaxRetries || wait < 0 ||
				(p.MaxWait > 0 && wait > p.MaxWait) ||
				(p.MaxTotalWait > 0 && wait > p.MaxTotalWait-totalWait) {
				slog.Warn("tgclient: FLOOD_WAIT retry budget exhausted, giving up", "retries", floodRetries, "wait", wait, "max_retries", p.MaxRetries)
				return err
			}
			slog.Warn("tgclient: FLOOD_WAIT, sleeping before retry", "attempt", floodRetries+1, "wait", wait)
			if err := sleep(ctx, wait); err != nil {
				return err
			}
			totalWait += wait
			floodRetries++
			continue
		}
		if !IsTransientTransport(err) || transientRetries >= p.MaxTransientRetries {
			return err
		}
		backoff := p.transientBackoff(transientRetries)
		slog.Warn(
			"tgclient: transient transport failure, reconnecting before retry",
			"attempt", transientRetries+1,
			"max_retries", p.MaxTransientRetries,
			"backoff", backoff,
			"error", err,
		)
		if err := sleep(ctx, backoff); err != nil {
			return err
		}
		transientRetries++
	}
}

// transientBackoff returns the wait before the given zero-based transport
// retry: TransientBackoff doubled per attempt, capped at MaxTransientBackoff.
func (p FloodWaitRetryPolicy) transientBackoff(retry int) time.Duration {
	backoff := p.TransientBackoff
	maxBackoff := p.MaxTransientBackoff
	if maxBackoff == 0 {
		return backoff
	}
	if backoff >= maxBackoff {
		return maxBackoff
	}
	for i := 0; i < retry; i++ {
		if backoff > maxBackoff-backoff {
			return maxBackoff
		}
		backoff *= 2
	}
	return backoff
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
