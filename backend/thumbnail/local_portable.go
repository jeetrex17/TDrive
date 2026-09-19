//go:build (!darwin && !ios && !android) || !cgo

package thumbnail

func generateNativeLocal(_ string, _ int) ([]byte, bool, error) {
	return nil, false, nil
}

func generateNativeBytes(_ []byte, _ int) ([]byte, bool, error) {
	return nil, false, nil
}
