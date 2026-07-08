//go:build linux

package nativeplayer

import "testing"

func TestLinuxNativePlayerEnabledByDefault(t *testing.T) {
	t.Setenv(linuxNativePlayerFlag, "")

	if !linuxNativePlayerEnabled() {
		t.Fatalf("linuxNativePlayerEnabled() = false, want true by default")
	}
}

func TestLinuxNativePlayerCanBeDisabled(t *testing.T) {
	t.Setenv(linuxNativePlayerFlag, "0")

	if linuxNativePlayerEnabled() {
		t.Fatalf("linuxNativePlayerEnabled() = true, want false when %s=0", linuxNativePlayerFlag)
	}
}
