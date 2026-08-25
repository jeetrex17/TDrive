package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"TDrive/backend/core"
	"TDrive/backend/mountcontroller"
)

func TestAppMountDrivePinsResolvedActiveDrive(t *testing.T) {
	t.Parallel()

	controller := &fakeAppMountController{
		startStatus: mountcontroller.Status{
			Phase:           mountcontroller.PhaseMounted,
			Running:         true,
			Mounted:         true,
			DriveID:         42,
			DriveTitle:      "My Drive",
			DriveKind:       mountcontroller.DriveKindPersonal,
			Label:           "Tdrive personal",
			Location:        "/safe/mount",
			Mode:            mountcontroller.ModeReadWrite,
			WriteState:      mountcontroller.WriteStateReady,
			AcceptingWrites: true,
			WindowsDrive:    "T:",
		},
	}
	app := &App{
		ctx:             context.Background(),
		mountController: controller,
		mountDriveResolver: func() (mountcontroller.Drive, error) {
			return mountcontroller.Drive{
				ID:    42,
				Title: "My Drive",
				Kind:  mountcontroller.DriveKindPersonal,
			}, nil
		},
	}

	view, err := app.MountDrive()
	if err != nil {
		t.Fatalf("MountDrive() error = %v", err)
	}
	if controller.startCalls != 1 || controller.startedDrive.ID != 42 {
		t.Fatalf("controller start = calls:%d drive:%#v", controller.startCalls, controller.startedDrive)
	}
	if controller.startOptions.Mode != mountcontroller.ModeAuto {
		t.Fatalf("default app mount mode = %q, want auto", controller.startOptions.Mode)
	}
	if view.Phase != "mounted" || !view.Mounted || view.Label != "Tdrive personal" || view.Location != "/safe/mount" {
		t.Fatalf("MountDrive() = %#v", view)
	}
	if view.Drive.ID != 42 || view.Drive.Kind != mountcontroller.DriveKindPersonal {
		t.Fatalf("MountDrive().Drive = %#v", view.Drive)
	}
	if view.Mode != "read-write" || view.WriteState != "ready" || !view.AcceptingWrites {
		t.Fatalf("MountDrive() writable state = %#v", view)
	}

	payload, err := json.Marshal(view)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"url", "commands", "tdrive-", "127.0.0.1"} {
		if strings.Contains(strings.ToLower(string(payload)), forbidden) {
			t.Fatalf("MountView leaked %q: %s", forbidden, payload)
		}
	}
}

func TestResolveActiveMountDrivePropagatesEncryptedEligibility(t *testing.T) {
	app, db := setupEncryptionApp(t)
	t.Cleanup(app.engine.Close)
	app.engine.SetActiveChannelID(testEncryptionChannelID)
	seedAppEncryptionReplay(t, db, testEncryptionChannelID)

	drive, err := app.resolveActiveMountDriveContext(context.Background())
	if err != nil {
		t.Fatalf("resolveActiveMountDrive() error = %v", err)
	}
	if !drive.Encrypted {
		t.Fatalf("resolveActiveMountDrive() = %#v, want encrypted eligibility", drive)
	}
	if drive.EncryptionUnlocked {
		t.Fatalf("resolveActiveMountDrive() = %#v, want locked encryption", drive)
	}

	app.engine.EncryptionService().StoreMasterKey(bytes.Repeat([]byte{7}, 32))
	drive, err = app.resolveActiveMountDriveContext(context.Background())
	if err != nil {
		t.Fatalf("resolveActiveMountDrive() after unlock error = %v", err)
	}
	if !drive.Encrypted || !drive.EncryptionUnlocked {
		t.Fatalf("resolveActiveMountDrive() after unlock = %#v", drive)
	}
}

func TestAppMountDriveRejectsUnavailableActiveDrive(t *testing.T) {
	t.Parallel()

	sentinel := errors.New("no active drive")
	controller := &fakeAppMountController{}
	app := &App{
		ctx:             context.Background(),
		mountController: controller,
		mountDriveResolver: func() (mountcontroller.Drive, error) {
			return mountcontroller.Drive{}, sentinel
		},
	}

	if _, err := app.MountDrive(); !errors.Is(err, sentinel) {
		t.Fatalf("MountDrive() error = %v, want sentinel", err)
	}
	if controller.startCalls != 0 {
		t.Fatalf("controller Start calls = %d, want 0", controller.startCalls)
	}
}

func TestAppMountActionsDelegateAndMapPhases(t *testing.T) {
	t.Parallel()

	controller := &fakeAppMountController{
		status:     mountcontroller.Status{Phase: mountcontroller.PhaseAttaching, Running: true, Label: "Tdrive personal"},
		stopStatus: mountcontroller.Status{Phase: mountcontroller.PhaseStopped},
	}
	app := &App{ctx: context.Background(), mountController: controller}

	if view := app.MountStatus(); view.Phase != "mounting" || view.Mounted {
		t.Fatalf("MountStatus() = %#v", view)
	}
	if err := app.OpenMountedDrive(); err != nil {
		t.Fatalf("OpenMountedDrive() error = %v", err)
	}
	if controller.openCalls != 1 {
		t.Fatalf("Open calls = %d, want 1", controller.openCalls)
	}
	view, err := app.UnmountDrive()
	if err != nil || view.Phase != "idle" {
		t.Fatalf("UnmountDrive() = (%#v, %v)", view, err)
	}
	if controller.stopCalls != 1 {
		t.Fatalf("Stop calls = %d, want 1", controller.stopCalls)
	}
}

func TestAppMountMutationsUseBoundedContexts(t *testing.T) {
	t.Parallel()

	assertBounded := func(t *testing.T, ctx context.Context) {
		t.Helper()
		deadline, ok := ctx.Deadline()
		if !ok {
			t.Fatal("mount operation context has no deadline")
		}
		remaining := time.Until(deadline)
		if remaining <= 50*time.Second || remaining > encryptionMountTransitionTimeout {
			t.Fatalf("mount operation deadline remaining = %s", remaining)
		}
	}

	t.Run("single drive", func(t *testing.T) {
		controller := &fakeAppMountController{startContext: func(ctx context.Context) { assertBounded(t, ctx) }}
		app := &App{
			ctx:             context.Background(),
			mountController: controller,
			mountDriveResolver: func() (mountcontroller.Drive, error) {
				return mountcontroller.Drive{ID: 1, Kind: mountcontroller.DriveKindShared}, nil
			},
		}
		if _, err := app.MountDrive(); err != nil {
			t.Fatalf("MountDrive() error = %v", err)
		}
	})

	t.Run("selected drives", func(t *testing.T) {
		controller := &fakeAppMountController{startDrivesContext: func(ctx context.Context) { assertBounded(t, ctx) }}
		app := &App{
			ctx:             context.Background(),
			mountController: controller,
			mountDrivesResolver: func([]int64) ([]mountcontroller.Drive, error) {
				return []mountcontroller.Drive{{ID: 1, Kind: mountcontroller.DriveKindShared}}, nil
			},
		}
		if _, err := app.MountDrives([]int64{1}); err != nil {
			t.Fatalf("MountDrives() error = %v", err)
		}
	})

	t.Run("unmount", func(t *testing.T) {
		controller := &fakeAppMountController{stopContext: func(ctx context.Context) { assertBounded(t, ctx) }}
		app := &App{ctx: context.Background(), mountController: controller}
		if _, err := app.UnmountDrive(); err != nil {
			t.Fatalf("UnmountDrive() error = %v", err)
		}
	})
}

func TestAppMountFailureMessageRedactsCapabilities(t *testing.T) {
	t.Parallel()

	secret := "http://127.0.0.1:49152/tdrive-0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef/"
	controller := &fakeAppMountController{startStatus: mountcontroller.Status{
		Phase: mountcontroller.PhaseFailed,
		Error: "attach failed for " + secret,
	}, startErr: errors.New("attach failed for " + secret)}
	app := &App{
		ctx:             context.Background(),
		mountController: controller,
		mountDriveResolver: func() (mountcontroller.Drive, error) {
			return mountcontroller.Drive{ID: 42, Kind: mountcontroller.DriveKindPersonal}, nil
		},
	}

	view, err := app.MountDrive()
	if err == nil || !strings.Contains(err.Error(), "Mount operation failed") {
		t.Fatalf("MountDrive() error = %v, want safe fallback", err)
	}
	if strings.Contains(strings.ToLower(err.Error()), "tdrive-") {
		t.Fatalf("MountDrive() error leaked capability: %v", err)
	}
	payload, err := json.Marshal(view)
	if err != nil {
		t.Fatal(err)
	}
	text := strings.ToLower(string(payload))
	for _, forbidden := range []string{"tdrive-", "127.0.0.1", "http://", "localhost"} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("MountView leaked %q: %s", forbidden, payload)
		}
	}
	if !strings.Contains(view.Error, "Mount operation failed") {
		t.Fatalf("MountView error = %q, want safe fallback", view.Error)
	}
}

func TestAppShutdownClosesMountController(t *testing.T) {
	t.Parallel()

	controller := &fakeAppMountController{}
	app := &App{ctx: context.Background(), mountController: controller}
	app.shutdown(context.Background())
	if controller.closeCalls != 1 {
		t.Fatalf("Close calls = %d, want 1", controller.closeCalls)
	}
	if app.mountController != controller {
		t.Fatal("shutdown replaced the immutable mount controller reference")
	}
}

func TestAppMountControllerConstructionRetriesAfterFailure(t *testing.T) {
	t.Parallel()

	sentinel := errors.New("controller dependencies unavailable")
	controller := &fakeAppMountController{startStatus: mountcontroller.Status{Phase: mountcontroller.PhaseMounted}}
	constructionCalls := 0
	app := &App{
		ctx: context.Background(),
		mountControllerFactory: func(*core.Engine) (appMountController, error) {
			constructionCalls++
			if constructionCalls == 1 {
				return nil, sentinel
			}
			return controller, nil
		},
		mountDriveResolver: func() (mountcontroller.Drive, error) {
			return mountcontroller.Drive{ID: 1, Kind: mountcontroller.DriveKindShared}, nil
		},
	}

	if _, err := app.ensureMountController(); !errors.Is(err, sentinel) {
		t.Fatalf("startup controller construction error = %v, want sentinel", err)
	}
	if _, err := app.MountDrive(); err != nil {
		t.Fatalf("second MountDrive() error = %v", err)
	}
	if constructionCalls != 2 || controller.startCalls != 1 {
		t.Fatalf("construction calls = %d, start calls = %d", constructionCalls, controller.startCalls)
	}
}

func TestLockEncryptionSessionRefusesToClearKeyWhenMountCannotClose(t *testing.T) {
	app, _ := setupEncryptionApp(t)
	t.Cleanup(app.engine.Close)
	app.engine.EncryptionService().StoreMasterKey(bytes.Repeat([]byte{8}, 32))
	app.mountController = &fakeAppMountController{closeErr: errors.New("eject failed")}

	if err := app.lockEncryptionSession(); err == nil {
		t.Fatal("lockEncryptionSession() error = nil")
	}
	if _, remembered := app.engine.EncryptionService().LoadedMasterKey(); !remembered {
		t.Fatal("mount close failure cleared the encryption key")
	}
}

func TestLockEncryptionSessionClosesMountBeforeClearingKey(t *testing.T) {
	app, _ := setupEncryptionApp(t)
	t.Cleanup(app.engine.Close)
	app.engine.EncryptionService().StoreMasterKey(bytes.Repeat([]byte{8}, 32))
	controller := &fakeAppMountController{}
	app.mountController = controller

	if err := app.lockEncryptionSession(); err != nil {
		t.Fatalf("lockEncryptionSession() error = %v", err)
	}
	if controller.closeCalls != 1 {
		t.Fatalf("mount close calls = %d", controller.closeCalls)
	}
	if _, remembered := app.engine.EncryptionService().LoadedMasterKey(); remembered {
		t.Fatal("encryption key remains loaded after mount closed")
	}
}

type fakeAppMountController struct {
	startStatus        mountcontroller.Status
	status             mountcontroller.Status
	stopStatus         mountcontroller.Status
	startErr           error
	openErr            error
	stopErr            error
	closeErr           error
	startedDrive       mountcontroller.Drive
	startedDrives      []mountcontroller.Drive
	startOptions       mountcontroller.StartOptions
	startCalls         int
	startDrivesCalls   int
	openCalls          int
	stopCalls          int
	closeCalls         int
	startContext       func(context.Context)
	startDrivesContext func(context.Context)
	stopContext        func(context.Context)
}

func (fake *fakeAppMountController) Start(ctx context.Context, drive mountcontroller.Drive, options mountcontroller.StartOptions) (mountcontroller.Status, error) {
	if fake.startContext != nil {
		fake.startContext(ctx)
	}
	fake.startCalls++
	fake.startedDrive = drive
	fake.startOptions = options
	return fake.startStatus, fake.startErr
}

func (fake *fakeAppMountController) StartDrives(ctx context.Context, drives []mountcontroller.Drive, options mountcontroller.StartOptions) (mountcontroller.Status, error) {
	if fake.startDrivesContext != nil {
		fake.startDrivesContext(ctx)
	}
	fake.startDrivesCalls++
	fake.startedDrives = append([]mountcontroller.Drive(nil), drives...)
	fake.startOptions = options
	return fake.startStatus, fake.startErr
}

func (fake *fakeAppMountController) Status() mountcontroller.Status {
	return fake.status
}

func (fake *fakeAppMountController) Open(context.Context) error {
	fake.openCalls++
	return fake.openErr
}

func (fake *fakeAppMountController) Stop(ctx context.Context) (mountcontroller.Status, error) {
	if fake.stopContext != nil {
		fake.stopContext(ctx)
	}
	fake.stopCalls++
	return fake.stopStatus, fake.stopErr
}

func (fake *fakeAppMountController) Close(context.Context) error {
	fake.closeCalls++
	return fake.closeErr
}
