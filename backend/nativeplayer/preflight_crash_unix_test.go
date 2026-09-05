//go:build darwin || linux

package nativeplayer

import (
	"os/exec"
	"testing"
)

func TestDecoderCrashSignalDistinguishesCrashFromOrdinaryExit(t *testing.T) {
	crashErr := exec.Command("sh", "-c", "kill -SEGV $$").Run()
	if signal, ok := decoderCrashSignal(crashErr); !ok || signal.String() == "" {
		t.Fatalf("decoderCrashSignal(crash) = %v, %t; want crash signal", signal, ok)
	}

	ordinaryErr := exec.Command("sh", "-c", "exit 2").Run()
	if signal, ok := decoderCrashSignal(ordinaryErr); ok {
		t.Fatalf("decoderCrashSignal(ordinary exit) = %v, true; want false", signal)
	}
}
