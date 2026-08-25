package main

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"TDrive/backend/mountpolicy"
	encservice "TDrive/backend/services/encryption"
)

func TestAppEncryptionSetupFailsClosedWhenPolicyRefreshFails(t *testing.T) {
	detail := errors.New("telegram history unavailable")
	app, _, telegram := setupEncryptionAppWithPolicyRefresh(t, func(context.Context, int64) error {
		return detail
	})
	t.Cleanup(app.engine.Close)

	if _, err := app.EncryptionStatus(); !errors.Is(err, mountpolicy.ErrEncryptionPolicyUnavailable) || errors.Is(err, detail) {
		t.Fatalf("EncryptionStatus() error = %v, want sanitized unavailable", err)
	}
	if err := app.CreateEncryptionPassword("password-1", "hint"); !errors.Is(err, mountpolicy.ErrEncryptionPolicyUnavailable) || errors.Is(err, detail) {
		t.Fatalf("CreateEncryptionPassword() error = %v, want sanitized unavailable", err)
	}
	if len(telegram.SentControls()) != 0 {
		t.Fatalf("password setup sent %d controls with unknown policy", len(telegram.SentControls()))
	}
	if _, ok := app.engine.EncryptionService().LoadedMasterKey(); ok {
		t.Fatal("failed password setup retained a generated master key")
	}
}

func TestAppEncryptionSetupHonorsConfigRestoredByPolicyRefresh(t *testing.T) {
	var db *sql.DB
	refreshCalls := 0
	app, resolvedDB, telegram := setupEncryptionAppWithPolicyRefresh(t, func(context.Context, int64) error {
		refreshCalls++
		seedAppEncryptionReplay(t, db, testEncryptionChannelID)
		return nil
	})
	db = resolvedDB
	t.Cleanup(app.engine.Close)

	status, err := app.EncryptionStatus()
	if err != nil {
		t.Fatalf("EncryptionStatus() error = %v", err)
	}
	if !status.PasswordSet || status.PasswordRemembered {
		t.Fatalf("EncryptionStatus() = %#v, want locked configured vault", status)
	}
	if err := app.CreateEncryptionPassword("password-1", "hint"); !errors.Is(err, encservice.ErrPasswordAlreadySet) {
		t.Fatalf("CreateEncryptionPassword() error = %v, want already set", err)
	}
	if refreshCalls != 1 || len(telegram.SentControls()) != 0 {
		t.Fatalf("refresh calls = %d, sent controls = %d", refreshCalls, len(telegram.SentControls()))
	}
}

func TestAppEncryptionSetupCreatesOnlyAfterAuthoritativePlaintextPolicy(t *testing.T) {
	refreshCalls := 0
	app, _, telegram := setupEncryptionAppWithPolicyRefresh(t, func(context.Context, int64) error {
		refreshCalls++
		return nil
	})
	t.Cleanup(app.engine.Close)

	if err := app.CreateEncryptionPassword("password-1", "hint"); err != nil {
		t.Fatalf("CreateEncryptionPassword() error = %v", err)
	}
	if refreshCalls != 1 || len(telegram.SentControls()) != 1 {
		t.Fatalf("refresh calls = %d, sent controls = %d", refreshCalls, len(telegram.SentControls()))
	}
}
