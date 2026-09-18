//go:build !darwin && !linux && !windows

package thumbnail

import "context"

// generateVideoPoster is the answer on a platform with no decoder wired up yet.
//
// It is a real answer, not a gap: the caller's contract is that a poster is
// optional, so a video here is published exactly as it was before posters
// existed. Adding a platform means adding its file, not changing any caller.
func generateVideoPoster(_ context.Context, _ string, _ int) ([]byte, error) {
	return nil, ErrPosterUnsupported
}
