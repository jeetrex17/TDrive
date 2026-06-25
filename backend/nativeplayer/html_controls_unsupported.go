//go:build !darwin && !windows

package nativeplayer

func SupportsHTMLControls() bool {
	return false
}
