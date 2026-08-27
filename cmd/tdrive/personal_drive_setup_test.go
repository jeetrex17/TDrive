package main

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"

	"TDrive/backend/daemon"
)

type fakePersonalDriveSetupClient struct {
	selected []string
	created  int
}

func (f *fakePersonalDriveSetupClient) SelectPersonalDrive(channelID string) (daemon.PersonalDriveSetup, error) {
	f.selected = append(f.selected, channelID)
	return daemon.PersonalDriveSetup{Status: "ready", ActiveChannelID: channelID}, nil
}

func (f *fakePersonalDriveSetupClient) CreatePersonalDrive() (daemon.PersonalDriveSetup, error) {
	f.created++
	return daemon.PersonalDriveSetup{Status: "ready", ActiveChannelID: "9000"}, nil
}

func personalDriveFixture() daemon.PersonalDriveSetup {
	return daemon.PersonalDriveSetup{
		Status: "selection_required",
		Candidates: []daemon.PersonalDriveCandidate{
			{ID: "8200", Title: "TDrive", HasActivity: true, Recommended: true},
			{ID: "8300", Title: "Archive"},
		},
	}
}

func TestChoosePersonalDriveSelectsByDisplayedNumber(t *testing.T) {
	client := &fakePersonalDriveSetupClient{}
	var output bytes.Buffer
	result, err := choosePersonalDrive(client, personalDriveFixture(), strings.NewReader("2\n"), &output)
	if err != nil {
		t.Fatalf("choosePersonalDrive: %v", err)
	}
	if len(client.selected) != 1 || client.selected[0] != "8300" || client.created != 0 {
		t.Fatalf("selected=%v created=%d", client.selected, client.created)
	}
	if result.ActiveChannelID != "8300" {
		t.Fatalf("result = %#v", result)
	}
	if !strings.Contains(output.String(), "Recommended") || !strings.Contains(output.String(), "Create New TDrive") {
		t.Fatalf("menu output = %q", output.String())
	}
}

func TestChoosePersonalDriveNeverAcceptsRawChannelID(t *testing.T) {
	client := &fakePersonalDriveSetupClient{}
	_, err := choosePersonalDrive(client, personalDriveFixture(), strings.NewReader("8300\n"), io.Discard)
	if !errors.Is(err, io.EOF) {
		t.Fatalf("error = %v, want EOF after invalid menu choice", err)
	}
	if len(client.selected) != 0 || client.created != 0 {
		t.Fatalf("raw id changed state: selected=%v created=%d", client.selected, client.created)
	}
}

func TestChoosePersonalDriveRequiresCreateConfirmation(t *testing.T) {
	t.Run("confirmed", func(t *testing.T) {
		client := &fakePersonalDriveSetupClient{}
		result, err := choosePersonalDrive(client, personalDriveFixture(), strings.NewReader("c\ny\n"), io.Discard)
		if err != nil {
			t.Fatalf("choosePersonalDrive: %v", err)
		}
		if client.created != 1 || result.ActiveChannelID != "9000" {
			t.Fatalf("created=%d result=%#v", client.created, result)
		}
	})

	t.Run("eof creates nothing", func(t *testing.T) {
		client := &fakePersonalDriveSetupClient{}
		_, err := choosePersonalDrive(client, personalDriveFixture(), strings.NewReader("c\n"), io.Discard)
		if !errors.Is(err, io.EOF) {
			t.Fatalf("error = %v, want EOF", err)
		}
		if client.created != 0 {
			t.Fatalf("created=%d, want 0", client.created)
		}
	})
}

func TestTerminalSafeTitleStripsControlCharacters(t *testing.T) {
	got := terminalSafeTitle("\x1b[31mEvil\n\tName\x07")
	if strings.ContainsAny(got, "\x1b\n\t\x07") || got != "[31mEvil Name" {
		t.Fatalf("terminalSafeTitle = %q", got)
	}
}
