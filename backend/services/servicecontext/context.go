// Package servicecontext validates contexts at service boundaries.
package servicecontext

import (
	"context"
	"fmt"

	"TDrive/backend/projection"
)

// Check rejects nil and already-finished contexts while retaining an
// operation label and an errors.Is-compatible cause.
func Check(ctx context.Context, operation string) error {
	if ctx == nil {
		return fmt.Errorf("%s: %w", operation, projection.ErrInvalidContext)
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("%s: %w", operation, err)
	}
	return nil
}
