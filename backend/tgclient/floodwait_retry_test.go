package tgclient

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestFloodWaitRetryPolicyRetriesWithinBudget(t *testing.T) {
	t.Parallel()

	attempts := 0
	var slept []time.Duration
	policy := FloodWaitRetryPolicy{
		MaxRetries:   2,
		MaxWait:      5 * time.Second,
		MaxTotalWait: 8 * time.Second,
		Sleep: func(ctx context.Context, wait time.Duration) error {
			slept = append(slept, wait)
			return nil
		},
	}
	err := policy.Do(context.Background(), func() error {
		attempts++
		if attempts < 3 {
			return NewFloodWaitError(2 * time.Second)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	if attempts != 3 {
		t.Fatalf("attempts = %d, want 3", attempts)
	}
	if len(slept) != 2 || slept[0] != 2*time.Second || slept[1] != 2*time.Second {
		t.Fatalf("sleeps = %v, want [2s 2s]", slept)
	}
}

func TestFloodWaitRetryPolicyRejectsWaitBeyondBudget(t *testing.T) {
	t.Parallel()

	attempts := 0
	policy := FloodWaitRetryPolicy{
		MaxRetries:   5,
		MaxWait:      time.Second,
		MaxTotalWait: 5 * time.Second,
		Sleep: func(context.Context, time.Duration) error {
			t.Fatal("Sleep called for an over-budget wait")
			return nil
		},
	}
	want := NewFloodWaitError(2 * time.Second)
	err := policy.Do(context.Background(), func() error {
		attempts++
		return want
	})
	if !errors.Is(err, ErrFloodWait) {
		t.Fatalf("Do error = %v, want flood wait", err)
	}
	if attempts != 1 {
		t.Fatalf("attempts = %d, want 1", attempts)
	}
}

func TestFloodWaitRetryPolicyHonorsCancellationWhileSleeping(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	policy := FloodWaitRetryPolicy{
		MaxRetries:   1,
		MaxWait:      time.Second,
		MaxTotalWait: time.Second,
		Sleep: func(ctx context.Context, wait time.Duration) error {
			cancel()
			return sleepContext(ctx, wait)
		},
	}
	err := policy.Do(ctx, func() error {
		return NewFloodWaitError(time.Second)
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Do error = %v, want context canceled", err)
	}
}

func TestDefaultWriteFloodWaitRetryPolicyIsBounded(t *testing.T) {
	t.Parallel()

	policy := DefaultWriteFloodWaitRetryPolicy()
	if policy.MaxRetries <= 0 || policy.MaxWait <= 0 || policy.MaxTotalWait <= 0 {
		t.Fatalf("default policy is unbounded: %+v", policy)
	}
}

func TestFloodWaitRetryPolicyReturnsNonFloodErrorWithoutRetry(t *testing.T) {
	t.Parallel()

	want := errors.New("network failed")
	attempts := 0
	err := (FloodWaitRetryPolicy{MaxRetries: 3, MaxWait: time.Second, MaxTotalWait: 3 * time.Second}).Do(context.Background(), func() error {
		attempts++
		return want
	})
	if !errors.Is(err, want) || attempts != 1 {
		t.Fatalf("Do = %v after %d attempts, want original after one", err, attempts)
	}
}

func TestFloodWaitRetryPolicyEnforcesTotalBudget(t *testing.T) {
	t.Parallel()

	attempts := 0
	sleeps := 0
	policy := FloodWaitRetryPolicy{
		MaxRetries:   3,
		MaxWait:      3 * time.Second,
		MaxTotalWait: 3 * time.Second,
		Sleep: func(context.Context, time.Duration) error {
			sleeps++
			return nil
		},
	}
	err := policy.Do(context.Background(), func() error {
		attempts++
		return NewFloodWaitError(2 * time.Second)
	})
	if !errors.Is(err, ErrFloodWait) {
		t.Fatalf("Do error = %v, want flood wait", err)
	}
	if attempts != 2 || sleeps != 1 {
		t.Fatalf("attempts=%d sleeps=%d, want 2 and 1", attempts, sleeps)
	}
}

func TestFloodWaitRetryPolicyChecksContextBeforeAction(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := (FloodWaitRetryPolicy{}).Do(ctx, func() error {
		t.Fatal("action called after cancellation")
		return nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Do error = %v, want context canceled", err)
	}
}

func TestSleepContextCompletesAndHandlesZeroWait(t *testing.T) {
	t.Parallel()

	if err := sleepContext(context.Background(), 0); err != nil {
		t.Fatalf("zero sleep: %v", err)
	}
	if err := sleepContext(context.Background(), time.Millisecond); err != nil {
		t.Fatalf("positive sleep: %v", err)
	}
}

func TestFloodWaitRetryPolicyRejectsUnboundedOrMissingInputs(t *testing.T) {
	t.Parallel()

	validAction := func() error { return nil }
	for name, call := range map[string]func() error{
		"nil context": func() error {
			return (FloodWaitRetryPolicy{}).Do(nil, validAction)
		},
		"nil action": func() error {
			return (FloodWaitRetryPolicy{}).Do(context.Background(), nil)
		},
		"negative retries": func() error {
			return (FloodWaitRetryPolicy{MaxRetries: -1}).Do(context.Background(), validAction)
		},
		"unbounded wait": func() error {
			return (FloodWaitRetryPolicy{MaxRetries: 1}).Do(context.Background(), validAction)
		},
	} {
		t.Run(name, func(t *testing.T) {
			if err := call(); !errors.Is(err, ErrInvalidFloodWaitRetryPolicy) {
				t.Fatalf("Do error = %v, want invalid policy", err)
			}
		})
	}
}
