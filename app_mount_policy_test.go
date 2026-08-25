package main

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"testing"

	"TDrive/backend/mountcontroller"
	"TDrive/backend/mountpolicy"
	"TDrive/backend/projection"
)

func TestAppMountFailsClosedWhenPersonalPolicyCannotBeProven(t *testing.T) {
	app, _ := setupEncryptionApp(t)
	t.Cleanup(app.engine.Close)
	app.engine.SetActiveChannelID(testEncryptionChannelID)
	controller := &fakeAppMountController{}
	app.mountController = controller
	detail := errors.New("telegram endpoint unavailable")
	refreshCalls := 0
	app.mountEncryptionPolicyRefresh = func(context.Context, int64) error {
		refreshCalls++
		return detail
	}

	_, err := app.MountDrive()
	if !errors.Is(err, mountpolicy.ErrEncryptionPolicyUnavailable) || errors.Is(err, detail) {
		t.Fatalf("MountDrive() error = %v, want sanitized policy unavailable", err)
	}
	if refreshCalls != 1 || controller.startCalls != 0 {
		t.Fatalf("refresh calls = %d, controller starts = %d", refreshCalls, controller.startCalls)
	}
	if got := app.engine.ActiveChannelID(); got != testEncryptionChannelID {
		t.Fatalf("active drive changed to %d", got)
	}
}

func TestAppMountUsesEncryptedPolicyRestoredByAuthoritativeRefresh(t *testing.T) {
	for _, unlocked := range []bool{false, true} {
		t.Run(map[bool]string{false: "locked", true: "unlocked"}[unlocked], func(t *testing.T) {
			app, db := setupEncryptionApp(t)
			t.Cleanup(app.engine.Close)
			app.engine.SetActiveChannelID(testEncryptionChannelID)
			controller := &fakeAppMountController{startStatus: mountcontroller.Status{Phase: mountcontroller.PhaseMounted}}
			app.mountController = controller
			app.mountEncryptionPolicyRefresh = func(context.Context, int64) error {
				seedAppEncryptionReplay(t, db, testEncryptionChannelID)
				return nil
			}
			if unlocked {
				if err := app.engine.EncryptionService().StoreMasterKey(bytes.Repeat([]byte{0x66}, 32)); err != nil {
					t.Fatalf("StoreMasterKey() error = %v", err)
				}
			}

			if _, err := app.MountDrive(); err != nil {
				t.Fatalf("MountDrive() error = %v", err)
			}
			if controller.startCalls != 1 || !controller.startedDrive.Encrypted || controller.startedDrive.EncryptionUnlocked != unlocked {
				t.Fatalf("controller drive = %#v, calls = %d", controller.startedDrive, controller.startCalls)
			}
		})
	}
}

func TestAppMountAllowsPlaintextOnlyAfterAuthoritativeRefresh(t *testing.T) {
	app, _ := setupEncryptionApp(t)
	t.Cleanup(app.engine.Close)
	app.engine.SetActiveChannelID(testEncryptionChannelID)
	controller := &fakeAppMountController{startStatus: mountcontroller.Status{Phase: mountcontroller.PhaseMounted}}
	app.mountController = controller
	refreshCalls := 0
	app.mountEncryptionPolicyRefresh = func(context.Context, int64) error {
		refreshCalls++
		return nil
	}

	if _, err := app.MountDrive(); err != nil {
		t.Fatalf("MountDrive() error = %v", err)
	}
	if refreshCalls != 1 || controller.startCalls != 1 || controller.startedDrive.Encrypted {
		t.Fatalf("refresh calls = %d, controller drive = %#v", refreshCalls, controller.startedDrive)
	}
}

func seedAppEncryptionReplay(t *testing.T, db *sql.DB, channelID int64) {
	t.Helper()
	op := appEncryptionPolicyOp()
	if _, err := projection.ProjectFromOp(db, channelID, 101, op, 1, projection.Format(op)); err != nil {
		t.Fatalf("seed encryption replay: %v", err)
	}
}

func appEncryptionPolicyOp() projection.Op {
	return projection.Op{
		Type:             projection.OpEncConfig,
		KDFSalt:          bytes.Repeat([]byte{0x11}, 16),
		KDFParamsJSON:    `{"memory":65536,"time":3,"parallelism":4,"key_len":32,"salt_len":16}`,
		WrappedMasterKey: bytes.Repeat([]byte{0x22}, 72),
		KeyCheck:         bytes.Repeat([]byte{0x33}, 59),
		ConfigVersion:    1,
	}
}
