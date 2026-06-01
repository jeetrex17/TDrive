package auth

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	coreauth "TDrive/backend/auth"
)

type fakeEvents struct {
	events []eventCall
}

type eventCall struct {
	name string
	args []any
}

func (f *fakeEvents) Emit(name string, args ...any) {
	f.events = append(f.events, eventCall{name: name, args: args})
}

func TestSystemStatusNeedsSetupWhenNoCreds(t *testing.T) {
	setupConfigDir(t)
	svc := NewService(nil)

	if got := svc.SystemStatus(); got != "NEEDS_SETUP" {
		t.Fatalf("SystemStatus = %q, want NEEDS_SETUP", got)
	}
}

func TestSystemStatusReadyWhenCredsExist(t *testing.T) {
	setupConfigDir(t)
	if err := coreauth.SaveImpCredentials(123, "hash"); err != nil {
		t.Fatalf("save creds: %v", err)
	}
	svc := NewService(nil)

	if got := svc.SystemStatus(); got != "READY_FOR_LOGIN" {
		t.Fatalf("SystemStatus = %q, want READY_FOR_LOGIN", got)
	}
}

func TestSubmitCodeUnblocksGetCodech(t *testing.T) {
	svc := NewService(nil)
	done := make(chan string, 1)
	go func() {
		done <- <-svc.Codech()
	}()

	svc.SubmitCode("12345")
	select {
	case got := <-done:
		if got != "12345" {
			t.Fatalf("code = %q, want 12345", got)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for code channel")
	}
}

func TestSendHintEmitsEvent(t *testing.T) {
	events := &fakeEvents{}
	svc := NewService(events)

	svc.SendHint("Hint : 12")

	if len(events.events) != 1 {
		t.Fatalf("events = %+v, want one", events.events)
	}
	ev := events.events[0]
	if ev.name != "gothint" {
		t.Fatalf("event name = %q, want gothint", ev.name)
	}
	if len(ev.args) != 1 || ev.args[0] != "Hint : 12" {
		t.Fatalf("event args = %+v, want hint payload", ev.args)
	}
}

// StartLogin drives a real Telegram auth flow, so it stays integration-tested.
// Unit tests here cover the channel wiring and event emission that the flow uses.

func setupConfigDir(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", root)
	t.Setenv("HOME", root)
	t.Setenv("AppData", root)

	base, err := os.UserConfigDir()
	if err != nil {
		t.Fatalf("UserConfigDir: %v", err)
	}
	tdriveDir := filepath.Join(base, "TDrive")
	if err := os.MkdirAll(tdriveDir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	return tdriveDir
}
