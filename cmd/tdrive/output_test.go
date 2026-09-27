package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"TDrive/backend/daemon"
)

func TestTerminalSafeText(t *testing.T) {
	t.Parallel()
	input := "Team\x1b[31m\nFolder\u202e.txt"
	got := terminalSafeText(input)
	if strings.ContainsAny(got, "\x1b\n\r") || strings.ContainsRune(got, '\u202e') {
		t.Fatalf("unsafe terminal output %q", got)
	}
	if !strings.Contains(got, "Team") || !strings.Contains(got, "Folder") {
		t.Fatalf("text content lost: %q", got)
	}
}

func TestClassifyDaemonUnavailable(t *testing.T) {
	t.Parallel()
	classified, exitCode := classifyCommandError(errors.Join(daemon.ErrDaemonUnavailable, errors.New("socket closed")))
	if classified.Code != "unavailable" || !classified.Retryable || exitCode != 6 {
		t.Fatalf("classified = %+v, exit = %d", classified, exitCode)
	}
}

func TestWriteCommandErrorJSON(t *testing.T) {
	t.Parallel()
	var out bytes.Buffer
	code := writeCommandError(&out, &daemon.RemoteError{
		Code: "encryption_password_required", Message: "mount controller: encryption password required",
	}, true)
	if code != 3 {
		t.Fatalf("exit code = %d, want 3", code)
	}
	var payload struct {
		SchemaVersion int  `json:"schema_version"`
		OK            bool `json:"ok"`
		Error         struct {
			Code      string `json:"code"`
			Message   string `json:"message"`
			Retryable bool   `json:"retryable"`
		} `json:"error"`
	}
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatalf("invalid JSON error: %v", err)
	}
	if payload.SchemaVersion != 1 || payload.OK || payload.Error.Code != "encryption_password_required" || payload.Error.Retryable {
		t.Fatalf("unexpected JSON error: %+v", payload)
	}
}

func TestWantsJSONRespectsTerminator(t *testing.T) {
	t.Parallel()
	if !wantsJSON([]string{"ls", "--output=json"}) {
		t.Fatal("output flag not detected")
	}
	if wantsJSON([]string{"ls", "--", "--json"}) {
		t.Fatal("positional --json should not select JSON output")
	}
}
