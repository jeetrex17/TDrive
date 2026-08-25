package daemon

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"TDrive/backend"
	"TDrive/backend/core"
	"TDrive/backend/mountcontroller"
	"TDrive/backend/mountos"
	"TDrive/backend/projection"
	"TDrive/backend/tgclient"

	"github.com/gotd/td/telegram"
	_ "modernc.org/sqlite"
)

func TestDaemonMountShutdownBudgetCoversDetachAndEndpointCleanup(t *testing.T) {
	t.Parallel()

	const minimumGracefulCleanup = 25 * time.Second
	if daemonMountShutdownTimeout < minimumGracefulCleanup {
		t.Fatalf(
			"daemon mount shutdown timeout = %s, want at least %s",
			daemonMountShutdownTimeout,
			minimumGracefulCleanup,
		)
	}
}

func TestStartMountPinsSelectedDriveAndReturnsCapabilityFreeStatus(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("APPDATA", t.TempDir())

	const (
		activeDriveID   int64 = 8_100_001
		selectedDriveID int64 = 8_100_002
	)
	engine := newDaemonMountEngine(t, activeDriveID, selectedDriveID)
	connector := &daemonMountConnector{}
	controller, err := mountcontroller.NewWithConnector(engine, connector)
	if err != nil {
		t.Fatalf("create mount controller: %v", err)
	}
	server := &Server{engine: engine, mountController: controller}
	t.Cleanup(func() {
		if err := server.stopMountServer(context.Background()); err != nil {
			t.Errorf("clean up mount: %v", err)
		}
	})

	started, err := server.startMount(context.Background(), fmt.Sprint(selectedDriveID), "q")
	if err != nil {
		t.Fatalf("start selected mount: %v (cause: %v)", err, errors.Unwrap(err))
	}
	if !started.Running || !started.Mounted || started.Drive.ID != selectedDriveID {
		t.Fatalf("started mount = %#v", started)
	}
	if started.WindowsDrive != "Q:" || started.Label != "Tdrive — Pinned Drive" {
		t.Fatalf("mount identity = %#v", started)
	}
	if started.Location != "/Volumes/Tdrive personal" {
		t.Fatalf("mount location = %q", started.Location)
	}
	if got := engine.ActiveChannelID(); got != activeDriveID {
		t.Fatalf("active drive changed to %d, want %d", got, activeDriveID)
	}

	repeated, err := server.startMount(context.Background(), "", "")
	if err != nil {
		t.Fatalf("repeat mount start: %v", err)
	}
	if repeated != started {
		t.Fatalf("repeated mount = %#v, want %#v", repeated, started)
	}
	if got := connector.attachCalls(); got != 1 {
		t.Fatalf("Attach calls = %d, want 1", got)
	}

	if _, err := server.startMount(context.Background(), fmt.Sprint(activeDriveID), ""); !errors.Is(err, mountcontroller.ErrConflict) {
		t.Fatalf("change pinned drive error = %v, want conflict", err)
	}
	if got := engine.ActiveChannelID(); got != activeDriveID {
		t.Fatalf("active drive after conflict = %d, want %d", got, activeDriveID)
	}

	encoded, err := json.Marshal(started)
	if err != nil {
		t.Fatalf("marshal status: %v", err)
	}
	endpoint := connector.attachedEndpoint()
	if endpoint == "" {
		t.Fatal("connector did not receive an endpoint")
	}
	statusJSON := string(encoded)
	if strings.Contains(statusJSON, endpoint) || strings.Contains(statusJSON, "127.0.0.1") || strings.Contains(statusJSON, "/tdrive-") {
		t.Fatalf("mount response leaked endpoint capability: %s", statusJSON)
	}

	stopped, err := server.stopMount(context.Background())
	if err != nil {
		t.Fatalf("stop mount: %v", err)
	}
	if stopped.Running || stopped.Mounted || stopped.Phase != string(mountcontroller.PhaseStopped) {
		t.Fatalf("stopped mount = %#v", stopped)
	}
	if got := connector.detachCalls(); got != 1 {
		t.Fatalf("Detach calls = %d, want 1", got)
	}
}

func TestMountStatusWithoutControllerIsStopped(t *testing.T) {
	response := (*Server)(nil).mountStatus()
	if response.Running || response.Mounted || response.Phase != string(mountcontroller.PhaseStopped) {
		t.Fatalf("mountStatus() = %#v", response)
	}
}

func TestMountStatusOwnsPinnedDriveDuringLifecycle(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		status mountcontroller.Status
		want   bool
	}{
		{name: "stopped", status: mountcontroller.Status{Phase: mountcontroller.PhaseStopped}},
		{name: "failed before attach", status: mountcontroller.Status{Phase: mountcontroller.PhaseFailed, DriveID: 1}},
		{name: "preparing", status: mountcontroller.Status{Phase: mountcontroller.PhasePreparing, DriveID: 1}, want: true},
		{name: "attaching", status: mountcontroller.Status{Phase: mountcontroller.PhaseAttaching, Running: true, DriveID: 1}, want: true},
		{name: "mounted", status: mountcontroller.Status{Phase: mountcontroller.PhaseMounted, Running: true, Mounted: true, DriveID: 1}, want: true},
		{name: "stale OS mount", status: mountcontroller.Status{Phase: mountcontroller.PhaseFailed, Mounted: true, DriveID: 1}, want: true},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if got := mountStatusOwnsPinnedDrive(test.status); got != test.want {
				t.Fatalf("mountStatusOwnsPinnedDrive(%#v) = %t, want %t", test.status, got, test.want)
			}
		})
	}
}

func TestMountResponseMapsOnlySafeControllerFields(t *testing.T) {
	response := mountResponse(mountcontroller.Status{
		Phase:        mountcontroller.PhaseFailed,
		Running:      true,
		Mounted:      true,
		Mode:         "read-only",
		Label:        "Tdrive personal",
		Location:     "/Volumes/Tdrive personal",
		WindowsDrive: "T:",
		Error:        "safe error",
	}, Drive{ID: 42, Title: "Personal", Kind: projection.KindPersonal, Active: true})

	if response.Phase != string(mountcontroller.PhaseFailed) || !response.Running || !response.Mounted {
		t.Fatalf("mountResponse() = %#v", response)
	}
	if response.Drive.ID != 42 || response.Error != "safe error" || response.Location != "/Volumes/Tdrive personal" {
		t.Fatalf("mountResponse() = %#v", response)
	}
}

func newDaemonMountEngine(t *testing.T, activeDriveID, selectedDriveID int64) *core.Engine {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	previousDB := backend.DB
	backend.DB = db
	t.Cleanup(func() {
		backend.DB = previousDB
		_ = db.Close()
	})

	if err := projection.MigratePersonalChannel(db, activeDriveID); err != nil {
		t.Fatalf("migrate active drive: %v", err)
	}
	if err := projection.InsertChannel(db, projection.Channel{
		ChannelID:            selectedDriveID,
		AccessHash:           42,
		Title:                "Pinned Drive",
		Kind:                 projection.KindShared,
		PersonalBackfillDone: true,
	}); err != nil {
		t.Fatalf("insert selected drive: %v", err)
	}

	telegramClient := tgclient.NewFake(1)
	telegramClient.SeedChannel(tgclient.InputPeer{ChannelID: activeDriveID, AccessHash: 41}, "Active Drive")
	telegramClient.SeedChannel(tgclient.InputPeer{ChannelID: selectedDriveID, AccessHash: 42}, "Pinned Drive")
	engine, err := core.New(context.Background(), core.Config{
		TG:         telegramClient,
		SkipDBInit: true,
		Connect: func() (*telegram.Client, error) {
			return nil, nil
		},
	})
	if err != nil {
		t.Fatalf("create engine: %v", err)
	}
	engine.SetActiveChannelID(activeDriveID)
	t.Cleanup(engine.Close)
	return engine
}

type daemonMountConnector struct {
	mu          sync.Mutex
	endpoint    string
	attachCount int
	detachCount int
}

func (connector *daemonMountConnector) Attach(_ context.Context, config mountos.Config) (mountos.Attachment, error) {
	connector.mu.Lock()
	defer connector.mu.Unlock()
	connector.endpoint = config.Endpoint
	connector.attachCount++
	return mountos.NewAttachment(mountos.KindDarwin, "/Volumes/Tdrive personal"), nil
}

func (connector *daemonMountConnector) Detach(context.Context, mountos.Attachment) error {
	connector.mu.Lock()
	defer connector.mu.Unlock()
	connector.detachCount++
	return nil
}

func (*daemonMountConnector) Open(context.Context, mountos.Attachment) error { return nil }

func (connector *daemonMountConnector) attachCalls() int {
	connector.mu.Lock()
	defer connector.mu.Unlock()
	return connector.attachCount
}

func (connector *daemonMountConnector) detachCalls() int {
	connector.mu.Lock()
	defer connector.mu.Unlock()
	return connector.detachCount
}

func (connector *daemonMountConnector) attachedEndpoint() string {
	connector.mu.Lock()
	defer connector.mu.Unlock()
	return connector.endpoint
}
