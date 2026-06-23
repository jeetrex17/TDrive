//go:build linux

package nativeplayer

import "os"

const linuxNativePlayerFlag = "TDRIVE_EXPERIMENTAL_LINUX_NATIVE_PLAYER"

func linuxNativePlayerEnabled() bool {
	return os.Getenv(linuxNativePlayerFlag) == "1"
}
