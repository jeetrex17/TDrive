//go:build ios

package main

/*
#cgo CFLAGS: -x objective-c
#cgo LDFLAGS: -framework Foundation
#include <stdlib.h>
#import <Foundation/Foundation.h>

static char *tdriveDocumentsDir(void) {
    NSString *dir = NSSearchPathForDirectoriesInDomains(
        NSDocumentDirectory, NSUserDomainMask, YES).firstObject;
    return dir == nil ? NULL : strdup(dir.UTF8String);
}
*/
import "C"

import "unsafe"

// visibleStorageDir is the app container's Documents folder.
//
// It is the only directory iOS will let the Files app list, and only because
// Info.plist asks for it with UIFileSharingEnabled. Everything else TDrive
// writes stays in Application Support, where the database and the Telegram
// session cannot be dragged to the trash by accident.
func visibleStorageDir() string {
	dir := C.tdriveDocumentsDir()
	if dir == nil {
		return ""
	}
	defer C.free(unsafe.Pointer(dir))
	return C.GoString(dir)
}
