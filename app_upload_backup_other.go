//go:build !ios

package main

func excludeUploadSourceFromBackup(string) error { return nil }
