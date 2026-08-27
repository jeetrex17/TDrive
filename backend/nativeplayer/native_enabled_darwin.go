//go:build darwin

package nativeplayer

import "os"

const darwinNativePlayerFlag = "TDRIVE_EXPERIMENTAL_MACOS_NATIVE_PLAYER"

func darwinNativePlayerEnabled() bool {
	return experimentalNativePlayerEnabled(os.Getenv(darwinNativePlayerFlag))
}
