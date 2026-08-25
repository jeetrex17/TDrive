package mountfs

import (
	"fmt"

	"TDrive/backend/mountpath"

	"golang.org/x/text/unicode/norm"
)

// NormalizeWritableName validates a new file or folder name against TDrive's
// cross-platform namespace and returns its NFC display spelling. Legacy read
// entries continue to use portableName aliases and are not rejected.
func NormalizeWritableName(name string) (string, error) {
	normalized, err := mountpath.NormalizeComponent(name, mountpath.ComponentOptions{
		Portable:              true,
		RejectWindowsReserved: true,
	})
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrInvalidName, err)
	}
	return normalized, nil
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
