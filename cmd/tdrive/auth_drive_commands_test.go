package main

import (
	"bytes"
	"errors"
	"io"
	"os"
	"testing"

	"TDrive/backend/daemon"
)

func TestParseSetupArgsReadsSuccessivePipedAnswers(t *testing.T) {
	stdin := replaceStdin(t, "12345\nabcdef0123456789\n")
	defer stdin()

	id, hash, err := parseSetupArgs(nil)
	if err != nil {
		t.Fatalf("parseSetupArgs() error = %v", err)
	}
	if id != 12345 || hash != "abcdef0123456789" {
		t.Fatalf("parseSetupArgs() = (%d, %q), want (12345, %q)", id, hash, "abcdef0123456789")
	}
}

func TestPromptLineReturnsEOFWhenInputIsEmpty(t *testing.T) {
	stdin := replaceStdin(t, "")
	defer stdin()

	if _, err := promptLine("Answer: "); !errors.Is(err, io.EOF) {
		t.Fatalf("promptLine() error = %v, want EOF", err)
	}
}

func TestPickerUsesRemainingPipedInputAfterPrompt(t *testing.T) {
	stdin := replaceStdin(t, "+123456789\n2\n")
	defer stdin()

	if phone, err := promptLine("Phone: "); err != nil || phone != "+123456789" {
		t.Fatalf("phone = %q, error = %v", phone, err)
	}
	client := &fakePersonalDriveSetupClient{}
	var output bytes.Buffer
	_, err := choosePersonalDrive(client, personalDriveFixture(), os.Stdin, &output)
	if err != nil {
		t.Fatalf("choosePersonalDrive() error = %v", err)
	}
	if len(client.selected) != 1 || client.selected[0] != "8300" {
		t.Fatalf("selected = %v, want [8300]", client.selected)
	}
}

func TestParseSetupArgsReadsAPIHashFromStdin(t *testing.T) {
	stdin := replaceStdin(t, "abcdef0123456789\n")
	defer stdin()

	id, hash, err := parseSetupArgs([]string{"--api-id", "12345", "--api-hash-stdin"})
	if err != nil {
		t.Fatalf("parseSetupArgs() error = %v", err)
	}
	if id != 12345 || hash != "abcdef0123456789" {
		t.Fatalf("parseSetupArgs() = (%d, %q), want (12345, %q)", id, hash, "abcdef0123456789")
	}
}

func TestParseSetupArgsRejectsConflictingAPIHashSources(t *testing.T) {
	tests := [][]string{
		{"--api-id", "12345", "--api-hash", "secret", "--api-hash-stdin"},
		{"--api-id", "12345", "--api-id", "67890", "--api-hash", "secret"},
		{"--api-id", "12345", "--api-hash", "secret", "--api-hash", "other"},
		{"--api-id", "12345", "--api-hash-stdin", "--api-hash-stdin"},
	}
	for _, args := range tests {
		if _, _, err := parseSetupArgs(args); err == nil {
			t.Fatalf("parseSetupArgs(%q) accepted conflicting or duplicate options", args)
		}
	}
}

func TestParseLoginArgsExplicitDriveChoice(t *testing.T) {
	tests := []struct {
		name       string
		args       []string
		wantPhone  string
		wantID     string
		wantCreate bool
		wantErr    bool
	}{
		{name: "phone", args: []string{"+123456789"}, wantPhone: "+123456789"},
		{name: "candidate", args: []string{"--personal-drive-id", "42", "+123456789"}, wantPhone: "+123456789", wantID: "42"},
		{name: "create", args: []string{"+123456789", "--create-personal-drive"}, wantPhone: "+123456789", wantCreate: true},
		{name: "conflict", args: []string{"--personal-drive-id", "42", "--create-personal-drive"}, wantErr: true},
		{name: "missing ID", args: []string{"--personal-drive-id"}, wantErr: true},
		{name: "option is not ID", args: []string{"--personal-drive-id", "--create-personal-drive"}, wantErr: true},
		{name: "multiple phones", args: []string{"+123456789", "+987654321"}, wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := parseLoginArgs(test.args)
			if (err != nil) != test.wantErr {
				t.Fatalf("parseLoginArgs() error = %v, wantErr %v", err, test.wantErr)
			}
			if err == nil && (got.phone != test.wantPhone || got.personalDriveID != test.wantID || got.createPersonalDrive != test.wantCreate) {
				t.Fatalf("parseLoginArgs() = %+v, want phone=%q ID=%q create=%v", got, test.wantPhone, test.wantID, test.wantCreate)
			}
		})
	}
}

func TestChooseExplicitPersonalDriveRequiresOfferedCandidate(t *testing.T) {
	client := &fakePersonalDriveSetupClient{}
	setup := daemon.PersonalDriveSetup{Status: "selection_required", Candidates: []daemon.PersonalDriveCandidate{{ID: "42"}}}
	if _, err := chooseExplicitPersonalDrive(client, setup, "99", false); err == nil {
		t.Fatal("chooseExplicitPersonalDrive() selected an unoffered channel")
	}
	if len(client.selected) != 0 {
		t.Fatalf("selected = %q, want empty", client.selected)
	}
	if _, err := chooseExplicitPersonalDrive(client, setup, "42", false); err != nil {
		t.Fatal(err)
	}
	if len(client.selected) != 1 || client.selected[0] != "42" {
		t.Fatalf("selected = %q, want 42", client.selected)
	}
}

func TestDriveSelectorForCommand(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		args    []string
		driveID int64
		want    string
		wantErr bool
	}{
		{name: "active drive"},
		{name: "positional", args: []string{"Team Drive"}, want: "Team Drive"},
		{name: "explicit ID", driveID: 42, want: "42"},
		{name: "ambiguous", args: []string{"Team Drive"}, driveID: 42, wantErr: true},
		{name: "too many positional", args: []string{"a", "b"}, wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			got, err := driveSelectorForCommand(test.args, test.driveID, "usage: tdrive sync [name|id]")
			if (err != nil) != test.wantErr {
				t.Fatalf("driveSelectorForCommand() error = %v, wantErr %v", err, test.wantErr)
			}
			if err == nil && got != test.want {
				t.Fatalf("driveSelectorForCommand() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestValidateRebuildOptions(t *testing.T) {
	t.Parallel()
	if err := validateRebuildOptions(cliOptions{NonInteractive: true}); err == nil {
		t.Fatal("non-interactive rebuild without --yes was allowed")
	}
	if err := validateRebuildOptions(cliOptions{NonInteractive: true, Yes: true}); err != nil {
		t.Fatal("non-interactive rebuild with --yes was rejected")
	}
	if err := validateRebuildOptions(cliOptions{}); err != nil {
		t.Fatal("interactive rebuild was rejected")
	}
}

func replaceStdin(t *testing.T, input string) func() {
	t.Helper()
	file, err := os.CreateTemp(t.TempDir(), "stdin-*")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString(input); err != nil {
		t.Fatal(err)
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		t.Fatal(err)
	}
	old := os.Stdin
	os.Stdin = file
	return func() {
		os.Stdin = old
		_ = file.Close()
	}
}
