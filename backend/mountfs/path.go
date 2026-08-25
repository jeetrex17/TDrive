package mountfs

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"
)

const maxPathBytes = 4096

func splitAbsolutePath(value string) ([]string, error) {
	if value == "" || value[0] != '/' {
		return nil, fmt.Errorf("%w: path must be absolute", ErrInvalidPath)
	}
	if !utf8.ValidString(value) {
		return nil, fmt.Errorf("%w: path is not valid UTF-8", ErrInvalidPath)
	}
	if strings.ContainsRune(value, '\x00') {
		return nil, fmt.Errorf("%w: NUL is not allowed", ErrInvalidPath)
	}
	if strings.ContainsRune(value, '\\') {
		return nil, fmt.Errorf("%w: backslash is not allowed", ErrInvalidPath)
	}
	value = norm.NFC.String(value)
	if value == "/" {
		return nil, nil
	}
	if len(value) > maxPathBytes {
		return nil, fmt.Errorf("%w: path exceeds %d bytes", ErrInvalidPath, maxPathBytes)
	}

	components := strings.Split(value[1:], "/")
	for _, component := range components {
		switch component {
		case "":
			return nil, fmt.Errorf("%w: empty path component", ErrInvalidPath)
		case ".", "..":
			return nil, fmt.Errorf("%w: traversal component %q", ErrInvalidPath, component)
		}
		if len(component) > maxPortableNameBytes {
			return nil, fmt.Errorf("%w: component exceeds %d bytes", ErrInvalidPath, maxPortableNameBytes)
		}
	}
	return components, nil
}
