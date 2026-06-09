package auth

import (
	"context"
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

func TestSubmitCodeUnblocksWaitCode(t *testing.T) {
	svc := NewService(nil)
	svc.resetAttempt(stageStarted)
	done := make(chan string, 1)
	go func() {
		code, _ := svc.WaitCode(context.Background())
		done <- code
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

func TestCodeRejectedEmitsInvalidEvent(t *testing.T) {
	events := &fakeEvents{}
	svc := NewService(events)

	svc.CodeRejected()

	if len(events.events) != 1 || events.events[0].name != "login-code-invalid" {
		t.Fatalf("events = %+v, want one login-code-invalid", events.events)
	}
}

func TestCodeRetryDeliversNewCodeAfterRejection(t *testing.T) {
	events := &fakeEvents{}
	svc := NewService(events)
	svc.resetAttempt(stageStarted)

	// The flow asks for a code; the user submits a wrong one.
	first := make(chan string, 1)
	go func() {
		code, _ := svc.WaitCode(context.Background())
		first <- code
	}()
	svc.SubmitCode("11111")
	if got := <-first; got != "11111" {
		t.Fatalf("first code = %q, want 11111", got)
	}

	// Telegram rejects it; the flow loops back and waits for another code.
	svc.CodeRejected()

	second := make(chan string, 1)
	go func() {
		code, _ := svc.WaitCode(context.Background())
		second <- code
	}()
	svc.SubmitCode("22222")
	select {
	case got := <-second:
		if got != "22222" {
			t.Fatalf("retry code = %q, want 22222", got)
		}
	case <-time.After(time.Second):
		t.Fatal("retry code was not accepted after rejection")
	}

	invalid := 0
	for _, e := range events.events {
		if e.name == "login-code-invalid" {
			invalid++
		}
	}
	if invalid != 1 {
		t.Fatalf("login-code-invalid count = %d, want 1", invalid)
	}
}

func TestSubmitPasswordWithoutPasswordRequestDoesNotBlock(t *testing.T) {
	events := &fakeEvents{}
	svc := NewService(events)
	done := make(chan struct{}, 1)
	go func() {
		svc.SubmitPassword("secret")
		done <- struct{}{}
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("SubmitPassword blocked without password request")
	}
	if len(events.events) != 1 || events.events[0].name != "login-error" {
		t.Fatalf("events = %+v, want login-error", events.events)
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
