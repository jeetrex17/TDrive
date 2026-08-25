package mountdav

import (
	"os"
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

const (
	maxWritablePathBytes      = 4096
	maxWritableComponentBytes = 240
)

func cleanWritablePath(value string) (string, error) {
	clean, err := cleanWebDAVName(value)
	if err != nil {
		return "", err
	}
	clean = norm.NFC.String(clean)
	if len(clean) > maxWritablePathBytes {
		return "", os.ErrInvalid
	}
	if clean == "/" {
		return clean, nil
	}
	for _, component := range strings.Split(strings.TrimPrefix(clean, "/"), "/") {
		if !portableWritableComponent(component) {
			return "", os.ErrInvalid
		}
	}
	return clean, nil
}

func portableWritableComponent(component string) bool {
	if component == "" || len(component) > maxWritableComponentBytes ||
		strings.HasSuffix(component, ".") || strings.HasSuffix(component, " ") ||
		windowsReservedComponent(component) {
		return false
	}
	for _, character := range component {
		if unicode.IsControl(character) || unicode.Is(unicode.Bidi_Control, character) ||
			strings.ContainsRune(`<>:"/\|?*`, character) {
			return false
		}
	}
	return true
}

// isMacOSJunkPath reports whether path's final component is metadata macOS
// itself writes to any mounted volume: Finder's per-directory .DS_Store, its
// per-file AppleDouble ._ sidecars carrying resource forks/xattrs WebDAV has
// nowhere to store, and Spotlight/Trash/FSEvents' volume-root bookkeeping.
// These are legal path components -- portableWritableComponent accepts them
// -- but callers use this to fake success without ever staging or committing
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

func windowsReservedComponent(component string) bool {
	stem := component
	if index := strings.IndexByte(stem, '.'); index >= 0 {
		stem = stem[:index]
	}
	stem = strings.ToUpper(stem)
	switch stem {
	case "CON", "PRN", "AUX", "NUL", "CLOCK$":
		return true
	}
	characters := []rune(stem)
	if len(characters) != 4 || (string(characters[:3]) != "COM" && string(characters[:3]) != "LPT") {
		return false
	}
	switch characters[3] {
	case '1', '2', '3', '4', '5', '6', '7', '8', '9', '\u00b9', '\u00b2', '\u00b3':
		return true
	default:
		return false
	}
}
