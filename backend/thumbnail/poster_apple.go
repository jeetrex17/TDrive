//go:build ios && cgo

package thumbnail

/*
#cgo LDFLAGS: -framework AVFoundation -framework CoreMedia -framework Foundation
#include <stdlib.h>
int tdrive_video_poster(const char *path, int edge, int seekPercent, long long fallbackMicros, void **bytes, size_t *length);
*/
import "C"

import (
	"context"
	"fmt"
	"unsafe"
)

// generateVideoPoster draws one frame with AVAssetImageGenerator.
//
// iOS only, deliberately: macOS is a desktop and keeps mpv, whose codec
// coverage is far wider than AVFoundation's -- it opens the .mkv and .avi files
// AVFoundation refuses outright. On a phone there is no mpv to ship and every
// file worth a poster came from a camera, which is exactly what AVFoundation
// decodes in hardware.
func generateVideoPoster(ctx context.Context, path string, maxEdge int) ([]byte, error) {
	return posterDecode(ctx, func() ([]byte, error) {
		frame, err := applePosterFrame(path, maxEdge)
		if err != nil {
			return nil, err
		}
		defer clear(frame)
		return posterFromFrame(frame, maxEdge)
	})
}

// applePosterFrame returns the JPEG AVFoundation produced, or the reason it
// could not -- which is always the same reason as far as the caller is
// concerned: there is no picture for this file.
func applePosterFrame(path string, maxEdge int) ([]byte, error) {
	name := C.CString(path)
	defer C.free(unsafe.Pointer(name))
	var data unsafe.Pointer
	var size C.size_t
	code := C.tdrive_video_poster(
		name, C.int(maxEdge),
		C.int(posterSeekPercent), C.longlong(posterFallbackOffset.Microseconds()),
		&data, &size,
	)
	if code != 0 {
		return nil, fmt.Errorf("%w: no frame decoded", ErrPosterUnsupported)
	}
	defer func() {
		clear(unsafe.Slice((*byte)(data), int(size)))
		C.free(data)
	}()
	return C.GoBytes(data, C.int(size)), nil
}
