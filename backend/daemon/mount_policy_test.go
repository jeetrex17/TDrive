package daemon

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

func TestDaemonMountFailsClosedWhenPersonalPolicyCannotBeProven(t *testing.T) {
	configureDaemonPolicyTestHome(t)
	const (
		personalID int64 = 8_300_001
		sharedID   int64 = 8_300_002
	)
	engine := newDaemonMountEngine(t, personalID, sharedID)
	controller := &daemonPolicyController{}
	detail := errors.New("partial telegram history")
	refreshCalls := 0
	server := &Server{
		engine:          engine,
		mountController: controller,
		mountEncryptionPolicyRefresh: func(context.Context, int64) error {
			refreshCalls++
			return detail
		},
	}

	_, err := server.startMount(context.Background(), "", "", "")
	if !errors.Is(err, mountpolicy.ErrEncryptionPolicyUnavailable) || errors.Is(err, detail) {
		t.Fatalf("startMount() error = %v, want sanitized policy unavailable", err)
	}
	if refreshCalls != 1 || controller.startCalls != 0 {
		t.Fatalf("refresh calls = %d, controller starts = %d", refreshCalls, controller.startCalls)
	}
	if got := engine.ActiveChannelID(); got != personalID {
		t.Fatalf("active drive changed to %d", got)
	}
}

func TestDaemonMountUsesEncryptedPolicyRestoredByAuthoritativeRefresh(t *testing.T) {
	for _, unlocked := range []bool{false, true} {
		t.Run(map[bool]string{false: "locked", true: "unlocked"}[unlocked], func(t *testing.T) {
			configureDaemonPolicyTestHome(t)
			const (
				personalID int64 = 8_400_001
				sharedID   int64 = 8_400_002
			)
			engine := newDaemonMountEngine(t, personalID, sharedID)
			controller := &daemonPolicyController{}
			server := &Server{
				engine:          engine,
				mountController: controller,
				mountEncryptionPolicyRefresh: func(context.Context, int64) error {
					seedDaemonPolicyReplay(t, engine.ReadService().DB, personalID)
					return nil
				},
			}
			if unlocked {
				if err := engine.EncryptionService().StoreMasterKey(bytes.Repeat([]byte{0x77}, 32)); err != nil {
					t.Fatalf("StoreMasterKey() error = %v", err)
				}
			}

			if _, err := server.startMount(context.Background(), "", "", ""); err != nil {
				t.Fatalf("startMount() error = %v", err)
			}
			if controller.startCalls != 1 || !controller.drive.Encrypted || controller.drive.EncryptionUnlocked != unlocked {
				t.Fatalf("controller drive = %#v, calls = %d", controller.drive, controller.startCalls)
			}
		})
	}
}

func TestDaemonMountAllowsPlaintextOnlyAfterAuthoritativeRefresh(t *testing.T) {
	configureDaemonPolicyTestHome(t)
	const (
		personalID int64 = 8_500_001
		sharedID   int64 = 8_500_002
	)
	engine := newDaemonMountEngine(t, personalID, sharedID)
	controller := &daemonPolicyController{}
	refreshCalls := 0
	server := &Server{
		engine:          engine,
		mountController: controller,
		mountEncryptionPolicyRefresh: func(context.Context, int64) error {
			refreshCalls++
			return nil
		},
	}

	if _, err := server.startMount(context.Background(), "", "", ""); err != nil {
		t.Fatalf("startMount() error = %v", err)
	}
	if refreshCalls != 1 || controller.startCalls != 1 || controller.drive.Encrypted {
		t.Fatalf("refresh calls = %d, controller drive = %#v", refreshCalls, controller.drive)
	}
}

func TestDaemonVaultAndGeneralStatusFailClosedWhenPolicyCannotBeProven(t *testing.T) {
	configureDaemonPolicyTestHome(t)
	const (
		personalID int64 = 8_600_001
		sharedID   int64 = 8_600_002
	)
	detail := errors.New("telegram policy sync failed")
	contextKey := struct{}{}
	requestContext := context.WithValue(context.Background(), contextKey, "request")
	refreshCalls := 0
	engine := newDaemonMountEngineWithPolicyRefresh(t, personalID, sharedID, func(ctx context.Context, _ int64) error {
		refreshCalls++
		if got := ctx.Value(contextKey); got != "request" {
			t.Fatalf("policy refresh context value = %v", got)
		}
		return detail
	})
	server := &Server{engine: engine, state: newState()}

	if _, err := server.vaultStatus(requestContext); !errors.Is(err, mountpolicy.ErrEncryptionPolicyUnavailable) || errors.Is(err, detail) {
		t.Fatalf("vaultStatus() error = %v, want sanitized unavailable", err)
	}
	if _, err := server.status(requestContext); !errors.Is(err, mountpolicy.ErrEncryptionPolicyUnavailable) || errors.Is(err, detail) {
		t.Fatalf("status() error = %v, want sanitized unavailable", err)
	}
	if refreshCalls != 2 {
		t.Fatalf("policy refresh calls = %d, want 2", refreshCalls)
	}
}

func TestDaemonVaultStatusReportsUnconfiguredOnlyAfterAuthoritativeRefresh(t *testing.T) {
	configureDaemonPolicyTestHome(t)
	const (
		personalID int64 = 8_700_001
		sharedID   int64 = 8_700_002
	)
	refreshCalls := 0
	engine := newDaemonMountEngineWithPolicyRefresh(t, personalID, sharedID, func(context.Context, int64) error {
		refreshCalls++
		return nil
	})
	server := &Server{engine: engine, state: newState()}

	vault, err := server.vaultStatus(context.Background())
	if err != nil {
		t.Fatalf("vaultStatus() error = %v", err)
	}
	if !vault.Status.Available || vault.Status.Configured || vault.Status.Unlocked {
		t.Fatalf("vaultStatus() = %#v", vault)
	}
	status, err := server.status(context.Background())
	if err != nil {
		t.Fatalf("status() error = %v", err)
	}
	if !status.VaultAvailable || status.VaultConfigured || status.VaultUnlocked {
		t.Fatalf("status() = %#v", status)
	}
	if refreshCalls != 2 {
		t.Fatalf("policy refresh calls = %d, want 2", refreshCalls)
	}
}

func configureDaemonPolicyTestHome(t *testing.T) {
	t.Helper()
	configHome := t.TempDir()
	t.Setenv("HOME", configHome)
	t.Setenv("XDG_CONFIG_HOME", configHome)
	t.Setenv("APPDATA", configHome)
}

type daemonPolicyController struct {
	drive      mountcontroller.Drive
	startCalls int
}

func (controller *daemonPolicyController) Start(_ context.Context, drive mountcontroller.Drive, _ mountcontroller.StartOptions) (mountcontroller.Status, error) {
	controller.startCalls++
	controller.drive = drive
	return mountcontroller.Status{Phase: mountcontroller.PhaseMounted, DriveID: drive.ID}, nil
}

func (*daemonPolicyController) Status() mountcontroller.Status { return mountcontroller.Status{} }
func (*daemonPolicyController) Stop(context.Context) (mountcontroller.Status, error) {
	return mountcontroller.Status{Phase: mountcontroller.PhaseStopped}, nil
}
func (*daemonPolicyController) Close(context.Context) error { return nil }

func seedDaemonPolicyReplay(t *testing.T, db *sql.DB, channelID int64) {
	t.Helper()
	op := daemonEncryptionPolicyOp()
	if _, err := projection.ProjectFromOp(db, channelID, 101, op, 1, projection.Format(op)); err != nil {
		t.Fatalf("seed encryption replay: %v", err)
	}
}

func daemonEncryptionPolicyOp() projection.Op {
	return projection.Op{
		Type:             projection.OpEncConfig,
		KDFSalt:          bytes.Repeat([]byte{0x11}, 16),
		KDFParamsJSON:    `{"memory":65536,"time":3,"parallelism":4,"key_len":32,"salt_len":16}`,
		WrappedMasterKey: bytes.Repeat([]byte{0x22}, 72),
		KeyCheck:         bytes.Repeat([]byte{0x33}, 59),
		ConfigVersion:    1,
	}
}
