//go:build darwin && !ios

package nativeplayer

import "os"

const darwinNativePlayerFlag = "TDRIVE_EXPERIMENTAL_MACOS_NATIVE_PLAYER"

func darwinNativePlayerEnabled() bool {
	return nativePlayerEnabled(os.Getenv(darwinNativePlayerFlag))
}
