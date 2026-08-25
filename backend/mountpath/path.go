// Package mountpath owns the cross-platform path rules shared by mounted
// filesystem adapters. Callers choose whether to apply writable-name rules so
// legacy read aliases remain addressable.
package mountpath

import (
	"errors"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"
)

const (
	// MaxPathBytes bounds a normalized absolute mount path.
	MaxPathBytes = 4096
	// MaxComponentBytes leaves room for collision suffixes and filesystem
	// metadata beneath common 255-byte component limits.
	MaxComponentBytes = 240
)

const forbiddenPortableCharacters = `<>:"/\|?*`

// ErrInvalid identifies a path or component outside the mounted namespace.
var ErrInvalid = errors.New("invalid mount path")

// Options controls the small differences between protocol and filesystem
// boundaries. Zero-value options validate legacy absolute read paths.
type Options struct {
	AllowEmptyRoot        bool
	TrimTrailingSlash     bool
	PortableComponents    bool
	RejectWindowsReserved bool
}

// ComponentOptions controls validation for a single namespace component.
type ComponentOptions struct {
	Portable              bool
	RejectWindowsReserved bool
}

// ParseAbsolute normalizes an absolute path to NFC, validates its bounds and
// components, and returns both the normalized path and its component slice.
func ParseAbsolute(value string, options Options) (string, []string, error) {
	if !utf8.ValidString(value) {
		return "", nil, invalid("path is not valid UTF-8")
	}
	if value == "" && options.AllowEmptyRoot {
		value = "/"
	}
	if value == "" || value[0] != '/' {
		return "", nil, invalid("path must be absolute")
	}
	if strings.ContainsRune(value, '\x00') {
		return "", nil, invalid("NUL is not allowed")
	}
	if strings.ContainsRune(value, '\\') {
		return "", nil, invalid("backslash is not allowed")
	}

	value = norm.NFC.String(value)
	if value == "/" {
		return value, nil, nil
	}
	if options.TrimTrailingSlash && strings.HasSuffix(value, "/") {
		value = strings.TrimSuffix(value, "/")
		if strings.HasSuffix(value, "/") {
			return "", nil, invalid("empty path component")
		}
	}
	if len(value) > MaxPathBytes {
		return "", nil, invalid("path exceeds %d bytes", MaxPathBytes)
	}
	if value == "/" {
		return value, nil, nil
	}

	components := strings.Split(value[1:], "/")
	componentOptions := ComponentOptions{
		Portable:              options.PortableComponents,
		RejectWindowsReserved: options.RejectWindowsReserved,
	}
	for _, component := range components {
		if _, err := NormalizeComponent(component, componentOptions); err != nil {
			return "", nil, err
		}
	}
	return value, components, nil
}

// NormalizeComponent returns an NFC component after applying structural and,
// when requested, cross-platform writable-name rules.
func NormalizeComponent(value string, options ComponentOptions) (string, error) {
	if !utf8.ValidString(value) {
		return "", invalid("component is not valid UTF-8")
	}
	value = norm.NFC.String(value)
	switch value {
	case "":
		return "", invalid("empty path component")
	case ".", "..":
		return "", invalid("traversal component %q", value)
	}
	if len(value) > MaxComponentBytes {
		return "", invalid("component exceeds %d bytes", MaxComponentBytes)
	}
	if strings.ContainsRune(value, '\x00') || strings.ContainsRune(value, '\\') || strings.ContainsRune(value, '/') {
		return "", invalid("component contains a path separator or NUL")
	}
	if options.Portable {
		if strings.HasSuffix(value, ".") || strings.HasSuffix(value, " ") {
			return "", invalid("trailing dots and spaces are not portable")
		}
		for _, character := range value {
			if unicode.IsControl(character) || unicode.Is(unicode.Bidi_Control, character) ||
				strings.ContainsRune(forbiddenPortableCharacters, character) {
				return "", invalid("component contains a non-portable character")
			}
		}
	}
	if options.RejectWindowsReserved && IsWindowsReservedComponent(value) {
		return "", invalid("Windows device names are not allowed")
	}
	return value, nil
}

// IsWindowsReservedComponent reports whether Windows treats a component's
// extension-free stem as a reserved DOS device name.
func IsWindowsReservedComponent(component string) bool {
	stem := norm.NFC.String(component)
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

func invalid(format string, values ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalid, fmt.Sprintf(format, values...))
}
