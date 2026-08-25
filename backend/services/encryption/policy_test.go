package encryption

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"testing"

	"TDrive/backend/mountpolicy"
	"TDrive/backend/projection"
)

func TestStatusAndCreatePasswordFailClosedWhenPolicyRefreshFails(t *testing.T) {
	db := newPolicyServiceDB(t)
	detail := errors.New("telegram history incomplete")
	emitCalls := 0
	service := NewService(Config{
		DB:                db,
		PersonalChannelID: func() int64 { return testChannelID },
		EnsurePolicy: func(context.Context, int64) error {
			return detail
		},
		EmitOp: func(int64, projection.Op) error {
			emitCalls++
			return nil
		},
	})

	if _, err := service.StatusContext(context.Background()); !errors.Is(err, mountpolicy.ErrEncryptionPolicyUnavailable) || errors.Is(err, detail) {
		t.Fatalf("StatusContext() error = %v, want sanitized unavailable", err)
	}
	if err := service.CreatePasswordContext(context.Background(), "password-1", "hint"); !errors.Is(err, mountpolicy.ErrEncryptionPolicyUnavailable) || errors.Is(err, detail) {
		t.Fatalf("CreatePasswordContext() error = %v, want sanitized unavailable", err)
	}
	if emitCalls != 0 {
		t.Fatalf("EmitOp calls = %d, want 0", emitCalls)
	}
	if _, ok := service.LoadedMasterKey(); ok {
		t.Fatal("failed policy authority retained a generated master key")
	}
}

func TestStatusAndCreatePasswordHonorConfigRestoredByPolicyRefresh(t *testing.T) {
	db := newPolicyServiceDB(t)
	refreshCalls := 0
	emitCalls := 0
	service := NewService(Config{
		DB:                db,
		PersonalChannelID: func() int64 { return testChannelID },
		EnsurePolicy: func(context.Context, int64) error {
			refreshCalls++
			seedServicePolicyReplay(t, db)
			return nil
		},
		EmitOp: func(int64, projection.Op) error {
			emitCalls++
			return nil
		},
	})

	status, err := service.StatusContext(context.Background())
	if err != nil {
		t.Fatalf("StatusContext() error = %v", err)
	}
	if !status.PasswordSet || status.PasswordRemembered {
		t.Fatalf("StatusContext() = %#v, want locked configured vault", status)
	}
	if err := service.CreatePasswordContext(context.Background(), "password-1", "hint"); err == nil || !errors.Is(err, ErrPasswordAlreadySet) {
		t.Fatalf("CreatePasswordContext() error = %v, want password already set", err)
	}
	if refreshCalls != 1 || emitCalls != 0 {
		t.Fatalf("refresh calls = %d, emit calls = %d", refreshCalls, emitCalls)
	}
}

func TestCreatePasswordAllowedAfterAuthoritativePlaintextPolicy(t *testing.T) {
	db := newPolicyServiceDB(t)
	refreshCalls := 0
	emitCalls := 0
	service := NewService(Config{
		DB:                db,
		PersonalChannelID: func() int64 { return testChannelID },
		EnsurePolicy: func(context.Context, int64) error {
			refreshCalls++
			return nil
		},
		EmitOp: func(channelID int64, op projection.Op) error {
			emitCalls++
			_, err := projection.ProjectFromOp(db, channelID, int64(100+emitCalls), op, 1, projection.Format(op))
			return err
		},
	})

	if err := service.CreatePasswordContext(context.Background(), "password-1", "hint"); err != nil {
		t.Fatalf("CreatePasswordContext() error = %v", err)
	}
	if refreshCalls != 1 || emitCalls != 1 {
		t.Fatalf("refresh calls = %d, emit calls = %d", refreshCalls, emitCalls)
	}
	if _, ok := service.LoadedMasterKey(); !ok {
		t.Fatal("successful password creation did not remember the master key")
	}
}

func newPolicyServiceDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	if err := projection.MigratePersonalChannel(db, testChannelID); err != nil {
		t.Fatalf("migrate projection: %v", err)
	}
	return db
}

func seedServicePolicyReplay(t *testing.T, db *sql.DB) {
	t.Helper()
	op := projection.Op{
		Type:             projection.OpEncConfig,
		KDFSalt:          bytes.Repeat([]byte{0x11}, 16),
		KDFParamsJSON:    `{"memory":65536,"time":3,"parallelism":4,"key_len":32,"salt_len":16}`,
		WrappedMasterKey: bytes.Repeat([]byte{0x22}, 72),
		KeyCheck:         bytes.Repeat([]byte{0x33}, 59),
		ConfigVersion:    1,
	}
	if _, err := projection.ProjectFromOp(db, testChannelID, 101, op, 1, projection.Format(op)); err != nil {
		t.Fatalf("seed policy replay: %v", err)
	}
}
