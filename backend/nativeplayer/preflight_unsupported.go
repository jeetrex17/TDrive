//go:build !darwin && !linux

package nativeplayer

import "context"

func PreflightDecode(ctx context.Context, url string) error {
	return nil
}
