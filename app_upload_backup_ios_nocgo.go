//go:build ios && !cgo

package main

func excludeUploadSourceFromBackup(string) error {
	return nil
}
