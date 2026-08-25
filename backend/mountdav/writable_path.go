package mountdav

import (
	"os"
	"strings"

	"TDrive/backend/mountpath"
)

const maxWritableComponentBytes = mountpath.MaxComponentBytes

func cleanWritablePath(value string) (string, error) {
	clean, _, err := mountpath.ParseAbsolute(value, mountpath.Options{
		AllowEmptyRoot:        true,
		TrimTrailingSlash:     true,
		PortableComponents:    true,
		RejectWindowsReserved: true,
	})
	if err != nil {
		return "", os.ErrInvalid
	}
	return clean, nil
}

// isMacOSJunkPath reports whether path's final component is metadata macOS
// itself writes to any mounted volume: Finder's per-directory .DS_Store, its
// per-file AppleDouble ._ sidecars carrying resource forks/xattrs WebDAV has
// nowhere to store, and Spotlight/Trash/FSEvents' volume-root bookkeeping.
// These are legal portable path components, but callers use this to fake
// success without ever staging or committing
// anything for them. A hard rejection was tried first and reverted: Finder's
// copy engine can treat a rejected AppleDouble sidecar write as making the
// visible file copy itself fail, which is worse than silently discarding
// content nobody asked to store.
func macOSJunkComponent(component string) bool {
	if strings.HasPrefix(component, "._") {
		return true
	}
	switch component {
	case ".DS_Store", ".Spotlight-V100", ".Trashes", ".fseventsd", ".TemporaryItems", ".apdisk":
		return true
	default:
		return false
	}
}

// isMacOSJunkPath reports whether a cleaned, absolute path's final component
// is macOS metadata per macOSJunkComponent. The root path ("/") never is.
func isMacOSJunkPath(path string) bool {
	if path == "/" {
		return false
	}
	if index := strings.LastIndexByte(path, '/'); index >= 0 {
		return macOSJunkComponent(path[index+1:])
	}
	return macOSJunkComponent(path)
}
