//go:build ios && cgo

package app

/*
#cgo CFLAGS: -x objective-c
#cgo LDFLAGS: -framework Foundation
#include <stdlib.h>
#include <string.h>
#import <Foundation/Foundation.h>

static int tdriveExcludeFromBackup(const char *path, char **reason) {
    @autoreleasepool {
        NSString *name = [NSString stringWithUTF8String:path];
        NSURL *url = name == nil ? nil : [NSURL fileURLWithPath:name];
        NSError *error = nil;
        if (url != nil && [url setResourceValue:@YES
                                        forKey:NSURLIsExcludedFromBackupKey
                                         error:&error]) {
            return 0;
        }
        NSString *message = error.localizedDescription;
        *reason = strdup(message == nil ? "Foundation could not set the backup exclusion" : message.UTF8String);
        return 1;
    }
}
*/
import "C"

import (
	"fmt"
	"unsafe"
)

func excludeUploadSourceFromBackup(path string) error {
	name := C.CString(path)
	defer C.free(unsafe.Pointer(name))
	var reason *C.char
	if C.tdriveExcludeFromBackup(name, &reason) == 0 {
		return nil
	}
	if reason == nil {
		return fmt.Errorf("exclude upload source from iCloud backup: Foundation did not provide a reason")
	}
	defer C.free(unsafe.Pointer(reason))
	return fmt.Errorf("exclude upload source from iCloud backup: %s", C.GoString(reason))
}
