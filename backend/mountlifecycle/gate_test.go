package mountlifecycle

import (
	"context"
	"errors"
	"testing"
)

func TestGateSerializesAndHonorsCancellation(t *testing.T) {
	var gate Gate
	if err := gate.Lock(context.Background()); err != nil {
		t.Fatalf("Lock() error = %v", err)
	}
	if gate.TryLock() {
		gate.Unlock()
		t.Fatal("TryLock() acquired an already-held gate")
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := gate.Lock(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("Lock(canceled) error = %v, want context canceled", err)
	}

	gate.Unlock()
	if !gate.TryLock() {
		t.Fatal("canceled acquisition leaked the gate token")
	}
	gate.Unlock()
}

func TestGateRejectsNilContext(t *testing.T) {
	var gate Gate
	if err := gate.Lock(nil); !errors.Is(err, ErrContextRequired) {
		t.Fatalf("Lock(nil) error = %v, want ErrContextRequired", err)
	}
	if !gate.TryLock() {
		t.Fatal("nil-context acquisition changed the gate state")
	}
	gate.Unlock()
}
