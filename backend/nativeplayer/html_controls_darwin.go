//go:build darwin && !ios

package nativeplayer

func SupportsHTMLControls() bool {
	return true
}
