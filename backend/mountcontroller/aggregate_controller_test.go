package mountcontroller

import (
	"context"
	"errors"
	"sync"
	"testing"

	"TDrive/backend/mountfs"
	"TDrive/backend/mountos"
)

func TestStartDrivesBuildsOneMixedModeAggregateMount(t *testing.T) {
	t.Parallel()

	filesystems := newSelectionFilesystemBuilder(t)
	writes := &recordingAggregateWriter{}
	writers := &fakeWriterBuilder{session: writes}
	endpoint := &fakeEndpoint{endpoint: testEndpoint}
	connector := &fakeConnector{}
	controller := newTestController(t, Dependencies{
		Filesystems: filesystems,
		Writers:     writers,
		Endpoint:    endpoint,
		Connector:   connector,
	})

	status, err := controller.StartDrives(context.Background(), []Drive{
		{ID: 22, Title: "Team", Kind: DriveKindShared},
		personalDrive(),
	}, StartOptions{})
	if err != nil {
		t.Fatalf("StartDrives() error = %v", err)
	}
	if status.Label != "Tdrive" || status.Mode != ModeReadWrite || !status.Mounted || !status.AcceptingWrites {
		t.Fatalf("aggregate status = %#v", status)
	}
	if got := filesystems.buildIDs(); len(got) != 2 || got[0] != personalDrive().ID || got[1] != 22 {
		t.Fatalf("filesystem builds = %v", got)
	}
	if writers.buildCalls != 1 || writers.drive.ID != personalDrive().ID {
		t.Fatalf("writer builds = %d for %#v", writers.buildCalls, writers.drive)
	}
	aggregate, ok := endpoint.config.FS.(*mountfs.Aggregate)
	if !ok {
		t.Fatalf("endpoint filesystem = %T, want *mountfs.Aggregate", endpoint.config.FS)
	}
	entries, err := aggregate.ReadDir(context.Background(), "/")
	if err != nil {
		t.Fatalf("aggregate ReadDir(root) error = %v", err)
	}
	if len(entries) != 2 || entries[0].Name != "Personal" || entries[1].Name != "Shared — Team" {
		t.Fatalf("aggregate roots = %#v", entries)
	}
	if endpoint.startCalls != 1 || connector.attachCalls != 1 || connector.config.Mode != mountos.ModeReadWrite {
		t.Fatalf("mount calls endpoint=%d attach=%d config=%#v", endpoint.startCalls, connector.attachCalls, connector.config)
	}
	if _, err := controller.Stop(context.Background()); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
	if filesystems.totalCloseCalls() != 2 || writes.drainCalls != 1 || writes.closeCalls != 1 {
		t.Fatalf("cleanup content=%d drain=%d writer-close=%d", filesystems.totalCloseCalls(), writes.drainCalls, writes.closeCalls)
	}
}

func TestStartDrivesSharedOnlyIsReadOnly(t *testing.T) {
	t.Parallel()

	filesystems := newSelectionFilesystemBuilder(t)
	writers := &fakeWriterBuilder{session: &recordingAggregateWriter{}}
	endpoint := &fakeEndpoint{endpoint: testEndpoint}
	connector := &fakeConnector{}
	controller := newTestController(t, Dependencies{
		Filesystems: filesystems,
		Writers:     writers,
		Endpoint:    endpoint,
		Connector:   connector,
	})

	status, err := controller.StartDrives(context.Background(), []Drive{
		{ID: 31, Title: "Work", Kind: DriveKindShared},
		{ID: 32, Title: "Friends", Kind: DriveKindShared},
	}, StartOptions{})
	if err != nil {
		t.Fatalf("StartDrives() error = %v", err)
	}
	if status.Mode != ModeReadOnly || status.Label != "Tdrive" || writers.buildCalls != 0 {
		t.Fatalf("shared-only mount = status:%#v writer-builds:%d", status, writers.buildCalls)
	}
	if endpoint.config.Writer != nil || connector.config.Mode != mountos.ModeReadOnly {
		t.Fatalf("shared-only config endpoint=%#v connector=%#v", endpoint.config, connector.config)
	}
}

func TestStartDrivesLockedPersonalFailsBeforeSideEffects(t *testing.T) {
	t.Parallel()

	filesystems := newSelectionFilesystemBuilder(t)
	endpoint := &fakeEndpoint{endpoint: testEndpoint}
	connector := &fakeConnector{}
	controller := newTestController(t, Dependencies{
		Filesystems: filesystems,
		Writers:     &fakeWriterBuilder{session: &recordingAggregateWriter{}},
		Endpoint:    endpoint,
		Connector:   connector,
	})

	_, err := controller.StartDrives(context.Background(), []Drive{
		{ID: 11, Kind: DriveKindPersonal, Encrypted: true},
		{ID: 22, Title: "Team", Kind: DriveKindShared},
	}, StartOptions{})
	if !errors.Is(err, ErrEncryptionPasswordRequired) {
		t.Fatalf("StartDrives() error = %v, want ErrEncryptionPasswordRequired", err)
	}
	if len(filesystems.buildIDs()) != 0 || endpoint.startCalls != 0 || connector.attachCalls != 0 {
		t.Fatalf("locked selection caused side effects: builds=%v endpoint=%d attach=%d", filesystems.buildIDs(), endpoint.startCalls, connector.attachCalls)
	}
}

func TestStartDrivesRollbackClosesEarlierResources(t *testing.T) {
	t.Parallel()

	filesystems := newSelectionFilesystemBuilder(t)
	filesystems.failDrive = 22
	lease := &fakeMountKeyLease{key: make([]byte, 32)}
	endpoint := &fakeEndpoint{endpoint: testEndpoint}
	controller := newTestController(t, Dependencies{
		Filesystems: filesystems,
		Writers:     &fakeWriterBuilder{session: &recordingAggregateWriter{}},
		Keys:        &fakeMountKeyLeaser{lease: lease},
		Endpoint:    endpoint,
		Connector:   &fakeConnector{},
	})

	_, err := controller.StartDrives(context.Background(), []Drive{
		{ID: 11, Kind: DriveKindPersonal, Encrypted: true, EncryptionUnlocked: true},
		{ID: 22, Title: "Team", Kind: DriveKindShared},
	}, StartOptions{})
	if err == nil || !errors.Is(err, ErrStartFailed) {
		t.Fatalf("StartDrives() error = %v, want ErrStartFailed", err)
	}
	if filesystems.totalCloseCalls() != 1 || lease.closeCalls != 1 {
		t.Fatalf("rollback cleanup content=%d key=%d", filesystems.totalCloseCalls(), lease.closeCalls)
	}
	if endpoint.startCalls != 0 {
		t.Fatalf("endpoint started after partial build: %d", endpoint.startCalls)
	}
}

type selectionFilesystemBuilder struct {
	t         *testing.T
	mu        sync.Mutex
	built     []int64
	contents  map[int64]*fakeContent
	failDrive int64
}

func newSelectionFilesystemBuilder(t *testing.T) *selectionFilesystemBuilder {
	return &selectionFilesystemBuilder{t: t, contents: make(map[int64]*fakeContent)}
}

func (builder *selectionFilesystemBuilder) Build(
	_ context.Context,
	channelID int64,
	_ mountfs.Options,
	_ MountKeyLease,
) (*mountfs.FS, ContentLifetime, error) {
	builder.mu.Lock()
	builder.built = append(builder.built, channelID)
	if channelID == builder.failDrive {
		builder.mu.Unlock()
		return nil, nil, errors.New("build failed")
	}
	content := &fakeContent{}
	builder.contents[channelID] = content
	builder.mu.Unlock()
	filesystem, err := mountfs.New(channelID, emptyDirectorySource{}, emptyContentOpener{})
	if err != nil {
		builder.t.Fatalf("mountfs.New(%d) error = %v", channelID, err)
	}
	return filesystem, content, nil
}

func (builder *selectionFilesystemBuilder) buildIDs() []int64 {
	builder.mu.Lock()
	defer builder.mu.Unlock()
	return append([]int64(nil), builder.built...)
}

func (builder *selectionFilesystemBuilder) totalCloseCalls() int {
	builder.mu.Lock()
	defer builder.mu.Unlock()
	total := 0
	for _, content := range builder.contents {
		content.mu.Lock()
		total += content.closeCalls
		content.mu.Unlock()
	}
	return total
}
