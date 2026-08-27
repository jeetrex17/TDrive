//go:build linux

package nativeplayer

import "testing"

func TestLinuxNativePlayerRequiresExplicitOptIn(t *testing.T) {
	t.Setenv(linuxNativePlayerFlag, "")

	if linuxNativePlayerEnabled() {
		t.Fatalf("linuxNativePlayerEnabled() = true, want false by default")
	}
}

func TestLinuxNativePlayerCanBeEnabled(t *testing.T) {
	t.Setenv(linuxNativePlayerFlag, "1")

	if !linuxNativePlayerEnabled() {
		t.Fatalf("linuxNativePlayerEnabled() = false, want true when %s=1", linuxNativePlayerFlag)
	}
}
