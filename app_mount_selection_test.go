package main

import (
	"context"
	"errors"
	"slices"
	"testing"

	"TDrive/backend/mountcontroller"
	"TDrive/backend/projection"
)

func TestMountDrivesStartsExactCanonicalSelection(t *testing.T) {
	t.Parallel()

	controller := &fakeAppMountController{startStatus: mountcontroller.Status{
		Phase:      mountcontroller.PhaseMounted,
		Mounted:    true,
		Mode:       mountcontroller.ModeReadWrite,
		WriteState: mountcontroller.WriteStateReady,
		Label:      "Tdrive",
	}}
	app := &App{
		ctx:             context.Background(),
		mountController: controller,
		mountDrivesResolver: func(ids []int64) ([]mountcontroller.Drive, error) {
			if !slices.Equal(ids, []int64{11, 22}) {
				t.Fatalf("resolver ids = %v", ids)
			}
			return []mountcontroller.Drive{
				{ID: 11, Kind: mountcontroller.DriveKindPersonal},
				{ID: 22, Title: "Team", Kind: mountcontroller.DriveKindShared},
			}, nil
		},
	}

	view, err := app.MountDrives([]int64{11, 22})
	if err != nil {
		t.Fatalf("MountDrives() error = %v", err)
	}
	if controller.startDrivesCalls != 1 || len(controller.startedDrives) != 2 {
		t.Fatalf("StartDrives calls=%d drives=%#v", controller.startDrivesCalls, controller.startedDrives)
	}
	if view.Label != "Tdrive" || !view.Mounted || view.Mode != "read-write" {
		t.Fatalf("MountDrives() = %#v", view)
	}
}

func TestResolveMountDrivesUsesCanonicalRowsAndSkipsUnselectedPersonalPolicy(t *testing.T) {
	refreshCalls := 0
	app, db, _ := setupEncryptionAppWithPolicyRefresh(t, func(context.Context, int64) error {
		refreshCalls++
		return errors.New("personal policy must not be read")
	})
	t.Cleanup(app.engine.Close)
	for _, channel := range []projection.Channel{
		{ChannelID: 202, AccessHash: 2, Title: "Later", Kind: projection.KindShared, JoinedAt: 20, PersonalBackfillDone: true},
		{ChannelID: 101, AccessHash: 1, Title: "Earlier", Kind: projection.KindShared, JoinedAt: 10, PersonalBackfillDone: true},
	} {
		if err := projection.InsertChannel(db, channel); err != nil {
			t.Fatalf("InsertChannel(%d) error = %v", channel.ChannelID, err)
		}
	}

	drives, err := app.resolveMountDrives([]int64{202, 101})
	if err != nil {
		t.Fatalf("resolveMountDrives() error = %v", err)
	}
	if len(drives) != 2 || drives[0].ID != 101 || drives[0].Title != "Earlier" || drives[1].ID != 202 {
		t.Fatalf("canonical selection = %#v", drives)
	}
	if refreshCalls != 0 {
		t.Fatalf("shared-only selection refreshed personal policy %d times", refreshCalls)
	}
}

func TestResolveMountDrivesRejectsInvalidSelections(t *testing.T) {
	app, _, _ := setupEncryptionAppWithPolicyRefresh(t, func(context.Context, int64) error { return nil })
	t.Cleanup(app.engine.Close)

	for name, ids := range map[string][]int64{
		"empty":     nil,
		"zero":      {0},
		"negative":  {-1},
		"duplicate": {7, 7},
		"unknown":   {999999},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := app.resolveMountDrives(ids); err == nil {
				t.Fatalf("resolveMountDrives(%v) error = nil", ids)
			}
		})
	}
}

func TestMountDrivesResolverFailureDoesNotStartController(t *testing.T) {
	t.Parallel()

	sentinel := errors.New("invalid selection")
	controller := &fakeAppMountController{}
	app := &App{
		ctx:             context.Background(),
		mountController: controller,
		mountDrivesResolver: func([]int64) ([]mountcontroller.Drive, error) {
			return nil, sentinel
		},
	}

	if _, err := app.MountDrives(nil); !errors.Is(err, sentinel) {
		t.Fatalf("MountDrives() error = %v, want sentinel", err)
	}
	if controller.startDrivesCalls != 0 || controller.startCalls != 0 {
		t.Fatalf("invalid selection started controller: aggregate=%d single=%d", controller.startDrivesCalls, controller.startCalls)
	}
}
