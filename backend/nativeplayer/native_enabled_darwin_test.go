//go:build darwin

package nativeplayer

import "testing"

func TestDarwinNativePlayerIsOnByDefault(t *testing.T) {
	t.Setenv(darwinNativePlayerFlag, "")

	if !darwinNativePlayerEnabled() {
		t.Fatalf("darwinNativePlayerEnabled() = false, want true by default")
	}
}

func TestDarwinNativePlayerCanBeDisabled(t *testing.T) {
	t.Setenv(darwinNativePlayerFlag, "0")

	if darwinNativePlayerEnabled() {
		t.Fatalf("darwinNativePlayerEnabled() = true, want false when %s=0", darwinNativePlayerFlag)
	}
}
