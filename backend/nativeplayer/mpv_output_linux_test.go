//go:build linux

package nativeplayer

import (
	"errors"
	"strings"
	"testing"
)

func TestMPVOutputKeepsTail(t *testing.T) {
	out := &mpvOutput{}
	_, _ = out.Write([]byte(strings.Repeat("x", mpvOutputLimit)))
	_, _ = out.Write([]byte("\n[vo/gpu] failed to open display\n"))

	tail := out.Tail()
	if !strings.HasSuffix(tail, "[vo/gpu] failed to open display") {
		t.Fatalf("Tail() = %q, want it to end with the latest mpv message", tail)
	}
	if len(tail) > mpvOutputLimit {
		t.Fatalf("len(Tail()) = %d, want at most %d", len(tail), mpvOutputLimit)
	}
}

func TestMPVStartupErrorQuotesOutput(t *testing.T) {
	err := mpvStartupError(errors.New("exit status 1"), "Error parsing option --wid")
	if !strings.Contains(err.Error(), "exit status 1") || !strings.Contains(err.Error(), "Error parsing option --wid") {
		t.Fatalf("mpvStartupError() = %q, want the exit status and mpv output", err)
	}
	if got := mpvStartupError(nil, "").Error(); !strings.Contains(got, "no output") {
		t.Fatalf("mpvStartupError(nil, \"\") = %q, want a no-output note", got)
	}
}
