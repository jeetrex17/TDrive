package main

import (
	"context"
	"testing"

	encservice "TDrive/backend/services/encryption"
)

type backupEncryptionStatusService struct {
	appEncryptionService
	status encservice.Status
}

func (s backupEncryptionStatusService) StatusContext(context.Context) (encservice.Status, error) {
	return s.status, nil
}

func TestPhotoBackupShowsLockedEncryptionBeforeStarting(t *testing.T) {
	app := &App{encryptionServiceOverride: backupEncryptionStatusService{status: encservice.Status{Available: true, PasswordSet: true}}}
	initial := PhotoBackupState{Settings: PhotoBackupSettings{Enabled: true, Encrypt: true}, Status: PhotoBackupStatus{Phase: "idle"}}
	locked := app.photoBackupAccessState(initial)
	if !locked.EncryptionRequired || locked.Status.Phase != "paused" || locked.Status.Message != "Unlock encryption to back up your photos and videos." {
		t.Fatalf("locked state: %+v", locked)
	}
	if initial.Status.Phase != "idle" {
		t.Fatal("changed input state")
	}
	app.encryptionServiceOverride = backupEncryptionStatusService{status: encservice.Status{Available: true, PasswordSet: true, PasswordRemembered: true}}
	unlocked := app.photoBackupAccessState(initial)
	if unlocked.EncryptionRequired || unlocked.Status.Phase != "idle" {
		t.Fatalf("unlocked state: %+v", unlocked)
	}
}

func TestPhotoBackupPreservesExplicitPauseWhenEncryptionIsLocked(t *testing.T) {
	app := &App{}
	initial := PhotoBackupState{Settings: PhotoBackupSettings{Enabled: true, Encrypt: true}, ManualPaused: true, Status: PhotoBackupStatus{Phase: "paused", Message: "Paused by you."}}
	if got := app.photoBackupAccessState(initial); got.Status.Message != initial.Status.Message || got.EncryptionRequired {
		t.Fatalf("manual pause replaced: %+v", got)
	}
}
