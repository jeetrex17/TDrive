package updater

import (
	"testing"
	"time"
)

func TestWaitPIDFromArgs(t *testing.T) {
	cases := []struct {
		args []string
		pid  int
		ok   bool
	}{
		{[]string{"--wait-for-pid", "42"}, 42, true},
		{[]string{"--wait-for-pid=42"}, 42, true},
		{[]string{"-psn_0_1", "--wait-for-pid", "7"}, 7, true},
		{[]string{"--wait-for-pid"}, 0, false},
		{[]string{"--wait-for-pid", "abc"}, 0, false},
		{[]string{"--wait-for-pid", "0"}, 0, false},
		{nil, 0, false},
	}
	for _, tc := range cases {
		pid, ok := WaitPIDFromArgs(tc.args)
		if pid != tc.pid || ok != tc.ok {
			t.Errorf("WaitPIDFromArgs(%v) = %d, %v; want %d, %v", tc.args, pid, ok, tc.pid, tc.ok)
		}
	}
}

func TestWaitForExit(t *testing.T) {
	polls := 0
	alive := func(int) bool {
		polls++
		return polls < 3
	}
	if !WaitForExit(1, time.Second, alive) {
		t.Fatalf("expected process to be reported gone")
	}
	if WaitForExit(1, 50*time.Millisecond, func(int) bool { return true }) {
		t.Fatalf("expected timeout to report the process still alive")
	}
}
