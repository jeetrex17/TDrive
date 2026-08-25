package main

import (
	"bytes"
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"TDrive/backend/core"
	"TDrive/backend/mountcontroller"
	encservice "TDrive/backend/services/encryption"
)

func TestEncryptionLockWaitsForInFlightMountStart(t *testing.T) {
	app, _ := setupEncryptionApp(t)
	t.Cleanup(app.engine.Close)
	app.ctx = context.Background()
	app.engine.EncryptionService().StoreMasterKey(bytes.Repeat([]byte{9}, 32))

	controller := newBarrierAppMountController()
	controller.releaseStart = make(chan struct{})
	app.mountController = controller
	app.mountDriveResolver = unlockedEncryptedDrive

	mountDone := make(chan error, 1)
	go func() {
		_, err := app.MountDrive()
		mountDone <- err
	}()
	waitLifecycleSignal(t, controller.startEntered, "mount start")
	if app.mountLifecycle.TryLock() {
		app.mountLifecycle.Unlock()
		t.Fatal("mount start did not hold the app lifecycle gate")
	}

	lockInvoked := make(chan struct{})
	lockDone := make(chan error, 1)
	go func() {
		close(lockInvoked)
		lockDone <- app.lockEncryptionSession()
	}()
	waitLifecycleSignal(t, lockInvoked, "lock invocation")

	close(controller.releaseStart)
	if err := waitLifecycleResult(t, mountDone, "mount completion"); err != nil {
		t.Fatalf("MountDrive() error = %v", err)
	}
	waitLifecycleSignal(t, controller.closeEntered, "mount close after start")
	if err := waitLifecycleResult(t, lockDone, "encryption lock"); err != nil {
		t.Fatalf("lockEncryptionSession() error = %v", err)
	}
	if _, remembered := app.engine.EncryptionService().LoadedMasterKey(); remembered {
		t.Fatal("encryption lock returned with the master key still loaded")
	}
}

func TestCreateEncryptionPasswordSerializesPolicyChangeWithMountStart(t *testing.T) {
	app, _ := setupEncryptionApp(t)
	t.Cleanup(app.engine.Close)
	app.ctx = context.Background()

	controller := newBarrierAppMountController()
	controller.releaseClose = make(chan struct{})
	app.mountController = controller
	app.mountDriveResolver = func() (mountcontroller.Drive, error) {
		return mountcontroller.Drive{
			ID:    testEncryptionChannelID,
			Title: "TDrive personal",
			Kind:  mountcontroller.DriveKindPersonal,
		}, nil
	}

	createDone := make(chan error, 1)
	go func() {
		createDone <- app.CreateEncryptionPassword("correct horse battery staple", "hint")
	}()
	waitLifecycleSignal(t, controller.closeEntered, "mount close before password creation")
	if app.mountLifecycle.TryLock() {
		app.mountLifecycle.Unlock()
		t.Fatal("password creation did not hold the app lifecycle gate")
	}

	mountDone := make(chan error, 1)
	go func() {
		_, err := app.MountDrive()
		mountDone <- err
	}()
	close(controller.releaseClose)
	if err := waitLifecycleResult(t, createDone, "password creation"); err != nil {
		t.Fatalf("CreateEncryptionPassword() error = %v", err)
	}
	waitLifecycleSignal(t, controller.startEntered, "mount start after password creation")
	if err := waitLifecycleResult(t, mountDone, "mount completion"); err != nil {
		t.Fatalf("MountDrive() error = %v", err)
	}
	status, err := app.EncryptionStatus()
	if err != nil {
		t.Fatalf("EncryptionStatus() error = %v", err)
	}
	if !status.PasswordSet || !status.PasswordRemembered {
		t.Fatalf("encryption status = %#v, want configured policy before mount start", status)
	}
}

func TestEncryptionStatusDoesNotWaitForMountLifecycle(t *testing.T) {
	app, _ := setupEncryptionApp(t)
	t.Cleanup(app.engine.Close)
	app.ctx = context.Background()
	if err := app.mountLifecycle.Lock(context.Background()); err != nil {
		t.Fatalf("lock lifecycle gate: %v", err)
	}
	locked := true
	defer func() {
		if locked {
			app.mountLifecycle.Unlock()
		}
	}()

	done := make(chan error, 1)
	go func() {
		_, err := app.EncryptionStatus()
		done <- err
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("EncryptionStatus() error = %v", err)
		}
	case <-time.After(2 * time.Second):
		app.mountLifecycle.Unlock()
		locked = false
		<-done
		t.Fatal("EncryptionStatus() waited for the mount lifecycle gate")
	}
	app.mountLifecycle.Unlock()
	locked = false
}

func TestLogoutLifecyclePermanentlyRejectsQueuedMountStart(t *testing.T) {
	app, _ := setupEncryptionApp(t)
	t.Cleanup(app.engine.Close)
	app.ctx = context.Background()
	app.engine.EncryptionService().StoreMasterKey(bytes.Repeat([]byte{3}, 32))

	controller := newBarrierAppMountController()
	app.mountController = controller
	app.mountDriveResolver = unlockedEncryptedDrive

	cleanupEntered := make(chan struct{})
	releaseCleanup := make(chan struct{})
	logoutDone := make(chan error, 1)
	go func() {
		logoutDone <- app.runWithClosedMountForLogout(func() error {
			if _, remembered := app.engine.EncryptionService().LoadedMasterKey(); remembered {
				t.Error("logout cleanup began before the encryption key was cleared")
			}
			close(cleanupEntered)
			<-releaseCleanup
			return nil
		})
	}()
	waitLifecycleSignal(t, cleanupEntered, "logout cleanup")
	if app.mountLifecycle.TryLock() {
		app.mountLifecycle.Unlock()
		t.Fatal("logout cleanup did not hold the app lifecycle gate")
	}

	mountDone := make(chan error, 1)
	go func() {
		_, err := app.MountDrive()
		mountDone <- err
	}()
	close(releaseCleanup)
	if err := waitLifecycleResult(t, logoutDone, "logout cleanup completion"); err != nil {
		t.Fatalf("runWithClosedMountForLogout() error = %v", err)
	}
	if err := waitLifecycleResult(t, mountDone, "rejected mount start"); err == nil {
		t.Fatal("MountDrive() succeeded after logout became terminal")
	}
	select {
	case <-controller.startEntered:
		t.Fatal("controller Start was called after terminal logout")
	default:
	}
}

func TestPasswordKeyWritersSerializeBeforeLockAndLogout(t *testing.T) {
	tests := []struct {
		name      string
		configure func(*barrierEncryptionService)
		writeKey  func(*App) error
		clearKey  func(*App) error
	}{
		{
			name: "unlock before lock",
			configure: func(service *barrierEncryptionService) {
				service.useEntered = make(chan struct{})
				service.releaseUse = make(chan struct{})
			},
			writeKey: func(app *App) error { return app.UseEncryptionPassword("password") },
			clearKey: func(app *App) error { return app.lockEncryptionSession() },
		},
		{
			name: "password change before logout",
			configure: func(service *barrierEncryptionService) {
				service.changeEntered = make(chan struct{})
				service.releaseChange = make(chan struct{})
			},
			writeKey: func(app *App) error {
				return app.ChangeEncryptionPassword("old-password", "new-password", "hint")
			},
			clearKey: func(app *App) error { return app.runWithClosedMountForLogout(nil) },
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			app, _ := setupEncryptionApp(t)
			t.Cleanup(app.engine.Close)
			app.ctx = context.Background()
			app.mountController = newBarrierAppMountController()
			service := &barrierEncryptionService{delegate: app.engine.EncryptionService()}
			test.configure(service)
			app.encryptionServiceOverride = service

			writeDone := make(chan error, 1)
			go func() { writeDone <- test.writeKey(app) }()
			entered, release := service.activeBarrier()
			waitLifecycleSignal(t, entered, "password key writer")
			if app.mountLifecycle.TryLock() {
				app.mountLifecycle.Unlock()
				t.Fatal("password key writer did not hold the app lifecycle gate")
			}

			clearDone := make(chan error, 1)
			go func() { clearDone <- test.clearKey(app) }()
			close(release)
			if err := waitLifecycleResult(t, writeDone, "password key writer completion"); err != nil {
				t.Fatalf("password key writer error = %v", err)
			}
			if err := waitLifecycleResult(t, clearDone, "key clear completion"); err != nil {
				t.Fatalf("key clear error = %v", err)
			}
			if _, remembered := app.engine.EncryptionService().LoadedMasterKey(); remembered {
				t.Fatal("racing password operation repopulated the key after clear")
			}
		})
	}
}

func TestAppShutdownMakesMountLifecycleTerminal(t *testing.T) {
	controller := newBarrierAppMountController()
	app := &App{
		ctx:             context.Background(),
		mountController: controller,
		mountDriveResolver: func() (mountcontroller.Drive, error) {
			return mountcontroller.Drive{ID: 1, Title: "Personal", Kind: mountcontroller.DriveKindPersonal}, nil
		},
	}

	app.shutdown(context.Background())
	waitLifecycleSignal(t, controller.closeEntered, "shutdown mount close")
	if _, err := app.MountDrive(); err == nil {
		t.Fatal("MountDrive() succeeded after shutdown")
	}
	select {
	case <-controller.startEntered:
		t.Fatal("controller Start was called after shutdown")
	default:
	}
}

func TestAppShutdownWithoutControllerMakesMountLifecycleTerminal(t *testing.T) {
	constructionCalls := 0
	app := &App{
		ctx: context.Background(),
		mountControllerFactory: func(*core.Engine) (appMountController, error) {
			constructionCalls++
			return newBarrierAppMountController(), nil
		},
		mountDriveResolver: unlockedEncryptedDrive,
	}

	app.shutdown(context.Background())
	if _, err := app.MountDrive(); !errors.Is(err, errAppMountLifecycleTerminal) {
		t.Fatalf("MountDrive() after shutdown error = %v, want terminal lifecycle", err)
	}
	if constructionCalls != 0 {
		t.Fatalf("shutdown allowed %d controller constructions", constructionCalls)
	}
}

func TestFailedLogoutCleanupLeavesMountLifecycleRecoverable(t *testing.T) {
	app, _ := setupEncryptionApp(t)
	t.Cleanup(app.engine.Close)
	app.ctx = context.Background()
	controller := newBarrierAppMountController()
	app.mountController = controller
	app.mountDriveResolver = unlockedEncryptedDrive
	sentinel := errors.New("remove local session")

	if err := app.runWithClosedMountForLogout(func() error { return sentinel }); !errors.Is(err, sentinel) {
		t.Fatalf("logout cleanup error = %v, want sentinel", err)
	}
	if _, err := app.MountDrive(); err != nil {
		t.Fatalf("MountDrive() after failed logout cleanup error = %v", err)
	}
	waitLifecycleSignal(t, controller.startEntered, "recoverable mount start")
}

func unlockedEncryptedDrive() (mountcontroller.Drive, error) {
	return mountcontroller.Drive{
		ID:                 testEncryptionChannelID,
		Title:              "TDrive personal",
		Kind:               mountcontroller.DriveKindPersonal,
		Encrypted:          true,
		EncryptionUnlocked: true,
	}, nil
}

type barrierAppMountController struct {
	startEntered chan struct{}
	closeEntered chan struct{}
	releaseStart chan struct{}
	releaseClose chan struct{}

	startOnce sync.Once
	closeOnce sync.Once
}

func newBarrierAppMountController() *barrierAppMountController {
	return &barrierAppMountController{
		startEntered: make(chan struct{}),
		closeEntered: make(chan struct{}),
	}
}

func (controller *barrierAppMountController) Start(ctx context.Context, drive mountcontroller.Drive, _ mountcontroller.StartOptions) (mountcontroller.Status, error) {
	controller.startOnce.Do(func() { close(controller.startEntered) })
	if controller.releaseStart != nil {
		select {
		case <-controller.releaseStart:
		case <-ctx.Done():
			return mountcontroller.Status{}, ctx.Err()
		}
	}
	return mountcontroller.Status{
		Phase:      mountcontroller.PhaseMounted,
		Running:    true,
		Mounted:    true,
		DriveID:    drive.ID,
		DriveTitle: drive.Title,
		DriveKind:  drive.Kind,
	}, nil
}

func (*barrierAppMountController) Status() mountcontroller.Status {
	return mountcontroller.Status{Phase: mountcontroller.PhaseStopped}
}

func (*barrierAppMountController) Open(context.Context) error { return nil }

func (*barrierAppMountController) Stop(context.Context) (mountcontroller.Status, error) {
	return mountcontroller.Status{Phase: mountcontroller.PhaseStopped}, nil
}

func (controller *barrierAppMountController) Close(ctx context.Context) error {
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

func waitLifecycleSignal(t *testing.T, signal <-chan struct{}, name string) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for %s", name)
	}
}

func waitLifecycleResult(t *testing.T, result <-chan error, name string) error {
	t.Helper()
	select {
	case err := <-result:
		return err
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for %s", name)
		return nil
	}
}

type barrierEncryptionService struct {
	delegate *encservice.Service

	useEntered    chan struct{}
	releaseUse    chan struct{}
	changeEntered chan struct{}
	releaseChange chan struct{}
}

func (service *barrierEncryptionService) StatusContext(ctx context.Context) (encservice.Status, error) {
	return service.delegate.StatusContext(ctx)
}

func (service *barrierEncryptionService) CreatePasswordContext(ctx context.Context, password string, hint string) error {
	return service.delegate.CreatePasswordContext(ctx, password, hint)
}

func (service *barrierEncryptionService) UsePassword(string) error {
	close(service.useEntered)
	<-service.releaseUse
	service.delegate.StoreMasterKey(bytes.Repeat([]byte{6}, 32))
	return nil
}

func (service *barrierEncryptionService) ChangePassword(string, string, string) error {
	close(service.changeEntered)
	<-service.releaseChange
	service.delegate.StoreMasterKey(bytes.Repeat([]byte{7}, 32))
	return nil
}

func (service *barrierEncryptionService) activeBarrier() (<-chan struct{}, chan struct{}) {
	if service.useEntered != nil {
		return service.useEntered, service.releaseUse
	}
	return service.changeEntered, service.releaseChange
}
