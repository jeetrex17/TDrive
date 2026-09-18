package thumbnail

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"
)

// ErrPosterUnsupported reports that this build cannot decode a frame from this
// file. It is not a failure: the caller publishes the video without a picture,
// exactly as it did before posters existed.
var ErrPosterUnsupported = fmt.Errorf("thumbnail: video poster unsupported")

// videoExtensions is the set a poster may be attempted for. It deliberately
// matches the gallery's own media predicate (backend/projection/gallery_schema.go)
// rather than each decoder's capabilities: a file the gallery will show is a
// file worth trying to draw, and a decoder that cannot read it says so at the
// point it tries.
var videoExtensions = map[string]struct{}{
	"mp4": {}, "m4v": {}, "mov": {}, "qt": {}, "webm": {}, "mkv": {}, "mk3d": {},
	"avi": {}, "ts": {}, "m2ts": {}, "mts": {}, "flv": {}, "wmv": {}, "ogv": {},
	"mpeg": {}, "mpg": {},
}

// IsVideo reports whether a name is one the gallery would list as a video.
func IsVideo(name string) bool {
	ext := strings.TrimPrefix(strings.ToLower(filepath.Ext(strings.TrimSpace(name))), ".")
	_, ok := videoExtensions[ext]
	return ok
}

// GenerateVideoPoster decodes one frame of a video and returns it as a JPEG no
// larger than maxEdge on its long side.
//
// It takes a path rather than a reader because every decoder behind it wants a
// file: mpv is a separate process, and the mobile decoders are platform APIs
// that seek by URL. Callers that hold only a stream must stage it first, which
// the upload path already does for its own reasons.
//
// The returned error is ErrPosterUnsupported when the platform or the codec
// cannot produce a frame. Callers treat that as "no picture", never as a failed
// upload -- a video that cannot be drawn is still a video worth storing.
func GenerateVideoPoster(ctx context.Context, path string, maxEdge int) ([]byte, error) {
	if ctx == nil {
		return nil, fmt.Errorf("thumbnail: context required")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if strings.TrimSpace(path) == "" || maxEdge < 1 || maxEdge > 1600 {
		return nil, fmt.Errorf("thumbnail: invalid poster request")
	}
	if !IsVideo(path) {
		return nil, ErrPosterUnsupported
	}
	// One decode at a time, for the same reason GenerateLocal takes this slot:
	// upload concurrency must never multiply simultaneous decodes. A poster is
	// a whole video decoder's working set, so this matters more here, not less.
	select {
	case localDecodeSlot <- struct{}{}:
		defer func() { <-localDecodeSlot }()
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return generateVideoPoster(ctx, path, maxEdge)
}

// posterSeekFraction is how far into a video the frame is taken from.
//
// Not the first frame: videos routinely open on black, a fade, or a title card,
// and a grid of black tiles is worse than no tiles because it looks broken
// rather than empty. A tenth of the way in is past the opening of almost
// anything while still being early enough that a decoder reaches it quickly.
const posterSeekFraction = 0.1

// posterSeekPercent is the same number in the units every decoder behind this
// actually takes: mpv's --start=N%, and the whole-percent arithmetic the mobile
// bridges do against a container's declared duration. Derived rather than
// written twice so the three implementations cannot drift apart.
const posterSeekPercent = int(posterSeekFraction * 100)

// posterFallbackOffset is where the frame comes from when a container declares
// no usable duration -- a common enough state for a fragmented MP4 still being
// written, or a stream remuxed without a header. One second in is past a fade
// from black without being past the end of the shortest clip anyone keeps, and
// a decoder that overruns the file simply reports no frame.
const posterFallbackOffset = time.Second

// posterTimeout bounds one extraction. A frame comes from a seek and a single
// decode, so a video that has not produced one by now is not going to: a
// damaged file can otherwise hold a decoder open for as long as it likes, and
// this runs while someone is waiting for an upload.
const posterTimeout = 20 * time.Second
