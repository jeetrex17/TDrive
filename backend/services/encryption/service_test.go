package encryption

import (
	"bytes"
	"database/sql"
	"testing"

	"TDrive/backend/projection"

	_ "modernc.org/sqlite"
)

const testChannelID int64 = 424242

func newTestService(t *testing.T) (*Service, *sql.DB) {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := projection.MigratePersonalChannel(db, testChannelID); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	var msgID int64
	svc := NewService(Config{
		DB: db,
		PersonalChannelID: func() int64 {
			return testChannelID
		},
		EmitOp: func(channelID int64, op projection.Op) error {
			msgID++
			header := projection.Format(op)
			_, err := projection.ProjectFromOp(db, channelID, msgID, op, 1, header)
			return err
		},
	})
	return svc, db
}

func TestPasswordStatusAndRememberedKey(t *testing.T) {
	svc, _ := newTestService(t)

	status, err := svc.Status()
	if err != nil {
		t.Fatalf("status before create: %v", err)
	}
	if !status.Available || status.PasswordSet || status.PasswordRemembered {
		t.Fatalf("status before create = %+v", status)
	}

	if err := svc.CreatePassword("old-password", "pet name"); err != nil {
		t.Fatalf("create password: %v", err)
	}
	status, err = svc.Status()
	if err != nil {
		t.Fatalf("status after create: %v", err)
	}
	if !status.PasswordSet || !status.PasswordRemembered || status.Hint != "pet name" {
		t.Fatalf("status after create = %+v", status)
	}

	svc.Clear()
	status, err = svc.Status()
	if err != nil {
		t.Fatalf("status after clear: %v", err)
	}
	if !status.PasswordSet || status.PasswordRemembered {
		t.Fatalf("status after clear = %+v", status)
	}
	if err := svc.UsePassword("old-password"); err != nil {
		t.Fatalf("use password: %v", err)
	}
}

func TestChangePasswordKeepsMasterKey(t *testing.T) {
	svc, db := newTestService(t)

	if err := svc.CreatePassword("old-password", "old hint"); err != nil {
		t.Fatalf("create password: %v", err)
	}
	before, err := projection.GetEncryptionConfig(db, testChannelID)
	if err != nil {
		t.Fatalf("get before config: %v", err)
	}
	beforeMaster, err := unwrapMasterKey(before, "old-password")
	if err != nil {
		t.Fatalf("unwrap before: %v", err)
	}

	if err := svc.ChangePassword("bad-password", "new-password", "new hint"); err == nil {
		t.Fatalf("wrong current password unexpectedly succeeded")
	}
	if err := svc.UsePassword("old-password"); err != nil {
		t.Fatalf("old password should still work after failed change: %v", err)
	}

	if err := svc.ChangePassword("old-password", "new-password", "new hint"); err != nil {
		t.Fatalf("change password: %v", err)
	}
	svc.Clear()
	if err := svc.UsePassword("old-password"); err == nil {
		t.Fatalf("old password unexpectedly worked after change")
	}
	if err := svc.UsePassword("new-password"); err != nil {
		t.Fatalf("new password failed: %v", err)
	}

	after, err := projection.GetEncryptionConfig(db, testChannelID)
	if err != nil {
		t.Fatalf("get after config: %v", err)
	}
	afterMaster, err := unwrapMasterKey(after, "new-password")
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

func TestConfigOpCarriesRecoverableVault(t *testing.T) {
	_, db := newTestService(t)
	master := bytes.Repeat([]byte{0x42}, 32)
	cfg, err := buildConfig(testChannelID, "password-1", "pet name", master, 0)
	if err != nil {
		t.Fatalf("build config: %v", err)
	}

	header := projection.Format(configOp(cfg))
	op, err := projection.Parse(header)
	if err != nil {
		t.Fatalf("parse config op: %v", err)
	}
	if _, err := projection.ProjectFromOp(db, testChannelID, 99, op, 1, header); err != nil {
		t.Fatalf("project config op: %v", err)
	}

	got, err := projection.GetEncryptionConfig(db, testChannelID)
	if err != nil {
		t.Fatalf("get projected config: %v", err)
	}
	recovered, err := unwrapMasterKey(got, "password-1")
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

func TestMasterKeyGates(t *testing.T) {
	svc, _ := newTestService(t)

	if key, err := svc.MasterKeyForUpload(testChannelID, false); err != nil || key != nil {
		t.Fatalf("plaintext upload key=%v err=%v", key, err)
	}
	if _, err := svc.MasterKeyForUpload(testChannelID, true); err != ErrPasswordRequired {
		t.Fatalf("encrypted upload err=%v, want password required", err)
	}
	if _, err := svc.RequireMasterKeyForFile(true); err != ErrPasswordRequired {
		t.Fatalf("encrypted file err=%v, want password required", err)
	}

	if err := svc.CreatePassword("old-password", ""); err != nil {
		t.Fatalf("create password: %v", err)
	}
	if key, err := svc.MasterKeyForUpload(testChannelID, true); err != nil || len(key) == 0 {
		t.Fatalf("encrypted upload key len=%d err=%v", len(key), err)
	}
	if key, err := svc.RequireMasterKeyForFile(true); err != nil || len(key) == 0 {
		t.Fatalf("encrypted file key len=%d err=%v", len(key), err)
	}
}
