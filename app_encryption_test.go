package main

import (
	"bytes"
	"database/sql"
	"testing"

	"TDrive/backend"
	"TDrive/backend/auth"
	"TDrive/backend/projection"

	_ "modernc.org/sqlite"
)

const testEncryptionChannelID int64 = 424242

func setupEncryptionApp(t *testing.T) (*App, *sql.DB) {
	t.Helper()
	clearMasterKey()

	tempHome := t.TempDir()
	t.Setenv("HOME", tempHome)
	t.Setenv("XDG_CONFIG_HOME", tempHome)

	oldDB := backend.DB
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	backend.DB = db
	t.Cleanup(func() {
		clearMasterKey()
		backend.DB = oldDB
		_ = db.Close()
	})

	if err := auth.SaveConfig(testEncryptionChannelID); err != nil {
		t.Fatalf("save config: %v", err)
	}
	if err := projection.MigratePersonalChannel(db, testEncryptionChannelID); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return &App{}, db
}

func TestEncryptionPasswordStoresHint(t *testing.T) {
	app, _ := setupEncryptionApp(t)

	if err := app.CreateEncryptionPassword("old-password", "pet name"); err != nil {
		t.Fatalf("create password: %v", err)
	}

	status, err := app.EncryptionStatus()
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if !status.PasswordSet || !status.PasswordRemembered {
		t.Fatalf("status = %+v, want password set and remembered", status)
	}
	if status.Hint != "pet name" {
		t.Fatalf("hint = %q", status.Hint)
	}

	clearMasterKey()
	status, err = app.EncryptionStatus()
	if err != nil {
		t.Fatalf("status after clear: %v", err)
	}
	if !status.PasswordSet || status.PasswordRemembered || status.Hint != "pet name" {
		t.Fatalf("status after clear = %+v", status)
	}
	if err := app.UseEncryptionPassword("old-password"); err != nil {
		t.Fatalf("use password: %v", err)
	}
}

func TestChangeEncryptionPasswordRewrapsMasterKey(t *testing.T) {
	app, db := setupEncryptionApp(t)

	if err := app.CreateEncryptionPassword("old-password", "old hint"); err != nil {
		t.Fatalf("create password: %v", err)
	}
	before, err := projection.GetEncryptionConfig(db, testEncryptionChannelID)
	if err != nil {
		t.Fatalf("get before config: %v", err)
	}
	beforeMaster, err := unwrapEncryptionMasterKey(before, "old-password")
	if err != nil {
		t.Fatalf("unwrap before: %v", err)
	}

	if err := app.ChangeEncryptionPassword("bad-password", "new-password", "new hint"); err == nil {
		t.Fatalf("wrong current password unexpectedly succeeded")
	}
	if err := app.UseEncryptionPassword("old-password"); err != nil {
		t.Fatalf("old password should still work after failed change: %v", err)
	}

	if err := app.ChangeEncryptionPassword("old-password", "new-password", "new hint"); err != nil {
		t.Fatalf("change password: %v", err)
	}

	clearMasterKey()
	if err := app.UseEncryptionPassword("old-password"); err == nil {
		t.Fatalf("old password unexpectedly worked after change")
	}
	if err := app.UseEncryptionPassword("new-password"); err != nil {
		t.Fatalf("new password failed: %v", err)
	}

	after, err := projection.GetEncryptionConfig(db, testEncryptionChannelID)
	if err != nil {
		t.Fatalf("get after config: %v", err)
	}
	afterMaster, err := unwrapEncryptionMasterKey(after, "new-password")
	if err != nil {
		t.Fatalf("unwrap after: %v", err)
	}
	if !bytes.Equal(beforeMaster, afterMaster) {
		t.Fatalf("master key changed during password change")
	}
	if after.Hint != "new hint" {
		t.Fatalf("hint after change = %q", after.Hint)
	}
}
