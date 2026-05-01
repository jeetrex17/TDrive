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

func createEncryptionPassword(t *testing.T, channelID int64, password string, hint string) {
	t.Helper()
	master := bytes.Repeat([]byte{0x11}, 32)
	cfg, err := buildEncryptionConfig(channelID, password, hint, master, 0)
	if err != nil {
		t.Fatalf("build config: %v", err)
	}
	if err := projection.PutEncryptionConfig(backend.DB, cfg); err != nil {
		t.Fatalf("put config: %v", err)
	}
	storeMasterKey(master)
}

func TestEncryptionPasswordStoresHint(t *testing.T) {
	app, _ := setupEncryptionApp(t)

	createEncryptionPassword(t, testEncryptionChannelID, "old-password", "pet name")

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
	_, db := setupEncryptionApp(t)

	createEncryptionPassword(t, testEncryptionChannelID, "old-password", "old hint")
	before, err := projection.GetEncryptionConfig(db, testEncryptionChannelID)
	if err != nil {
		t.Fatalf("get before config: %v", err)
	}
	beforeMaster, err := unwrapEncryptionMasterKey(before, "old-password")
	if err != nil {
		t.Fatalf("unwrap before: %v", err)
	}

	if _, err := unwrapEncryptionMasterKey(before, "bad-password"); err == nil {
		t.Fatalf("wrong current password unexpectedly succeeded")
	}
	if err := rememberEncryptionPassword(before, "old-password"); err != nil {
		t.Fatalf("old password should still work after failed change: %v", err)
	}

	next, err := buildEncryptionConfig(testEncryptionChannelID, "new-password", "new hint", beforeMaster, before.CreatedAt)
	if err != nil {
		t.Fatalf("build changed config: %v", err)
	}
	if err := projection.PutEncryptionConfig(db, next); err != nil {
		t.Fatalf("save changed config: %v", err)
	}

	clearMasterKey()
	after, err := projection.GetEncryptionConfig(db, testEncryptionChannelID)
	if err != nil {
		t.Fatalf("get after config: %v", err)
	}
	if err := rememberEncryptionPassword(after, "old-password"); err == nil {
		t.Fatalf("old password unexpectedly worked after change")
	}
	if err := rememberEncryptionPassword(after, "new-password"); err != nil {
		t.Fatalf("new password failed: %v", err)
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

func TestEncryptionConfigOpCarriesRecoverableVault(t *testing.T) {
	_, db := setupEncryptionApp(t)
	master := bytes.Repeat([]byte{0x42}, 32)
	cfg, err := buildEncryptionConfig(testEncryptionChannelID, "password-1", "pet name", master, 0)
	if err != nil {
		t.Fatalf("build config: %v", err)
	}

	header := projection.Format(encryptionConfigOp(cfg))
	op, err := projection.Parse(header)
	if err != nil {
		t.Fatalf("parse config op: %v", err)
	}
	if _, err := projection.ProjectFromOp(db, testEncryptionChannelID, 99, op, 1, header); err != nil {
		t.Fatalf("project config op: %v", err)
	}

	got, err := projection.GetEncryptionConfig(db, testEncryptionChannelID)
	if err != nil {
		t.Fatalf("get projected config: %v", err)
	}
	recovered, err := unwrapEncryptionMasterKey(got, "password-1")
	if err != nil {
		t.Fatalf("unwrap recovered config: %v", err)
	}
	if !bytes.Equal(recovered, master) {
		t.Fatalf("recovered master key mismatch")
	}
	if got.Hint != "pet name" {
		t.Fatalf("hint = %q", got.Hint)
	}
}
