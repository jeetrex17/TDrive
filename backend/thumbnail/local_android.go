//go:build android && cgo

package thumbnail

/*
#include <stdlib.h>
int tdrive_android_thumbnail(const char *path, size_t pathLength, int edge, int encoded, int orientation, void **bytes, size_t *length);
*/
import "C"

import "unsafe"

func generateNativeLocal(path string, edge int) ([]byte, bool, error) {
	name := C.CString(path)
	defer C.free(unsafe.Pointer(name))
	var data unsafe.Pointer
	var size C.size_t
	code := C.tdrive_android_thumbnail(name, C.size_t(len(path)), C.int(edge), 0, 1, &data, &size)
	return nativeAndroidResult(code, data, size)
}

func generateNativeBytes(input []byte, edge int) ([]byte, bool, error) {
	if len(input) == 0 {
		return nil, true, ErrUnsupported
	}
	var data unsafe.Pointer
	var size C.size_t
	code := C.tdrive_android_thumbnail((*C.char)(unsafe.Pointer(&input[0])), C.size_t(len(input)), C.int(edge), 1, C.int(exifOrientation(input)), &data, &size)
	return nativeAndroidResult(code, data, size)
}

func nativeAndroidResult(code C.int, data unsafe.Pointer, size C.size_t) ([]byte, bool, error) {
	if code == 3 {
		return nil, false, nil
	} // CLI/non-Java host: guarded portable decoder.
	if code != 0 {
		return nil, true, ErrUnsupported
	}
	defer func() { clear(unsafe.Slice((*byte)(data), int(size))); C.free(data) }()
	return C.GoBytes(data, C.int(size)), true, nil
}
