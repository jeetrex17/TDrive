package file

import (
	"context"
	"errors"
	"strings"
	"testing"

	"TDrive/backend/projection"
)

func TestMutationMethodsRejectNilContext(t *testing.T) {
	svc, _, _, _ := newTestService(t)
	tests := map[string]func() error{
		"metadata": func() error {
			return svc.MetaContext(nil, personalChannelID, 1, "file.txt", 1, "")
		},
		"rename": func() error {
			return svc.Rename(nil, personalChannelID, 1, "renamed.txt")
		},
		"move": func() error {
			return svc.Move(nil, personalChannelID, 1, "")
		},
		"delete": func() error {
			return svc.Delete(nil, personalChannelID, 1)
		},
	}
	for operation, call := range tests {
		t.Run(operation, func(t *testing.T) {
			err := call()
			if !errors.Is(err, projection.ErrInvalidContext) {
				t.Fatalf("error = %v, want projection.ErrInvalidContext", err)
			}
			if !strings.Contains(err.Error(), operation) {
				t.Fatalf("error = %q, want %q operation context", err, operation)
			}
		})
	}
}

func TestRenameStopsBeforeEmitWhenContextCanceled(t *testing.T) {
	svc, db, _, _ := newTestService(t)
	project(t, db, personalChannelID, 44, 7, projection.Op{
		Type:           projection.OpFileUpload,
		Name:           "before.txt",
		FileSize:       1,
		FileUploadTime: 1,
	})
	called := false
	svc.EmitOpContext = func(context.Context, int64, projection.Op) (int64, error) {
		called = true
		return 0, nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := svc.Rename(ctx, personalChannelID, 44, "after.txt")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Rename error = %v, want context canceled", err)
	}
	if called {
		t.Fatal("emitter called after cancellation")
	}
}

func TestMetaContextPropagatesContextToEmitter(t *testing.T) {
	svc, _, _, _ := newTestService(t)
	type contextKey string
	const key contextKey = "mutation"
	ctx := context.WithValue(context.Background(), key, "mounted")
	called := false
	svc.EmitOpContext = func(got context.Context, channelID int64, op projection.Op) (int64, error) {
		called = true
		if got.Value(key) != "mounted" {
			t.Fatalf("context value was not propagated")
		}
		return svc.EmitOp(channelID, op)
	}

	if err := svc.MetaContext(ctx, personalChannelID, 55, "mounted.txt", 8, ""); err != nil {
		t.Fatalf("MetaContext: %v", err)
	}
	if !called {
		t.Fatal("context emitter was not called")
	}
}
