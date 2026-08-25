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
	if policy.MaxTransientRetries <= 0 || policy.TransientBackoff <= 0 {
		t.Fatalf("default policy has no transient budget: %+v", policy)
	}
	if policy.TransientJitter <= 0 {
		t.Fatalf("default policy has no transient jitter: %+v", policy)
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
		"transient retries without backoff": func() error {
			return (FloodWaitRetryPolicy{MaxTransientRetries: 3}).Do(context.Background(), validAction)
		},
		"negative transient jitter": func() error {
			return (FloodWaitRetryPolicy{TransientJitter: -1}).Do(context.Background(), validAction)
		},
	} {
		t.Run(name, func(t *testing.T) {
			if err := call(); !errors.Is(err, ErrInvalidFloodWaitRetryPolicy) {
				t.Fatalf("Do error = %v, want invalid policy", err)
			}
		})
	}
}

func TestTransientRetryPolicyRecoversAfterTransportFailure(t *testing.T) {
	t.Parallel()

	engineClosed := errors.New("rpcDoRequest: retryUntilAck: engine forcibly closed: context canceled")
	var attempts int
	var slept []time.Duration
	policy := FloodWaitRetryPolicy{
		MaxRetries:          2,
		MaxWait:             time.Second,
		MaxTotalWait:        2 * time.Second,
		MaxTransientRetries: 3,
		TransientBackoff:    2 * time.Second,
		MaxTransientBackoff: 30 * time.Second,
		Sleep: func(ctx context.Context, wait time.Duration) error {
			slept = append(slept, wait)
			return nil
		},
	}
	err := policy.Do(context.Background(), func() error {
		attempts++
		if attempts == 1 {
			return engineClosed
		}
		return nil
	})
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	if attempts != 2 {
		t.Fatalf("attempts = %d, want 2", attempts)
	}
	if len(slept) != 1 || slept[0] != 2*time.Second {
		t.Fatalf("sleeps = %v, want [2s]", slept)
	}
}

func TestTransientRetryPolicyBacksOffDoublingUpToCap(t *testing.T) {
	t.Parallel()

	engineClosed := errors.New("engine forcibly closed")
	var attempts int
	var slept []time.Duration
	policy := FloodWaitRetryPolicy{
		MaxTransientRetries: 4,
		TransientBackoff:    2 * time.Second,
		MaxTransientBackoff: 6 * time.Second,
		Sleep: func(ctx context.Context, wait time.Duration) error {
			slept = append(slept, wait)
			return nil
		},
	}
	err := policy.Do(context.Background(), func() error {
		attempts++
		return engineClosed
	})
	if !errors.Is(err, engineClosed) {
		t.Fatalf("Do error = %v, want original", err)
	}
	if attempts != 5 {
		t.Fatalf("attempts = %d, want 5 (1 initial + 4 retries)", attempts)
	}
	want := []time.Duration{2 * time.Second, 4 * time.Second, 6 * time.Second, 6 * time.Second}
	if len(slept) != len(want) {
		t.Fatalf("sleeps = %v, want %v", slept, want)
	}
	for i := range want {
		if slept[i] != want[i] {
			t.Fatalf("sleeps = %v, want %v", slept, want)
		}
	}
}

func TestTransientRetryPolicyKeepsConstantBackoffWithoutCap(t *testing.T) {
	t.Parallel()

	engineClosed := errors.New("engine forcibly closed")
	var attempts int
	var slept []time.Duration
	policy := FloodWaitRetryPolicy{
		MaxTransientRetries: 3,
		TransientBackoff:    2 * time.Second,
		Sleep: func(ctx context.Context, wait time.Duration) error {
			slept = append(slept, wait)
			return nil
		},
	}
	err := policy.Do(context.Background(), func() error {
		attempts++
		return engineClosed
	})
	if !errors.Is(err, engineClosed) {
		t.Fatalf("Do error = %v, want original", err)
	}
	if attempts != 4 {
		t.Fatalf("attempts = %d, want 4 (1 initial + 3 retries)", attempts)
	}
	want := []time.Duration{2 * time.Second, 2 * time.Second, 2 * time.Second}
	if len(slept) != len(want) {
		t.Fatalf("sleeps = %v, want %v", slept, want)
	}
	for i := range want {
		if slept[i] != want[i] {
			t.Fatalf("sleeps = %v, want %v", slept, want)
		}
	}
}

func TestTransientBackoffHonorsCapWithoutOverflow(t *testing.T) {
	t.Parallel()

	const maxDuration = time.Duration(1<<63 - 1)
	tests := []struct {
		name   string
		policy FloodWaitRetryPolicy
		retry  int
		want   time.Duration
	}{
		{
			name: "initial delay above cap",
			policy: FloodWaitRetryPolicy{
				TransientBackoff:    8 * time.Second,
				MaxTransientBackoff: 6 * time.Second,
			},
			want: 6 * time.Second,
		},
		{
			name: "doubling would overflow duration",
			policy: FloodWaitRetryPolicy{
				TransientBackoff:    maxDuration/2 + 1,
				MaxTransientBackoff: maxDuration,
			},
			retry: 1,
			want:  maxDuration,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := test.policy.transientBackoff(test.retry); got != test.want {
				t.Fatalf("transientBackoff(%d) = %s, want %s", test.retry, got, test.want)
			}
		})
	}
}

func TestTransientBackoffJitterStaysBounded(t *testing.T) {
	t.Parallel()

	policy := FloodWaitRetryPolicy{
		TransientBackoff:    2 * time.Second,
		MaxTransientBackoff: 2500 * time.Millisecond,
		TransientJitter:     time.Second,
	}
	for i := 0; i < 100; i++ {
		got := policy.transientBackoff(0)
		if got < 2*time.Second || got > 2500*time.Millisecond {
			t.Fatalf("jittered backoff = %s, want [2s, 2.5s]", got)
		}
	}
}

func TestSaturatingDurationAdd(t *testing.T) {
	t.Parallel()

	const maxDuration = time.Duration(1<<63 - 1)
	if got := saturatingDurationAdd(maxDuration-time.Second, 2*time.Second); got != maxDuration {
		t.Fatalf("saturating add = %s, want %s", got, maxDuration)
	}
	if got := saturatingDurationAdd(time.Second, time.Second); got != 2*time.Second {
		t.Fatalf("regular add = %s, want 2s", got)
	}
}

func TestTransientRetryPolicyCallerCancellationWinsOverEngineClose(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	attempts := 0
	err := (FloodWaitRetryPolicy{
		MaxTransientRetries: 3,
		TransientBackoff:    time.Millisecond,
	}).Do(ctx, func() error {
		attempts++
		// gotd wraps its own internal cancellation into engine-close errors;
		// once the caller's context is dead too, no further attempt may run.
		cancel()
		return errors.New("engine forcibly closed: context canceled")
	})
	if attempts != 1 || !errors.Is(err, context.Canceled) {
		t.Fatalf("attempts = %d err = %v, want single attempt ending in caller cancellation", attempts, err)
	}
}

func TestTransientRetryPolicySleepCancellationStopsRetrying(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	policy := FloodWaitRetryPolicy{
		MaxTransientRetries: 3,
		TransientBackoff:    time.Millisecond,
		Sleep: func(ctx context.Context, wait time.Duration) error {
			cancel()
			return sleepContext(ctx, wait)
		},
	}
	err := policy.Do(ctx, func() error {
		return errors.New("connection reset by peer")
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Do error = %v, want context canceled", err)
	}
}

func TestTransientAndFloodBudgetsAreIndependent(t *testing.T) {
	t.Parallel()

	var attempts int
	policy := FloodWaitRetryPolicy{
		MaxRetries:   2,
		MaxWait:      time.Second,
		MaxTotalWait: 10 * time.Second,
		Sleep:        func(context.Context, time.Duration) error { return nil },

		MaxTransientRetries: 2,
		TransientBackoff:    time.Millisecond,
	}
	err := policy.Do(context.Background(), func() error {
		attempts++
		switch attempts {
		case 1:
			return errors.New("engine forcibly closed")
		case 2:
			return NewFloodWaitError(10 * time.Millisecond)
		case 3:
			return errors.New("connection reset by peer")
		case 4:
			return NewFloodWaitError(10 * time.Millisecond)
		case 5:
			return errors.New("unexpected EOF")
		}
		t.Fatal("action called after both budgets were exhausted")
		return nil
	})
	if err == nil || err.Error() != "unexpected EOF" {
		t.Fatalf("Do error = %v, want unexpected EOF", err)
	}
	// 1 initial + 2 flood + 2 transient retries; the fifth attempt's failure
	// exhausts both budgets and must surface immediately.
	if attempts != 5 {
		t.Fatalf("attempts = %d, want 5", attempts)
	}
}
