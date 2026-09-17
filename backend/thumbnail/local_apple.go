//go:build (darwin || ios) && cgo

package thumbnail

/*
#cgo LDFLAGS: -framework CoreFoundation -framework CoreGraphics -framework ImageIO
#include <stdlib.h>
int tdrive_image_thumbnail(const char *path, int edge, void **bytes, size_t *length);
int tdrive_image_thumbnail_data(const void *input, size_t count, int edge, void **bytes, size_t *length);
*/
import "C"

import "unsafe"

func generateNativeLocal(path string, edge int) ([]byte, bool, error) {
	name := C.CString(path)
	defer C.free(unsafe.Pointer(name))
	var data unsafe.Pointer
	var size C.size_t
	code := C.tdrive_image_thumbnail(name, C.int(edge), &data, &size)
	return nativeImageResult(code, data, size)
}

func generateNativeBytes(input []byte, edge int) ([]byte, bool, error) {
	if len(input) == 0 {
		return nil, true, ErrUnsupported
	}
	var data unsafe.Pointer
	var size C.size_t
	code := C.tdrive_image_thumbnail_data(unsafe.Pointer(&input[0]), C.size_t(len(input)), C.int(edge), &data, &size)
	return nativeImageResult(code, data, size)
}

func nativeImageResult(code C.int, data unsafe.Pointer, size C.size_t) ([]byte, bool, error) {
	if code == 2 {
		return nil, true, ErrTooLarge
	}
	if code != 0 {
		return nil, true, ErrUnsupported
	}
	defer func() { clear(unsafe.Slice((*byte)(data), int(size))); C.free(data) }()
	return C.GoBytes(data, C.int(size)), true, nil
}
