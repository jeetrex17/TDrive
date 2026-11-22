package app

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"TDrive/backend/datadir"
	"TDrive/backend/mountcontroller"
)

func useMountLetterDataDir(t *testing.T) string {
	t.Helper()
	datadir.Set(t.TempDir())
	t.Cleanup(func() { datadir.Set("") })
	dir, err := datadir.Dir()
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestMountDriveLetterDefaultsAndPersists(t *testing.T) {
	useMountLetterDataDir(t)
	app := &App{}

	view, err := app.GetMountDriveLetter()
	if err != nil || view.Letter != "T:" {
		t.Fatalf("GetMountDriveLetter() = %#v, %v; want T:", view, err)
	}
	view, err = app.SetMountDriveLetter(" q ")
	if err != nil || view.Letter != "Q:" {
		t.Fatalf("SetMountDriveLetter(q) = %#v, %v; want Q:", view, err)
	}
	view, err = app.GetMountDriveLetter()
	if err != nil || view.Letter != "Q:" {
		t.Fatalf("GetMountDriveLetter() after save = %#v, %v; want Q:", view, err)
	}
}

func TestSetMountDriveLetterRejectsInvalidLetters(t *testing.T) {
	dir := useMountLetterDataDir(t)
	app := &App{}

	for _, input := range []string{"1", "QQ", "Q:/", `Q:\`, "é"} {
		if _, err := app.SetMountDriveLetter(input); err == nil {
			t.Errorf("SetMountDriveLetter(%q) succeeded, want an error", input)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, mountDriveLetterFilename)); !os.IsNotExist(err) {
		t.Fatalf("an invalid letter was saved: stat error = %v", err)
	}
}

func TestMountUsesSavedDriveLetter(t *testing.T) {
	useMountLetterDataDir(t)
	app := &App{ctx: context.Background()}
	if _, err := app.SetMountDriveLetter("M:"); err != nil {
		t.Fatal(err)
	}
	controller := &fakeAppMountController{startStatus: mountcontroller.Status{Phase: mountcontroller.PhaseMounted, Mounted: true}}
	app.mountController = controller
	app.mountDriveResolver = func() (mountcontroller.Drive, error) {
		return mountcontroller.Drive{ID: 7, Kind: mountcontroller.DriveKindPersonal}, nil
	}
	app.mountDrivesResolver = func([]int64) ([]mountcontroller.Drive, error) {
		return []mountcontroller.Drive{{ID: 7, Kind: mountcontroller.DriveKindPersonal}}, nil
	}

	if _, err := app.mountDriveNative(); err != nil {
		t.Fatal(err)
	}
	if got := controller.startOptions; got.WindowsDrive != "M:" || got.Mode != mountcontroller.ModeAuto {
		t.Fatalf("MountDrive start options = %#v, want M: in auto mode", got)
	}
	if _, err := app.mountDrivesNative([]int64{7}); err != nil {
		t.Fatal(err)
	}
	if got := controller.startOptions; got.WindowsDrive != "M:" {
		t.Fatalf("MountDrives start options = %#v, want M:", got)
	}
}

func TestMountFallsBackToDefaultLetterWhenPreferenceIsDamaged(t *testing.T) {
	dir := useMountLetterDataDir(t)
	if err := os.WriteFile(filepath.Join(dir, mountDriveLetterFilename), []byte(`{"windows_drive":"??"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := mountStartOptions().WindowsDrive; got != "" {
		t.Fatalf("damaged preference WindowsDrive = %q, want the controller default", got)
	}
}
