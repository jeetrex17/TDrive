//go:build windows

package nativeplayer

func SupportsHTMLControls() bool {
	// Windowed WebView2 transparency does not reliably reveal a sibling child
	// HWND underneath it. Keep Windows on the explicit fallback layout where mpv
	// owns only the video rectangle and TDrive owns the reserved chrome strips.
	return false
}
