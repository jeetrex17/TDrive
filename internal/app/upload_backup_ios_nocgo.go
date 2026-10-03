//go:build ios && !cgo

package app

func excludeUploadSourceFromBackup(string) error {
	return nil
}
