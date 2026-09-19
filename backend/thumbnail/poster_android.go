//go:build android && cgo

package thumbnail

/*
#include <stdlib.h>
int tdrive_android_poster(const char *path, size_t pathLength, int edge, int seekPercent, long long fallbackMicros, void **bytes, size_t *length);
*/
import "C"

import (
	"context"
	"fmt"
	"unsafe"
)

// generateVideoPoster draws one frame with MediaMetadataRetriever, through the
// same Java bridge the sampled photo path already uses.
//
// The platform decoder rather than a bundled one: Android has no mpv to ship,
// the retriever reaches the hardware decoders that read the phone's own camera
// files, and on API 27+ it scales the frame during extraction, so a 4K video
// never becomes a 4K bitmap on a device that cannot spare one.
func generateVideoPoster(ctx context.Context, path string, maxEdge int) ([]byte, error) {
	return posterDecode(ctx, func() ([]byte, error) {
		frame, err := androidPosterFrame(path, maxEdge)
		if err != nil {
			return nil, err
		}
		defer clear(frame)
		return posterFromFrame(frame, maxEdge)
	})
}

// androidPosterFrame returns the JPEG the Java side encoded, or the reason it
// could not. Every outcome the bridge reports is "no picture": the only thing
// that distinguishes them is what a log would say.
func androidPosterFrame(path string, maxEdge int) ([]byte, error) {
	name := C.CString(path)
	defer C.free(unsafe.Pointer(name))
	var data unsafe.Pointer
	var size C.size_t
	code := C.tdrive_android_poster(
		name, C.size_t(len(path)), C.int(maxEdge),
		C.int(posterSeekPercent), C.longlong(posterFallbackOffset.Microseconds()),
		&data, &size,
	)
	if code == 3 {
		// No activity has bound the bridge: a CLI or test host. There is no
		// portable video decoder to fall back to, unlike the image path.
		return nil, fmt.Errorf("%w: no android media host", ErrPosterUnsupported)
	}
	if code != 0 {
		return nil, fmt.Errorf("%w: no frame decoded", ErrPosterUnsupported)
	}
	defer func() {
		clear(unsafe.Slice((*byte)(data), int(size)))
		C.free(data)
	}()
	return C.GoBytes(data, C.int(size)), nil
}
