package mountos

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestWaitForPollHonorsContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if err := waitForPoll(ctx, time.Hour); !errors.Is(err, context.Canceled) {
		t.Fatalf("waitForPoll error = %v, want context.Canceled", err)
	}
}

func TestWaitForPollCompletesAfterDelay(t *testing.T) {
	if err := waitForPoll(context.Background(), 0); err != nil {
		t.Fatalf("waitForPoll error = %v, want nil", err)
	}
}
