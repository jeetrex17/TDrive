package main

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"testing"

	"TDrive/backend/daemon"
	"TDrive/backend/mountcontroller"
)

func TestParseMountStartArgs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		args         []string
		wantSelector string
		wantDrive    string
		wantMode     string
		wantStdin    bool
		wantErr      bool
	}{
		{name: "server applies default", wantDrive: ""},
		{
			name:         "selected drive and normalized letter",
			args:         []string{"--drive", "  Team Drive  ", "--windows-drive", "q"},
			wantSelector: "Team Drive",
			wantDrive:    "Q:",
		},
		{
			name:         "options can be reordered with read-only override",
			args:         []string{"--windows-drive", "s:", "--read-only", "--drive", "42"},
			wantSelector: "42",
			wantDrive:    "S:",
			wantMode:     "read-only",
		},
		{name: "password from stdin", args: []string{"--password-stdin"}, wantStdin: true},
		{name: "duplicate password flag", args: []string{"--password-stdin", "--password-stdin"}, wantErr: true},
		{name: "missing selected drive", args: []string{"--drive"}, wantErr: true},
		{name: "option is not drive", args: []string{"--drive", "--password-stdin"}, wantErr: true},
		{name: "missing Windows letter", args: []string{"--windows-drive"}, wantErr: true},
		{name: "invalid Windows path", args: []string{"--windows-drive", `T:\`}, wantErr: true},
		{name: "invalid multi-letter drive", args: []string{"--windows-drive", "TT:"}, wantErr: true},
		{name: "unknown option", args: []string{"--read-write"}, wantErr: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			options, err := parseMountStartArgs(test.args)
			if test.wantErr {
				if err == nil {
					t.Fatalf("parseMountStartArgs(%q) error = nil", test.args)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseMountStartArgs(%q): %v", test.args, err)
			}
			if options.selector != test.wantSelector || options.windowsDrive != test.wantDrive || options.mode != test.wantMode || options.passwordStdin != test.wantStdin {
				t.Fatalf(
					"parseMountStartArgs(%q) = %+v, want (%q, %q, %q, %v)",
					test.args,
					options,
					test.wantSelector,
					test.wantDrive,
					test.wantMode,
					test.wantStdin,
				)
			}
		})
	}
}

func TestMountStartWithUnlockRetriesLockedDriveOnce(t *testing.T) {
	t.Parallel()
	client := &fakeMountStartClient{startErrors: []error{mountcontroller.ErrEncryptionPasswordRequired, nil}}
	reads := 0
	out, err := mountStartWithUnlock(client, mountStartOptions{selector: "42"}, false, func() (string, error) {
		reads++
		return "secret", nil
	})
	if err != nil {
		t.Fatalf("mountStartWithUnlock() error = %v", err)
	}
	if !out.Mounted || client.starts != 2 || client.unlocks != 1 || client.password != "secret" || reads != 1 {
		t.Fatalf("out=%+v starts=%d unlocks=%d password=%q reads=%d", out, client.starts, client.unlocks, client.password, reads)
	}
}

func TestMountStartWithUnlockDoesNotPromptOnUnrelatedFailure(t *testing.T) {
	t.Parallel()
	want := errors.New("mount device unavailable")
	client := &fakeMountStartClient{startErrors: []error{want}}
	_, err := mountStartWithUnlock(client, mountStartOptions{}, false, func() (string, error) {
		t.Fatal("password prompt on unrelated failure")
		return "", nil
	})
	if !errors.Is(err, want) || client.unlocks != 0 {
		t.Fatalf("error=%v unlocks=%d, want original error and no unlock", err, client.unlocks)
	}
}

func TestMountStartWithUnlockNonInteractiveFailsBeforePrompt(t *testing.T) {
	t.Parallel()
	client := &fakeMountStartClient{startErrors: []error{mountcontroller.ErrEncryptionPasswordRequired}}
	_, err := mountStartWithUnlock(client, mountStartOptions{}, true, func() (string, error) {
		t.Fatal("password prompt in non-interactive mode")
		return "", nil
	})
	if err == nil || client.unlocks != 0 {
		t.Fatalf("error=%v unlocks=%d, want interaction required and no unlock", err, client.unlocks)
	}
}

func TestMountStartWithUnlockAcceptsExplicitStdinNonInteractive(t *testing.T) {
	t.Parallel()
	client := &fakeMountStartClient{startErrors: []error{mountcontroller.ErrEncryptionPasswordRequired}}
	_, err := mountStartWithUnlock(client, mountStartOptions{passwordStdin: true}, true, func() (string, error) {
		return "secret", nil
	})
	if err != nil || client.starts != 2 || client.unlocks != 1 {
		t.Fatalf("error=%v starts=%d unlocks=%d, want successful retry", err, client.starts, client.unlocks)
	}
}

func TestMountStartWithUnlockStopsAfterUnlockFailure(t *testing.T) {
	t.Parallel()
	want := errors.New("incorrect password")
	client := &fakeMountStartClient{startErrors: []error{mountcontroller.ErrEncryptionPasswordRequired}, unlockError: want}
	_, err := mountStartWithUnlock(client, mountStartOptions{}, false, func() (string, error) {
		return "wrong", nil
	})
	if !errors.Is(err, want) || client.starts != 1 || client.unlocks != 1 {
		t.Fatalf("error=%v starts=%d unlocks=%d, want unlock error and no retry", err, client.starts, client.unlocks)
	}
}

func TestMountNeedsPasswordRecognizesRemoteCode(t *testing.T) {
	t.Parallel()
	err := fmt.Errorf("remote mount: %w", &daemon.RemoteError{Code: "encryption_password_required", Message: "locked"})
	if !mountNeedsPassword(err) {
		t.Fatalf("mountNeedsPassword(%v) = false", err)
	}
	if mountNeedsPassword(&daemon.RemoteError{Code: "permission_denied", Message: "locked"}) {
		t.Fatal("mountNeedsPassword() treated an unrelated remote code as a password prompt")
	}
}

type fakeMountStartClient struct {
	startErrors []error
	starts      int
	unlocks     int
	password    string
	unlockError error
}

func (f *fakeMountStartClient) MountStart(_, _, _ string) (daemon.MountResponse, error) {
	index := f.starts
	f.starts++
	if index < len(f.startErrors) && f.startErrors[index] != nil {
		return daemon.MountResponse{}, f.startErrors[index]
	}
	return daemon.MountResponse{Mounted: true}, nil
}

func (f *fakeMountStartClient) VaultUnlock(password string) (daemon.VaultResponse, error) {
	f.unlocks++
	f.password = password
	return daemon.VaultResponse{}, f.unlockError
}

func TestRunMountRejectsInvalidCommandsBeforeConnecting(t *testing.T) {
	t.Parallel()

	tests := [][]string{
		{"status", "extra"},
		{"stop", "extra"},
		{"unknown"},
		{"start", "--read-write"},
	}
	for _, args := range tests {
		if err := runMount(args); err == nil {
			t.Fatalf("runMount(%q) error = nil", args)
		}
	}
}

func TestPrintMountResponseIsConciseAndCapabilityFree(t *testing.T) {
	t.Parallel()

	response := daemon.MountResponse{
		Running:  true,
		Mounted:  true,
		Mode:     "read-only",
		Label:    "Tdrive personal",
		Location: "/Users/test/Library/Caches/TDrive/mounts/personal",
		Drive:    daemon.Drive{ID: 42, Title: "Private Drive"},
	}

	var output bytes.Buffer
	printMountResponse(&output, response)
	text := output.String()
	for _, forbidden := range []string{"tdrive-", "127.0.0.1", "http://", "net use"} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("output leaked %q: %q", forbidden, text)
		}
	}
	for _, expected := range []string{
		"mounted: Tdrive personal (read-only)",
		"location: /Users/test/Library/Caches/TDrive/mounts/personal",
		"drive: Private Drive (42), pinned until disconnected",
	} {
		if !strings.Contains(text, expected) {
			t.Fatalf("output %q does not contain %q", text, expected)
		}
	}
}

func TestSafeMountMessageRedactsCapabilities(t *testing.T) {
	t.Parallel()

	secret := "http://127.0.0.1:49152/tdrive-0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef/"
	if got := safeMountMessage("attach failed for " + secret); strings.Contains(got, secret) || strings.Contains(got, "tdrive-") {
		t.Fatalf("safeMountMessage leaked capability: %q", got)
	}
	if got := safeMountMessage("attach failed for HTTP://LOCALHOST:49152/TDRIVE-secret"); strings.Contains(strings.ToLower(got), "tdrive-") {
		t.Fatalf("safeMountMessage leaked case-varied capability: %q", got)
	}
}

func TestPrintMountResponseReportsStoppedAndSanitizedError(t *testing.T) {
	t.Parallel()

	var output bytes.Buffer
	printMountResponse(&output, daemon.MountResponse{
		Phase: "failed",
		Error: "Could not attach the drive",
	})

	if got, want := output.String(), "mount: stopped\nerror: Could not attach the drive\n"; got != want {
		t.Fatalf("output = %q, want %q", got, want)
	}
}

func TestPrintMountResponseReportsLifecycleWithoutCallingItStopped(t *testing.T) {
	t.Parallel()

	var mounting bytes.Buffer
	printMountResponse(&mounting, daemon.MountResponse{
		Running: true,
		Phase:   "attaching",
		Mode:    "read-only",
		Label:   "Tdrive personal",
	})
	if got, want := mounting.String(), "mount: mounting Tdrive personal (read-only)\n"; got != want {
		t.Fatalf("mounting output = %q, want %q", got, want)
	}

	secret := "http://127.0.0.1:49152/tdrive-0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef/"
	var stale bytes.Buffer
	printMountResponse(&stale, daemon.MountResponse{
		Mounted:  true,
		Phase:    "failed",
		Mode:     "read-only",
		Label:    "Tdrive personal",
		Location: secret,
		Error:    "Disconnect failed for " + secret,
	})
	text := stale.String()
	if !strings.Contains(text, "mounted: Tdrive personal (read-only)") || !strings.Contains(text, "error: Mount operation failed") {
		t.Fatalf("stale mount output = %q", text)
	}
	if strings.Contains(text, secret) || strings.Contains(text, "127.0.0.1") || strings.Contains(text, "tdrive-") {
		t.Fatalf("stale mount output leaked capability: %q", text)
	}
}

func TestPrintMountResponseReportsWritableDrainHonestly(t *testing.T) {
	t.Parallel()

	var output bytes.Buffer
	printMountResponse(&output, daemon.MountResponse{
		Running:      true,
		Mounted:      true,
		Phase:        "draining",
		Mode:         "read-write",
		WriteState:   "draining",
		ActiveWrites: 2,
		Label:        "Tdrive personal",
	})
	got := output.String()
	if !strings.Contains(got, "mount: finishing 2 active writes before ejecting Tdrive personal") {
		t.Fatalf("draining output = %q", got)
	}
	if strings.Contains(got, "stopped") || strings.Contains(got, "read-only") {
		t.Fatalf("draining output was misleading: %q", got)
	}
}

func TestPrintMountResponseReportsPausedWritesAfterFailedDrain(t *testing.T) {
	t.Parallel()

	var output bytes.Buffer
	printMountResponse(&output, daemon.MountResponse{
		Running:         true,
		Mounted:         true,
		Phase:           "failed",
		Mode:            "read-write",
		WriteState:      "draining",
		AcceptingWrites: false,
		Label:           "Tdrive personal",
		Error:           "TDrive could not finish pending changes; the drive remains mounted",
	})
	got := output.String()
	if !strings.Contains(got, "writes: paused") {
		t.Fatalf("failed drain output = %q", got)
	}
	if strings.Contains(got, "writes: ready") {
		t.Fatalf("failed drain output was misleading: %q", got)
	}
}
