package main

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"TDrive/backend/mountcontroller"
	"TDrive/backend/projection"
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

func TestAppMountDriveReadOnlyIsExplicitFallback(t *testing.T) {
	t.Parallel()

	controller := &fakeAppMountController{startStatus: mountcontroller.Status{
		Phase:      mountcontroller.PhaseMounted,
		Mounted:    true,
		Mode:       mountcontroller.ModeReadOnly,
		WriteState: mountcontroller.WriteStateDisabled,
	}}
	app := &App{
		ctx:             context.Background(),
		mountController: controller,
		mountDriveResolver: func() (mountcontroller.Drive, error) {
			return mountcontroller.Drive{ID: 42, Title: "My Drive", Kind: mountcontroller.DriveKindPersonal}, nil
		},
	}

	view, err := app.MountDriveReadOnly()
	if err != nil || view.Mode != "read-only" {
		t.Fatalf("MountDriveReadOnly() = (%#v, %v)", view, err)
	}
	if controller.startOptions.Mode != mountcontroller.ModeReadOnly {
		t.Fatalf("read-only app mode = %q", controller.startOptions.Mode)
	}
}

func TestResolveActiveMountDrivePropagatesEncryptedEligibility(t *testing.T) {
	app, db := setupEncryptionApp(t)
	t.Cleanup(app.engine.Close)
	app.engine.SetActiveChannelID(testEncryptionChannelID)
	if err := projection.PutEncryptionConfig(db, projection.EncryptionConfig{
		ChannelID:        testEncryptionChannelID,
		Enabled:          true,
		KDFSalt:          []byte("salt"),
		KDFParamsJSON:    `{}`,
		WrappedMasterKey: []byte("wrapped"),
		KeyCheck:         []byte("check"),
	}); err != nil {
		t.Fatalf("seed encryption config: %v", err)
	}

	drive, err := app.resolveActiveMountDrive()
	if err != nil {
		t.Fatalf("resolveActiveMountDrive() error = %v", err)
	}
	if !drive.Encrypted {
		t.Fatalf("resolveActiveMountDrive() = %#v, want encrypted eligibility", drive)
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

type fakeAppMountController struct {
	startStatus  mountcontroller.Status
	status       mountcontroller.Status
	stopStatus   mountcontroller.Status
	startErr     error
	openErr      error
	stopErr      error
	closeErr     error
	startedDrive mountcontroller.Drive
	startOptions mountcontroller.StartOptions
	startCalls   int
	openCalls    int
	stopCalls    int
	closeCalls   int
}

func (fake *fakeAppMountController) Start(_ context.Context, drive mountcontroller.Drive, options mountcontroller.StartOptions) (mountcontroller.Status, error) {
	fake.startCalls++
	fake.startedDrive = drive
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

func (fake *fakeAppMountController) Stop(context.Context) (mountcontroller.Status, error) {
	fake.stopCalls++
	return fake.stopStatus, fake.stopErr
}

func (fake *fakeAppMountController) Close(context.Context) error {
	fake.closeCalls++
	return fake.closeErr
}
