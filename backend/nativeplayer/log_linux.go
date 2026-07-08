//go:build linux

package nativeplayer

import "log"

func linuxNativeLogf(format string, args ...any) {
	log.Printf("tdrive linux native player: "+format, args...)
}
