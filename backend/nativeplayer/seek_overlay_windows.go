//go:build windows

package nativeplayer

import (
	"fmt"
	"os"
	"syscall"
	"unsafe"
)

// overlayDebugf logs seek-overlay diagnostics to stderr when
// TDRIVE_MEDIA_THUMB_DEBUG=1 (set by the Windows test Debug launcher). It lets us
// trace the overlay path on a real machine without a debugger.
func overlayDebugf(format string, args ...any) {
	if os.Getenv("TDRIVE_MEDIA_THUMB_DEBUG") == "1" {
		fmt.Fprintf(os.Stderr, "[seek-overlay] "+format+"\n", args...)
	}
}

// Seek-thumbnail overlay.
//
// WebView2 cannot paint HTML above the mpv child window (native windows always
// sit above webview content), so the seek-preview thumbnail is drawn by a
// dedicated STATIC child window raised above the video. SS_BITMAP +
// SS_REALSIZECONTROL lets the control display a stretched HBITMAP with no custom
// WndProc, which keeps this path simple and robust. Every call here runs on the
// window thread (see windowsWindowThread), so the HWND/HBITMAP handles are
// touched from a single thread and need no locking.

var (
	gdi32 = syscall.NewLazyDLL("gdi32.dll")

	procCreateDIBSection   = gdi32.NewProc("CreateDIBSection")
	procDeleteObject       = gdi32.NewProc("DeleteObject")
	procCreateRoundRectRgn = gdi32.NewProc("CreateRoundRectRgn")

	procSendMessageW = user32.NewProc("SendMessageW")
	procSetWindowRgn = user32.NewProc("SetWindowRgn")
)

const (
	ssBitmap          = 0x0000000E
	ssRealSizeControl = 0x00000040
	stmSetImage       = 0x0172
	imageBitmap       = 0
	dibRGBColors      = 0
	biRGB             = 0

	// seekOverlayCornerRadius rounds the preview corners to match the app's
	// surfaces. It is in physical pixels; the small radius reads well across DPI
	// without per-scale tuning.
	seekOverlayCornerRadius = 12
)

type bitmapInfoHeader struct {
	Size          uint32
	Width         int32
	Height        int32
	Planes        uint16
	BitCount      uint16
	Compression   uint32
	SizeImage     uint32
	XPelsPerMeter int32
	YPelsPerMeter int32
	ClrUsed       uint32
	ClrImportant  uint32
}

// createSeekOverlayWindow creates the hidden STATIC child used to paint seek
// thumbnails over the video. It intentionally does not use WS_CLIPSIBLINGS:
// this window is supposed to overlap the mpv child HWND, and that style can
// clip the thumbnail out exactly where it needs to be visible.
func createSeekOverlayWindow(parent uintptr) (uintptr, error) {
	className, _ := syscall.UTF16PtrFromString("STATIC")
	title, _ := syscall.UTF16PtrFromString("TDriveSeekOverlay")
	style := uintptr(wsChild | ssBitmap | ssRealSizeControl)
	hwnd, _, callErr := procCreateWindowExW.Call(
		0,
		uintptr(unsafe.Pointer(className)),
		uintptr(unsafe.Pointer(title)),
		style,
		0, 0, 0, 0,
		parent,
		0, 0, 0,
	)
	if hwnd == 0 {
		return 0, callFailed("CreateWindowExW(seek overlay)", callErr)
	}
	return hwnd, nil
}

// newOverlayDIB builds a top-down 32bpp DIB section from bmp and returns its
// HBITMAP. The caller owns the handle and must DeleteObject it once the STATIC
// no longer references it. Must run on the window thread.
func newOverlayDIB(bmp *overlayBitmap) (uintptr, error) {
	bi := bitmapInfoHeader{
		Planes:      1,
		BitCount:    32,
		Compression: biRGB,
		Width:       int32(bmp.Width),
		Height:      -int32(bmp.Height), // negative height => top-down, matching our buffer
	}
	bi.Size = uint32(unsafe.Sizeof(bi))

	// bits receives a pointer to the DIB's pixel memory (OS-owned, so the GC
	// never moves it). Keeping it as unsafe.Pointer avoids a uintptr round-trip.
	var bits unsafe.Pointer
	hbm, _, callErr := procCreateDIBSection.Call(
		0,
		uintptr(unsafe.Pointer(&bi)),
		dibRGBColors,
		uintptr(unsafe.Pointer(&bits)),
		0,
		0,
	)
	if hbm == 0 || bits == nil {
		return 0, callFailed("CreateDIBSection", callErr)
	}
	dst := unsafe.Slice((*byte)(bits), len(bmp.Pixels))
	copy(dst, bmp.Pixels)
	return hbm, nil
}

// positionSeekOverlay places the overlay at x,y,w,h (physical pixels, parent
// client coords), raises it above the video child, rounds its corners, and
// shows it without taking focus. Must run on the window thread.
func positionSeekOverlay(hwnd uintptr, x, y, w, h int) {
	ret, _, callErr := procSetWindowPos.Call(
		hwnd,
		hwndTop,
		uintptr(x), uintptr(y), uintptr(w), uintptr(h),
		swpNoActivate|swpShowWindow,
	)
	overlayDebugf("SetWindowPos hwnd=%d x=%d y=%d w=%d h=%d ret=%d err=%v", hwnd, x, y, w, h, ret, callErr)
	// CreateRoundRectRgn's bounding rect is exclusive on the right/bottom edge.
	rgn, _, _ := procCreateRoundRectRgn.Call(0, 0, uintptr(w+1), uintptr(h+1), seekOverlayCornerRadius, seekOverlayCornerRadius)
	if rgn != 0 {
		// On success SetWindowRgn takes ownership of the region; only free it
		// ourselves if the call failed.
		if ret, _, _ := procSetWindowRgn.Call(hwnd, rgn, 1); ret == 0 {
			procDeleteObject.Call(rgn)
		}
	}
}
