package main

import (
	"context"
	"database/sql"
	"testing"

	"TDrive/backend"
	"TDrive/backend/auth"
	"TDrive/backend/core"
	"TDrive/backend/projection"
	"TDrive/backend/tgclient"

	"github.com/gotd/td/telegram"
	_ "modernc.org/sqlite"
)

const testEncryptionChannelID int64 = 424242

func setupEncryptionApp(t *testing.T) (*App, *sql.DB) {
	t.Helper()
	app, db, _ := setupEncryptionAppWithPolicyRefresh(t, nil)
	return app, db
}

func setupEncryptionAppWithPolicyRefresh(t *testing.T, refresh func(context.Context, int64) error) (*App, *sql.DB, *tgclient.Fake) {
	t.Helper()

	tempHome := t.TempDir()
	t.Setenv("HOME", tempHome)
	t.Setenv("XDG_CONFIG_HOME", tempHome)

	oldDB := backend.DB
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	backend.DB = db
	t.Cleanup(func() {
		backend.DB = oldDB
		_ = db.Close()
	})

	if err := auth.SaveConfig(testEncryptionChannelID); err != nil {
		t.Fatalf("save config: %v", err)
	}
	if err := projection.MigratePersonalChannel(db, testEncryptionChannelID); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if _, err := db.Exec(`UPDATE channels SET personal_backfill_done = 1 WHERE channel_id = ?`, testEncryptionChannelID); err != nil {
		t.Fatalf("mark backfill done: %v", err)
	}

	fakeTG := tgclient.NewFake(1)
	fakeTG.SeedChannel(tgclient.InputPeer{ChannelID: testEncryptionChannelID, AccessHash: 7}, "My Drive")

	engine, err := core.New(t.Context(), core.Config{
		TG:                      fakeTG,
		SkipDBInit:              true,
		EncryptionPolicyRefresh: refresh,
		Connect: func() (nilClient *telegram.Client, err error) {
			return nil, nil
		},
	})
	if err != nil {
		t.Fatalf("new core engine: %v", err)
	}

	app := &App{engine: engine}
	return app, db, fakeTG
}

func TestEncryptionPasswordStoresHint(t *testing.T) {
	app, _ := setupEncryptionApp(t)

	if result := app.CreateEncryptionPassword("old-password", "pet name"); !result.OK {
		t.Fatalf("create password: %v", result.Error)
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

	app.clearEncryptionSession()
	status, err = app.EncryptionStatus()
	if err != nil {
		t.Fatalf("status after clear: %v", err)
	}
	if !status.PasswordSet || status.PasswordRemembered || status.Hint != "pet name" {
		t.Fatalf("status after clear = %+v", status)
	}
	if result := app.UseEncryptionPassword("old-password"); !result.OK {
		t.Fatalf("use password: %v", result.Error)
	}
}

func TestChangeEncryptionPasswordRewrapsConfig(t *testing.T) {
	app, db := setupEncryptionApp(t)

	if result := app.CreateEncryptionPassword("old-password", "old hint"); !result.OK {
		t.Fatalf("create password: %v", result.Error)
	}
	if result := app.ChangeEncryptionPassword("bad-password", "new-password", "new hint"); result.OK {
		t.Fatalf("wrong current password unexpectedly succeeded")
	}
	if result := app.UseEncryptionPassword("old-password"); !result.OK {
		t.Fatalf("old password should still work after failed change: %v", result.Error)
	}

	if result := app.ChangeEncryptionPassword("old-password", "new-password", "new hint"); !result.OK {
		t.Fatalf("change password: %v", result.Error)
	}
	after, err := projection.GetEncryptionConfig(db, testEncryptionChannelID)
	if err != nil {
		t.Fatalf("get after config: %v", err)
	}
	if after.Hint != "new hint" {
		t.Fatalf("hint after change = %q", after.Hint)
	}

	app.clearEncryptionSession()
	if result := app.UseEncryptionPassword("old-password"); result.OK {
		t.Fatalf("old password unexpectedly worked after change")
	}
	if result := app.UseEncryptionPassword("new-password"); !result.OK {
		t.Fatalf("new password failed: %v", result.Error)
	}
}
