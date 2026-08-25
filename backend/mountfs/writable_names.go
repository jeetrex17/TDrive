package mountfs

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"
)

const forbiddenWritableNameCharacters = `<>:"/\\|?*`

// NormalizeWritableName validates a new file or folder name against TDrive's
// cross-platform namespace and returns its NFC display spelling. Legacy read
// entries continue to use portableName aliases and are not rejected.
func NormalizeWritableName(name string) (string, error) {
	if !utf8.ValidString(name) {
		return "", fmt.Errorf("%w: name is not valid UTF-8", ErrInvalidName)
	}
	name = norm.NFC.String(name)
	if name == "" {
		return "", fmt.Errorf("%w: name is empty", ErrInvalidName)
	}
	if name == "." || name == ".." {
		return "", fmt.Errorf("%w: traversal names are not allowed", ErrInvalidName)
	}
	if len(name) > maxPortableNameBytes {
		return "", fmt.Errorf("%w: name exceeds %d bytes", ErrInvalidName, maxPortableNameBytes)
	}
	if strings.HasSuffix(name, ".") || strings.HasSuffix(name, " ") {
		return "", fmt.Errorf("%w: trailing dots and spaces are not portable", ErrInvalidName)
	}
	for _, character := range name {
		if unicode.IsControl(character) || strings.ContainsRune(forbiddenWritableNameCharacters, character) {
			return "", fmt.Errorf("%w: name contains a non-portable character", ErrInvalidName)
		}
	}
	if isWindowsReservedName(name) {
		return "", fmt.Errorf("%w: Windows device names are not allowed", ErrInvalidName)
	}
	return name, nil
}

// ValidateWritableName reports whether a new file or folder name belongs to
// TDrive's portable namespace.
func ValidateWritableName(name string) error {
	_, err := NormalizeWritableName(name)
	return err
}

// NameKey returns the NFC-normalized, Unicode case-folded namespace key shared
// by files and folders. ValidateWritableName must be applied at write
// boundaries before persisting a user-provided name.
func NameKey(name string) string {
	return portableFold.String(norm.NFC.String(name))
}
