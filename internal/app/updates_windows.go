package app

import (
	"log/slog"
	"unsafe"

	"golang.org/x/sys/windows"
)

// GetCurrentPackageFullName reports identity even for a sideloaded MSIX. A
// filename, build tag or install-directory check cannot reliably identify it.
func storeManagedUpdates() bool {
	proc := windows.NewLazySystemDLL("kernel32.dll").NewProc("GetCurrentPackageFullName")
	if err := proc.Find(); err != nil {
		slog.Warn("cannot query Windows package identity; disabling self updates", "error", err)
		return true
	}
	var length uint32
	code, _, _ := proc.Call(uintptr(unsafe.Pointer(&length)), 0)
	switch code {
	case uintptr(windows.APPMODEL_ERROR_NO_PACKAGE):
		return false
	case uintptr(windows.ERROR_INSUFFICIENT_BUFFER):
		return true
	default:
		// Fail closed if Windows cannot establish that the process is unpackaged.
		slog.Warn("cannot query Windows package identity; disabling self updates", "code", code)
		return true
	}
}
