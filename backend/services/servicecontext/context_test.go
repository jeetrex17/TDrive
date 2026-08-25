package servicecontext

import (
	"context"
	"errors"
	"strings"
	"testing"

	"TDrive/backend/projection"
)

func TestCheck(t *testing.T) {
	tests := []struct {
		name      string
		ctx       context.Context
		wantError error
	}{
		{name: "active", ctx: context.Background()},
		{name: "nil", wantError: projection.ErrInvalidContext},
		{name: "canceled", ctx: canceledContext(), wantError: context.Canceled},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := Check(test.ctx, "file: rename")
			if !errors.Is(err, test.wantError) {
				t.Fatalf("Check() error = %v, want %v", err, test.wantError)
			}
			if test.wantError != nil && !strings.Contains(err.Error(), "file: rename") {
				t.Fatalf("Check() error = %q, want operation context", err)
			}
		})
	}
}

func canceledContext() context.Context {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	return ctx
}
