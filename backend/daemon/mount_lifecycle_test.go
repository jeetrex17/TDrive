package daemon

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"sync"
	"testing"
	"time"

	coreauth "TDrive/backend/auth"
	"TDrive/backend/mountcontroller"
)

func TestVaultLockSerializesKeyClearWithRacingMountStart(t *testing.T) {
	prepareDaemonLifecycleTest(t)
	const (
		personalDriveID int64 = 8_300_001
		sharedDriveID   int64 = 8_300_002
	)
	engine := newDaemonMountEngine(t, personalDriveID, sharedDriveID)
	seedDaemonEncryptionPolicy(t, engine.ReadService().DB, personalDriveID)
	engine.EncryptionService().StoreMasterKey(bytes.Repeat([]byte{4}, 32))

	controller := newBarrierDaemonMountController()
	controller.releaseStop = make(chan struct{})
	server := &Server{engine: engine, mountController: controller}

	lockDone := make(chan error, 1)
	go func() {
		_, err := server.vaultLock(context.Background())
		lockDone <- err
	}()
	waitDaemonLifecycleSignal(t, controller.stopEntered, "vault mount stop")
	if server.mountLifecycle.TryLock() {
		server.mountLifecycle.Unlock()
		t.Fatal("vault lock did not hold the daemon mount lifecycle gate")
	}

	startDone := make(chan error, 1)
	go func() {
		_, err := server.startMount(context.Background(), "", "", "")
		startDone <- err
	}()
	close(controller.releaseStop)
	if err := waitDaemonLifecycleResult(t, lockDone, "vault lock"); err != nil {
		t.Fatalf("vaultLock() error = %v", err)
	}
	waitDaemonLifecycleSignal(t, controller.startEntered, "mount start after vault lock")
	if err := waitDaemonLifecycleResult(t, startDone, "mount start"); err != nil {
		t.Fatalf("startMount() error = %v", err)
	}
	if _, remembered := engine.EncryptionService().LoadedMasterKey(); remembered {
		t.Fatal("vault lock returned with the master key still loaded")
	}
	drive := controller.startedDriveSnapshot()
	if !drive.Encrypted || drive.EncryptionUnlocked {
		t.Fatalf("racing mount observed drive = %#v, want encrypted and locked", drive)
	}
}

func TestAuthLogoutMakesMountLifecycleTerminal(t *testing.T) {
	prepareDaemonLifecycleTest(t)
	const (
		personalDriveID int64 = 8_400_001
		sharedDriveID   int64 = 8_400_002
	)
	engine := newDaemonMountEngine(t, personalDriveID, sharedDriveID)
	seedDaemonEncryptionPolicy(t, engine.ReadService().DB, personalDriveID)
	engine.EncryptionService().StoreMasterKey(bytes.Repeat([]byte{5}, 32))

	controller := newBarrierDaemonMountController()
	controller.releaseStop = make(chan struct{})
	server := &Server{engine: engine, mountController: controller}

	logoutDone := make(chan error, 1)
	go func() {
		_, err := server.authLogout(context.Background(), string(coreauth.LogoutSoft))
		logoutDone <- err
	}()
	waitDaemonLifecycleSignal(t, controller.stopEntered, "logout mount stop")
	if server.mountLifecycle.TryLock() {
		server.mountLifecycle.Unlock()
		t.Fatal("logout did not hold the daemon mount lifecycle gate")
	}

	startDone := make(chan error, 1)
	go func() {
		_, err := server.startMount(context.Background(), "", "", "")
		startDone <- err
	}()
	close(controller.releaseStop)
	if err := waitDaemonLifecycleResult(t, logoutDone, "daemon logout"); err != nil {
		t.Fatalf("authLogout() error = %v", err)
	}
	if err := waitDaemonLifecycleResult(t, startDone, "rejected mount start"); err == nil {
		t.Fatal("startMount() succeeded after terminal logout")
	}
	select {
	case <-controller.startEntered:
		t.Fatal("controller Start was called after terminal logout")
	default:
	}
	if _, remembered := engine.EncryptionService().LoadedMasterKey(); remembered {
		t.Fatal("logout returned with the master key still loaded")
	}
}

func TestDaemonMountCloseMakesLifecycleTerminal(t *testing.T) {
	prepareDaemonLifecycleTest(t)
	const (
		personalDriveID int64 = 8_500_001
		sharedDriveID   int64 = 8_500_002
	)
	engine := newDaemonMountEngine(t, personalDriveID, sharedDriveID)
	controller := newBarrierDaemonMountController()
	controller.releaseClose = make(chan struct{})
	server := &Server{engine: engine, mountController: controller}

	closeDone := make(chan error, 1)
	go func() { closeDone <- server.stopMountServer(context.Background()) }()
	waitDaemonLifecycleSignal(t, controller.closeEntered, "mount server close")
	if server.mountLifecycle.TryLock() {
		server.mountLifecycle.Unlock()
		t.Fatal("mount Close did not hold the daemon mount lifecycle gate")
	}

	startDone := make(chan error, 1)
	go func() {
		_, err := server.startMount(context.Background(), "", "", "")
		startDone <- err
	}()
	close(controller.releaseClose)
	if err := waitDaemonLifecycleResult(t, closeDone, "mount server close"); err != nil {
		t.Fatalf("stopMountServer() error = %v", err)
	}
	if err := waitDaemonLifecycleResult(t, startDone, "rejected mount start"); err == nil {
		t.Fatal("startMount() succeeded after terminal mount server close")
	}
	select {
	case <-controller.startEntered:
		t.Fatal("controller Start was called after terminal mount server close")
	default:
	}
}

func TestVaultUnlockHonorsLifecycleGateAndCanceledContext(t *testing.T) {
	prepareDaemonLifecycleTest(t)
	engine := newDaemonMountEngine(t, 8_550_001, 8_550_002)
	server := &Server{engine: engine, mountController: newBarrierDaemonMountController()}
	if err := server.mountLifecycle.Lock(context.Background()); err != nil {
		t.Fatalf("lock lifecycle gate: %v", err)
	}
	defer server.mountLifecycle.Unlock()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := server.vaultUnlock(ctx, "password"); !errors.Is(err, context.Canceled) {
		t.Fatalf("vaultUnlock() error = %v, want context canceled while queued", err)
	}
	if _, remembered := engine.EncryptionService().LoadedMasterKey(); remembered {
		t.Fatal("canceled vault unlock changed key state")
	}
}

func TestMountStartAndVaultUnlockRejectAlreadyCanceledContext(t *testing.T) {
	server := &Server{}
	for iteration := 0; iteration < 100; iteration++ {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if _, err := server.startMount(ctx, "", "", ""); !errors.Is(err, context.Canceled) {
			t.Fatalf("startMount() iteration %d error = %v, want context canceled", iteration, err)
		}
		if _, err := server.vaultUnlock(ctx, "password"); !errors.Is(err, context.Canceled) {
			t.Fatalf("vaultUnlock() iteration %d error = %v, want context canceled", iteration, err)
		}
	}
	if !server.mountLifecycle.TryLock() {
		t.Fatal("canceled lifecycle acquisition leaked the gate token")
	}
	server.mountLifecycle.Unlock()
}

func TestVaultLockAndLogoutRetainKeyWhenEjectFails(t *testing.T) {
	tests := []struct {
		name string
		run  func(*Server) error
	}{
		{
			name: "vault lock",
			run: func(server *Server) error {
				_, err := server.vaultLock(context.Background())
				return err
			},
		},
		{
			name: "logout",
			run: func(server *Server) error {
				_, err := server.authLogout(context.Background(), string(coreauth.LogoutSoft))
				return err
			},
		},
	}
	for index, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			prepareDaemonLifecycleTest(t)
			personalDriveID := int64(8_600_001 + index*2)
			engine := newDaemonMountEngine(t, personalDriveID, personalDriveID+1)
			engine.EncryptionService().StoreMasterKey(bytes.Repeat([]byte{7}, 32))
			ejectErr := errors.New("eject failed")
			controller := newBarrierDaemonMountController()
			controller.stopErr = ejectErr
			controller.status = mountcontroller.Status{
				Phase:     mountcontroller.PhaseFailed,
				Running:   true,
				Mounted:   true,
				DriveID:   personalDriveID,
				DriveKind: mountcontroller.DriveKindPersonal,
			}
			server := &Server{engine: engine, mountController: controller}

			if err := test.run(server); !errors.Is(err, ejectErr) {
				t.Fatalf("operation error = %v, want eject failure", err)
			}
			if _, remembered := engine.EncryptionService().LoadedMasterKey(); !remembered {
				t.Fatal("eject failure cleared the master key")
			}
			if status := controller.Status(); !status.Mounted {
				t.Fatalf("eject failure lost mounted state: %#v", status)
			}
		})
	}
}

func seedDaemonEncryptionPolicy(t *testing.T, db *sql.DB, channelID int64) {
	t.Helper()
	seedDaemonPolicyReplay(t, db, channelID)
}

func prepareDaemonLifecycleTest(t *testing.T) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("APPDATA", t.TempDir())
}

type barrierDaemonMountController struct {
	mu sync.Mutex

	status       mountcontroller.Status
	startedDrive mountcontroller.Drive
	stopErr      error

	startEntered chan struct{}
	stopEntered  chan struct{}
	closeEntered chan struct{}
	releaseStart chan struct{}
	releaseStop  chan struct{}
	releaseClose chan struct{}

	startOnce sync.Once
	stopOnce  sync.Once
	closeOnce sync.Once
}

func newBarrierDaemonMountController() *barrierDaemonMountController {
	return &barrierDaemonMountController{
		status:       mountcontroller.Status{Phase: mountcontroller.PhaseStopped},
		startEntered: make(chan struct{}),
		stopEntered:  make(chan struct{}),
		closeEntered: make(chan struct{}),
	}
}

func (controller *barrierDaemonMountController) Start(ctx context.Context, drive mountcontroller.Drive, _ mountcontroller.StartOptions) (mountcontroller.Status, error) {
	controller.mu.Lock()
	controller.startedDrive = drive
	controller.mu.Unlock()
	controller.startOnce.Do(func() { close(controller.startEntered) })
	if controller.releaseStart != nil {
		select {
		case <-controller.releaseStart:
		case <-ctx.Done():
			return mountcontroller.Status{}, ctx.Err()
		}
	}
	status := mountcontroller.Status{
		Phase:                   mountcontroller.PhaseMounted,
		Running:                 true,
		Mounted:                 true,
		DriveID:                 drive.ID,
		DriveTitle:              drive.Title,
		DriveKind:               drive.Kind,
		DriveEncrypted:          drive.Encrypted,
		DriveEncryptionUnlocked: drive.EncryptionUnlocked,
	}
	controller.mu.Lock()
	controller.status = status
	controller.mu.Unlock()
	return status, nil
}

func (controller *barrierDaemonMountController) Status() mountcontroller.Status {
	controller.mu.Lock()
	defer controller.mu.Unlock()
	return controller.status
}

func (controller *barrierDaemonMountController) Stop(ctx context.Context) (mountcontroller.Status, error) {
	controller.stopOnce.Do(func() { close(controller.stopEntered) })
	if controller.releaseStop != nil {
		select {
		case <-controller.releaseStop:
		case <-ctx.Done():
			return controller.Status(), ctx.Err()
		}
	}
	if controller.stopErr != nil {
		return controller.Status(), controller.stopErr
	}
	status := mountcontroller.Status{Phase: mountcontroller.PhaseStopped}
	controller.mu.Lock()
	controller.status = status
	controller.mu.Unlock()
	return status, nil
}

func (controller *barrierDaemonMountController) Close(ctx context.Context) error {
	controller.closeOnce.Do(func() { close(controller.closeEntered) })
	if controller.releaseClose != nil {
		select {
		case <-controller.releaseClose:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return nil
}

func (controller *barrierDaemonMountController) startedDriveSnapshot() mountcontroller.Drive {
	controller.mu.Lock()
	defer controller.mu.Unlock()
	return controller.startedDrive
}

func waitDaemonLifecycleSignal(t *testing.T, signal <-chan struct{}, name string) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for %s", name)
	}
}

func waitDaemonLifecycleResult(t *testing.T, result <-chan error, name string) error {
	t.Helper()
	select {
	case err := <-result:
		return err
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for %s", name)
		return nil
	}
}
