package main

import (
	"errors"
	"testing"
)

func TestUpdateCleanupDoesNotStartAfterMountStartupFailure(t *testing.T) {
	t.Parallel()

	cleanupCalls := 0
	started := scheduleUpdateCleanup(
		errors.New("mount controller unavailable"),
		func() error {
			cleanupCalls++
			return nil
		},
	)

	if started {
		t.Fatal("update cleanup was scheduled after mount startup failed")
	}
	if cleanupCalls != 0 {
		t.Fatalf("update cleanup calls = %d, want 0", cleanupCalls)
	}
}
