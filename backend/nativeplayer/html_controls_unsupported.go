//go:build !darwin

package nativeplayer

func SupportsHTMLControls() bool {
	return false
}
