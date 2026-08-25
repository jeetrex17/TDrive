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
		if unicode.IsControl(character) || strings.ContainsRune(`<>:"/\|?*`, character) {
			return false
		}
	}
	return true
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
