package main

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"
	"time"
)

func TestParseCLIOptions(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		args    []string
		want    cliOptions
		command []string
		wantErr bool
	}{
		{
			name:    "flags before and after command",
			args:    []string{"--json", "ls", "--drive-id", "42", "/Photos", "--timeout=2m"},
			want:    cliOptions{JSON: true, DriveID: 42, Timeout: 2 * time.Minute},
			command: []string{"ls", "/Photos"},
		},
		{
			name:    "output and safety flags",
			args:    []string{"rm", "--output", "json", "--non-interactive", "--yes", "/Old"},
			want:    cliOptions{JSON: true, NonInteractive: true, Yes: true},
			command: []string{"rm", "/Old"},
		},
		{
			name:    "double dash preserves positional flags",
			args:    []string{"ls", "--", "--json"},
			command: []string{"ls", "--", "--json"},
		},
		{name: "invalid drive", args: []string{"ls", "--drive-id", "0"}, wantErr: true},
		{name: "missing drive", args: []string{"ls", "--drive-id"}, wantErr: true},
		{name: "invalid timeout", args: []string{"ls", "--timeout", "never"}, wantErr: true},
		{name: "unknown output", args: []string{"ls", "--output", "yaml"}, wantErr: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			got, command, err := parseCLIOptions(test.args)
			if (err != nil) != test.wantErr {
				t.Fatalf("parseCLIOptions(%q) error = %v, wantErr %v", test.args, err, test.wantErr)
			}
			if test.wantErr {
				return
			}
			if got != test.want || !reflect.DeepEqual(command, test.command) {
				t.Fatalf("parseCLIOptions(%q) = (%+v, %q), want (%+v, %q)", test.args, got, command, test.want, test.command)
			}
		})
	}
}

func TestNonInteractiveGetRejectsBrokenSymlinkWithoutConnecting(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("creating symlinks requires extra privileges on Windows")
	}
	target := filepath.Join(t.TempDir(), "destination")
	if err := os.Symlink(filepath.Join(filepath.Dir(target), "missing"), target); err != nil {
		t.Fatal(err)
	}
	opts := cliOptions{NonInteractive: true}
	previous := currentCLIOptions.Swap(&opts)
	defer currentCLIOptions.Store(previous)
	err := runGet([]string{"/remote", target})
	var cliErr *CLIError
	if !errors.As(err, &cliErr) || cliErr.Code != "confirmation_required" {
		t.Fatalf("get error = %v, want confirmation_required", err)
	}
}

func TestRequireNonInteractiveConfirmation(t *testing.T) {
	t.Parallel()
	if err := requireNonInteractiveConfirmation(cliOptions{}, "logout"); err != nil {
		t.Fatal(err)
	}
	if err := requireNonInteractiveConfirmation(cliOptions{NonInteractive: true, Yes: true}, "logout"); err != nil {
		t.Fatal(err)
	}
	if err := requireNonInteractiveConfirmation(cliOptions{NonInteractive: true}, "logout"); err == nil {
		t.Fatal("non-interactive destructive action must require --yes")
	}
}
