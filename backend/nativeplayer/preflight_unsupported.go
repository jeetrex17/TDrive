//go:build !darwin && !linux && !windows

package nativeplayer

import "context"

func PreflightDecode(ctx context.Context, url string) error {
	return nil
}
