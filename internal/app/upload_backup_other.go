//go:build !ios

package app

func excludeUploadSourceFromBackup(string) error { return nil }
