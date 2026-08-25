package mountfs

import (
	"fmt"

	"TDrive/backend/mountpath"
)

func splitAbsolutePath(value string) ([]string, error) {
	_, components, err := mountpath.ParseAbsolute(value, mountpath.Options{})
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidPath, err)
	}
	return components, nil
}
