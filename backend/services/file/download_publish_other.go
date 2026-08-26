//go:build !darwin && !linux && !windows

package file

import "fmt"

func publishDirectoryNoReplace(_, _ string) error {
	return fmt.Errorf("atomic no-replace folder publication is unsupported on this platform")
}
