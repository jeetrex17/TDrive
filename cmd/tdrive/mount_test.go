package main

import (
	"io"
	"os"
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
			name:         "options can be reordered",
			args:         []string{"--windows-drive", "s:", "--drive", "42"},
			wantSelector: "42",
			wantDrive:    "S:",
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
			selector, drive, err := parseMountStartArgs(test.args)
			if test.wantErr {
				if err == nil {
					t.Fatalf("parseMountStartArgs(%q) error = nil", test.args)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseMountStartArgs(%q): %v", test.args, err)
			}
			if selector != test.wantSelector || drive != test.wantDrive {
				t.Fatalf(
					"parseMountStartArgs(%q) = (%q, %q), want (%q, %q)",
					test.args,
					selector,
					drive,
					test.wantSelector,
					test.wantDrive,
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

func TestPrintMountResponse(t *testing.T) {
	stopped := captureMountOutput(t, func() {
		printMountResponse(daemon.MountResponse{Error: "WebDAV server stopped unexpectedly"})
	})
	if !strings.Contains(stopped, "mount: stopped") || !strings.Contains(stopped, "error: WebDAV server stopped unexpectedly") {
		t.Fatalf("stopped output = %q", stopped)
	}

	running := captureMountOutput(t, func() {
		printMountResponse(daemon.MountResponse{
			Running:      true,
			Mode:         "read-only",
			URL:          "http://127.0.0.1:7000/capability/",
			Drive:        daemon.Drive{ID: 42, Title: "Pinned"},
			WindowsDrive: "T:",
			Commands: daemon.MountCommands{
				ActiveOSMount: "active hint",
				WindowsMap:    "windows hint",
				MacFinder:     "finder hint",
				LinuxMount:    "linux hint",
			},
		})
	})
	for _, want := range []string{
		"mount: running (read-only)",
		"drive: Pinned (42), pinned until stopped",
		"url:   http://127.0.0.1:7000/capability/",
		"letter: T:",
		"run:   active hint",
		"win:   windows hint",
		"mac:   finder hint",
		"linux: linux hint",
	} {
		if !strings.Contains(running, want) {
			t.Fatalf("running output %q does not contain %q", running, want)
		}
	}
}

func captureMountOutput(t *testing.T, run func()) string {
	t.Helper()
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	previous := os.Stdout
	os.Stdout = writer
	defer func() { os.Stdout = previous }()

	run()
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	output, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	if err := reader.Close(); err != nil {
		t.Fatal(err)
	}
	return string(output)
}
