//go:build !windows && !(darwin && !ios) && !(linux && !android) && !(android && cgo) && !(ios && cgo)

package thumbnail

import "context"

// generateVideoPoster is the answer where no decoder is reachable: a host with
// no platform framework at all, or a mobile build compiled without cgo, which
// is how the tooling cross-compiles for a tag check.
//
// It is a real answer, not a gap: the caller's contract is that a poster is
// optional, so a video here is published exactly as it was before posters
// existed. Adding a platform means adding its file, not changing any caller.
func generateVideoPoster(_ context.Context, _ string, _ int) ([]byte, error) {
	return nil, ErrPosterUnsupported
}
