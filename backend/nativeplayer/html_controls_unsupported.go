//go:build (!darwin && !windows) || ios

package nativeplayer

func SupportsHTMLControls() bool {
	return false
}
