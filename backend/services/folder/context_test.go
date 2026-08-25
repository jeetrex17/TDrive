package folder

import (
	"context"
	"errors"
	"testing"

	"TDrive/backend/projection"
)

func TestCreateContextStopsBeforeEmitWhenCanceled(t *testing.T) {
	svc, _, _, _ := newTestService(t)
	called := false
	svc.EmitOpContext = func(context.Context, int64, projection.Op) error {
		called = true
		return nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := svc.CreateContext(ctx, testChannelID, "Canceled", "")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("CreateContext error = %v, want context canceled", err)
	}
	if called {
		t.Fatal("emitter called after cancellation")
	}
}

func TestRenameContextPropagatesContextToEmitter(t *testing.T) {
	svc, _, _, _ := newTestService(t)
	folder, err := svc.Create(testChannelID, "Before", "")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	type contextKey string
	const key contextKey = "test"
	ctx := context.WithValue(context.Background(), key, "value")
	called := false
	svc.EmitOpContext = func(got context.Context, channelID int64, op projection.Op) error {
		called = true
		if got.Value(key) != "value" {
			t.Fatalf("context value was not propagated")
		}
		return svc.EmitOp(channelID, op)
	}

	if err := svc.RenameContext(ctx, testChannelID, folder.ID, "After"); err != nil {
		t.Fatalf("RenameContext: %v", err)
	}
	if !called {
		t.Fatal("context emitter was not called")
	}
}

func TestMoveContextPropagatesContextToEmitter(t *testing.T) {
	svc, _, _, _ := newTestService(t)
	parent, err := svc.Create(testChannelID, "Parent", "")
	if err != nil {
		t.Fatalf("create parent: %v", err)
	}
	child, err := svc.Create(testChannelID, "Child", "")
	if err != nil {
		t.Fatalf("create child: %v", err)
	}
	type contextKey string
	const key contextKey = "move"
	ctx := context.WithValue(context.Background(), key, "value")
	called := false
	svc.EmitOpContext = func(got context.Context, channelID int64, op projection.Op) error {
		called = true
		if got.Value(key) != "value" {
			t.Fatalf("context value was not propagated")
		}
		return svc.EmitOp(channelID, op)
	}

	if err := svc.MoveContext(ctx, testChannelID, child.ID, parent.ID); err != nil {
		t.Fatalf("MoveContext: %v", err)
	}
	if !called {
		t.Fatal("context emitter was not called")
	}
}

func TestDeletePropagatesContextToBatchEmitter(t *testing.T) {
	svc, _, _, _ := newTestService(t)
	folder, err := svc.Create(testChannelID, "Delete", "")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	type contextKey string
	const key contextKey = "delete"
	ctx := context.WithValue(context.Background(), key, "value")
	called := false
	svc.EmitOpsContext = func(got context.Context, channelID int64, ops []projection.Op) error {
		called = true
		if got.Value(key) != "value" {
			t.Fatalf("context value was not propagated")
		}
		return svc.EmitOps(channelID, ops)
	}

	if err := svc.Delete(ctx, testChannelID, folder.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if !called {
		t.Fatal("context batch emitter was not called")
	}
}

func TestContextMethodsRejectNilContext(t *testing.T) {
	svc, _, _, _ := newTestService(t)
	tests := map[string]func() error{
		"create": func() error {
			_, err := svc.CreateContext(nil, testChannelID, "No context", "")
			return err
		},
		"delete": func() error {
			return svc.Delete(nil, testChannelID, "d:missing")
		},
		"rename": func() error {
			return svc.RenameContext(nil, testChannelID, "d:missing", "Name")
		},
		"move": func() error {
			return svc.MoveContext(nil, testChannelID, "d:missing", "")
		},
	}
	for name, call := range tests {
		t.Run(name, func(t *testing.T) {
			if err := call(); !errors.Is(err, projection.ErrInvalidContext) {
				t.Fatalf("error = %v, want projection.ErrInvalidContext", err)
			}
		})
	}
}
