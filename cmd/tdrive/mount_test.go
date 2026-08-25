package main

import (
	"bytes"
	"strings"
	"testing"

	"TDrive/backend/daemon"
)

func TestParseMountStartArgs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		args         []string
		wantSelector string
		wantDrive    string
		wantMode     string
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
		{name: "missing selected drive", args: []string{"--drive"}, wantErr: true},
		{name: "missing Windows letter", args: []string{"--windows-drive"}, wantErr: true},
		{name: "invalid Windows path", args: []string{"--windows-drive", `T:\`}, wantErr: true},
		{name: "invalid multi-letter drive", args: []string{"--windows-drive", "TT:"}, wantErr: true},
		{name: "unknown option", args: []string{"--read-write"}, wantErr: true},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			selector, drive, mode, err := parseMountStartArgs(test.args)
			if test.wantErr {
				if err == nil {
					t.Fatalf("parseMountStartArgs(%q) error = nil", test.args)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseMountStartArgs(%q): %v", test.args, err)
			}
			if selector != test.wantSelector || drive != test.wantDrive || mode != test.wantMode {
				t.Fatalf(
					"parseMountStartArgs(%q) = (%q, %q, %q), want (%q, %q, %q)",
					test.args,
					selector,
					drive,
					mode,
					test.wantSelector,
					test.wantDrive,
					test.wantMode,
				)
			}
		})
	}
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
