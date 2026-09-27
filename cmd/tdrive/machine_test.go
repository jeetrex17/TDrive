package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"TDrive/backend/daemon"
)

func TestWriteMachineSuccess(t *testing.T) {
	var output bytes.Buffer
	if err := writeMachineSuccess(&output, "ls", map[string]any{"entries": []string{"a\n\"b"}}); err != nil {
		t.Fatal(err)
	}
	var result struct {
		SchemaVersion int             `json:"schema_version"`
		OK            bool            `json:"ok"`
		Command       string          `json:"command"`
		Data          json.RawMessage `json:"data"`
	}
	decoder := json.NewDecoder(&output)
	if err := decoder.Decode(&result); err != nil {
		t.Fatal(err)
	}
	if result.SchemaVersion != 1 || !result.OK || result.Command != "ls" || !strings.Contains(string(result.Data), `a\n\"b`) {
		t.Fatalf("unexpected envelope: %+v", result)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		t.Fatalf("expected exactly one JSON value, got %v", err)
	}
}

func TestMachineArgsEndOfOptions(t *testing.T) {
	got, err := parseMachineArgs([]string{"--parents", "--", "--folder"}, "--parents")
	if err != nil {
		t.Fatal(err)
	}
	if !got.flags["--parents"] || len(got.positional) != 1 || got.positional[0] != "--folder" {
		t.Fatalf("unexpected parse: %+v", got)
	}
}

func TestMachineArgsRejectUnknown(t *testing.T) {
	_, err := parseMachineArgs([]string{"--oops"}, "--parents")
	if err == nil || !strings.Contains(err.Error(), "--oops") {
		t.Fatalf("expected unknown option error, got %v", err)
	}
}

func TestMachineRejectsUnsupportedSubcommandBeforeDaemonStart(t *testing.T) {
	t.Parallel()
	for _, args := range [][]string{
		{"mount", "start"},
		{"vault", "rotate"},
		{"drive", "use", "42"},
		{"status", "extra"},
	} {
		if err := validateMachineCommand(args); err == nil {
			t.Errorf("validateMachineCommand(%q) accepted unsupported command", args)
		}
	}
	if err := validateMachineCommand([]string{"mount", "status"}); err != nil {
		t.Fatal(err)
	}
}

func TestMachineRemotePath(t *testing.T) {
	for _, path := range []string{"/", "/Photos/2026", "/--strange"} {
		if err := requireAbsoluteRemote(path); err != nil {
			t.Errorf("%q: %v", path, err)
		}
	}
	for _, path := range []string{"", ".", "Photos", "~/Photos", "/..", "/Photos/../", "/a//b", "/file\x00name", "\xff"} {
		if err := requireAbsoluteRemote(path); err == nil {
			t.Errorf("%q: expected rejection", path)
		}
	}
}

type recordingMachineClient struct {
	*daemon.Client
	driveID  int64
	target   string
	selector string
}

func (client *recordingMachineClient) ListInDrive(driveID int64, target string) (daemon.ListResponse, error) {
	client.driveID = driveID
	client.target = target
	return daemon.ListResponse{Path: target}, nil
}

func (client *recordingMachineClient) Sync(selector string) (daemon.MaintenanceResponse, error) {
	client.selector = selector
	return daemon.MaintenanceResponse{}, nil
}

func (client *recordingMachineClient) MountStatus() (daemon.MountResponse, error) {
	return daemon.MountResponse{
		Location: "http://localhost:1234/tdrive-token",
		Error:    "failed at http://localhost:1234/tdrive-token",
	}, nil
}

func TestMachineListUsesExplicitDrive(t *testing.T) {
	client := &recordingMachineClient{}
	data, err := machineFilesystem(client, []string{"ls", "/Photos"}, cliOptions{DriveID: 42})
	if err != nil {
		t.Fatal(err)
	}
	if client.driveID != 42 || client.target != "/Photos" {
		t.Fatalf("RPC scope = (%d, %q)", client.driveID, client.target)
	}
	if data.(daemon.ListResponse).Path != "/Photos" {
		t.Fatalf("unexpected result: %+v", data)
	}
}

func TestMachineRemoveNeedsConfirmation(t *testing.T) {
	_, err := machineFilesystem(&recordingMachineClient{}, []string{"rm", "/Photos"}, cliOptions{DriveID: 42})
	var cliErr *CLIError
	if !errors.As(err, &cliErr) || cliErr.Code != "confirmation_required" {
		t.Fatalf("expected confirmation error, got %v", err)
	}
}

func TestMachineFindRejectsExcessiveLimit(t *testing.T) {
	_, err := machineFilesystem(&recordingMachineClient{}, []string{"find", "--limit", "1001", "query"}, cliOptions{DriveID: 42})
	if err == nil || !strings.Contains(err.Error(), "--limit") {
		t.Fatalf("expected limit validation error, got %v", err)
	}
}

func TestMachinePutRejectsDirectory(t *testing.T) {
	_, err := machineTransfer(&recordingMachineClient{}, []string{"put", t.TempDir(), "/Backup"}, cliOptions{DriveID: 42})
	if err == nil || !strings.Contains(err.Error(), "regular files") {
		t.Fatalf("expected directory rejection, got %v", err)
	}
}

func TestMachineDownloadPrechecksExistingTarget(t *testing.T) {
	target := filepath.Join(t.TempDir(), "existing")
	if err := os.WriteFile(target, []byte("do not replace"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := machineTransfer(&recordingMachineClient{}, []string{"get", "/file", target}, cliOptions{DriveID: 42})
	var cliErr *CLIError
	if !errors.As(err, &cliErr) || cliErr.Code != "conflict" {
		t.Fatalf("expected no-clobber error, got %v", err)
	}
	contents, err := os.ReadFile(target)
	if err != nil || string(contents) != "do not replace" {
		t.Fatalf("existing target changed: %q, %v", contents, err)
	}
}

func TestMachineSyncUsesExplicitDrive(t *testing.T) {
	client := &recordingMachineClient{}
	if _, err := machineMaintenance(client, []string{"sync"}, cliOptions{DriveID: 42}); err != nil {
		t.Fatal(err)
	}
	if client.selector != "42" {
		t.Fatalf("selector = %q", client.selector)
	}
	if _, err := machineMaintenance(client, []string{"sync", "43"}, cliOptions{DriveID: 42}); err == nil {
		t.Fatal("expected conflicting drive IDs to be rejected")
	}
}

func TestMachineMountStatusRedactsEndpoint(t *testing.T) {
	data, err := machineResult(&recordingMachineClient{}, []string{"mount", "status"}, cliOptions{})
	if err != nil {
		t.Fatal(err)
	}
	out := data.(daemon.MountResponse)
	if out.Location != "" || strings.Contains(out.Error, "localhost") || out.Error == "" {
		t.Fatalf("mount data leaked endpoint: %+v", out)
	}
}

func TestMachineResultRedactsInviteCapabilities(t *testing.T) {
	t.Parallel()
	secret := "https://t.me/+private-invite"
	drive := daemon.Drive{ID: 42, Title: "Team", InviteLink: secret}
	cases := []any{
		daemon.DriveListResponse{Drives: []daemon.Drive{drive}},
		daemon.PendingJoinsResponse{Pending: []daemon.PendingJoin{{InviteLink: secret, InviteHash: "private-invite"}}},
		daemon.PathResponse{Drive: drive},
		daemon.ListResponse{Drive: drive},
		daemon.FindResponse{Drive: drive},
		daemon.EntryResponse{Drive: drive},
		daemon.UploadResponse{Drive: drive},
		daemon.DownloadResponse{Drive: drive},
		daemon.MaintenanceResponse{Drive: drive},
		daemon.MountResponse{Drive: drive, Location: secret},
	}
	for _, input := range cases {
		redacted, err := redactMachineResult(input)
		if err != nil {
			t.Fatalf("redact %T: %v", input, err)
		}
		encoded, err := json.Marshal(redacted)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(encoded), secret) || strings.Contains(string(encoded), "private-invite") {
			t.Fatalf("%T leaked invite capability: %s", input, encoded)
		}
	}
	if drive.InviteLink != secret {
		t.Fatal("redaction mutated input drive")
	}
	if _, err := redactMachineResult(struct{ Secret string }{Secret: secret}); err == nil {
		t.Fatal("unexpected result type should fail closed")
	}
}
