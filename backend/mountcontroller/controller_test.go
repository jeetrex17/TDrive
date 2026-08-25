package mountcontroller

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"

	"TDrive/backend/mountfs"
	"TDrive/backend/mountos"
)

func TestDisplayLabel(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		drive Drive
		want  string
	}{
		{
			name:  "personal title is never exposed",
			drive: Drive{ID: 1, Title: "Telegram Saved Messages", Kind: DriveKindPersonal},
			want:  "Tdrive personal",
		},
		{
			name:  "shared title",
			drive: Drive{ID: 2, Title: "Family files", Kind: DriveKindShared},
			want:  "Tdrive — Family files",
		},
		{
			name:  "unsafe shared title",
			drive: Drive{ID: 3, Title: "  Family\n/ \x00\\ archive  ", Kind: DriveKindShared},
			want:  "Tdrive — Family archive",
		},
		{
			name:  "empty shared title",
			drive: Drive{ID: 4, Title: "\x00/\\", Kind: DriveKindShared},
			want:  "Tdrive — Shared drive",
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			got, err := displayLabel(test.drive)
			if err != nil {
				t.Fatalf("displayLabel() error = %v", err)
			}
			if got != test.want {
				t.Fatalf("displayLabel() = %q, want %q", got, test.want)
			}
			if len([]rune(got)) > maxDisplayLabelRunes || len(got) > maxDisplayLabelBytes {
				t.Fatalf("displayLabel() returned an unbounded label: runes=%d bytes=%d", len([]rune(got)), len(got))
			}
		})
	}
}

func TestDisplayLabelBoundsLongUTF8Title(t *testing.T) {
	t.Parallel()

	label, err := displayLabel(Drive{
		ID:    1,
		Kind:  DriveKindShared,
		Title: strings.Repeat("🗂️資料", 100),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len([]rune(label)) > maxDisplayLabelRunes || len(label) > maxDisplayLabelBytes {
		t.Fatalf("label is unbounded: runes=%d bytes=%d", len([]rune(label)), len(label))
	}
	if !strings.HasPrefix(label, "Tdrive — ") {
		t.Fatalf("label = %q", label)
	}
}

func TestStartPublishesTransitionsAndMountSpecificCacheOptions(t *testing.T) {
	t.Parallel()

	events := &eventLog{}
	buildEntered := make(chan struct{})
	buildRelease := make(chan struct{})
	attachEntered := make(chan struct{})
	attachRelease := make(chan struct{})
	builder := &fakeFilesystemBuilder{
		events:  events,
		entered: buildEntered,
		release: buildRelease,
		content: &fakeContent{events: events},
	}
	endpoint := &fakeEndpoint{events: events, endpoint: testEndpoint}
	connector := &fakeConnector{
		events:        events,
		attachEntered: attachEntered,
		attachRelease: attachRelease,
	}
	controller := newTestController(t, Dependencies{
		Filesystems: builder,
		Endpoint:    endpoint,
		Connector:   connector,
	})

	result := make(chan startResult, 1)
	go func() {
		status, err := controller.Start(context.Background(), personalDrive(), StartOptions{})
		result <- startResult{status: status, err: err}
	}()

	<-buildEntered
	assertStatus(t, controller.Status(), PhasePreparing, false, false)
	close(buildRelease)
	<-attachEntered
	assertStatus(t, controller.Status(), PhaseAttaching, true, false)
	close(attachRelease)

	started := <-result
	if started.err != nil {
		t.Fatalf("Start() error = %v", started.err)
	}
	assertStatus(t, started.status, PhaseMounted, true, true)
	if started.status.Label != "Tdrive personal" || started.status.Mode != readOnlyMode || started.status.WindowsDrive != defaultWindowsDrive {
		t.Fatalf("Start() status = %#v", started.status)
	}
	if builder.options.SnapshotTTL != defaultMountSnapshotTTL {
		t.Fatalf("SnapshotTTL = %v, want %v", builder.options.SnapshotTTL, defaultMountSnapshotTTL)
	}
	if connector.config.Endpoint != testEndpoint || connector.config.Label != "Tdrive personal" || connector.config.WindowsDrive != defaultWindowsDrive {
		t.Fatalf("connector config = %#v", connector.config)
	}
	if connector.config.Mode != mountos.ModeReadOnly {
		t.Fatalf("connector mode = %q, want read-only", connector.config.Mode)
	}
}

func TestStartDefaultsToWritableOnlyForEligiblePersonalPlaintextDrive(t *testing.T) {
	t.Parallel()

	writes := &fakeWriterSession{}
	builders := &fakeWriterBuilder{session: writes}
	endpoint := &fakeEndpoint{endpoint: testEndpoint}
	connector := &fakeConnector{}
	controller := newTestController(t, Dependencies{
		Filesystems: &fakeFilesystemBuilder{content: &fakeContent{}},
		Writers:     builders,
		Endpoint:    endpoint,
		Connector:   connector,
	})

	status, err := controller.Start(context.Background(), personalDrive(), StartOptions{})
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if status.Mode != ModeReadWrite || status.WriteState != WriteStateReady {
		t.Fatalf("writable status = %#v", status)
	}
	if builders.buildCalls != 1 || builders.drive.ID != personalDrive().ID {
		t.Fatalf("writer build = calls:%d drive:%#v", builders.buildCalls, builders.drive)
	}
	if builders.fs == nil || builders.fs != endpoint.config.FS {
		t.Fatal("writer builder did not receive the mounted filesystem instance")
	}
	if endpoint.config.Writer != writes || endpoint.config.Mode != ModeReadWrite {
		t.Fatalf("endpoint config = %#v", endpoint.config)
	}
	if connector.config.Mode != mountos.ModeReadWrite {
		t.Fatalf("connector mode = %q, want read-write", connector.config.Mode)
	}
}

func TestStartFallsBackToReadOnlyForIneligibleDrive(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		drive Drive
	}{
		{name: "shared", drive: Drive{ID: 20, Title: "Shared", Kind: DriveKindShared}},
		{name: "encrypted personal", drive: Drive{ID: 21, Title: "Private", Kind: DriveKindPersonal, Encrypted: true}},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			builders := &fakeWriterBuilder{session: &fakeWriterSession{}}
			endpoint := &fakeEndpoint{endpoint: testEndpoint}
			connector := &fakeConnector{}
			controller := newTestController(t, Dependencies{
				Filesystems: &fakeFilesystemBuilder{content: &fakeContent{}},
				Writers:     builders,
				Endpoint:    endpoint,
				Connector:   connector,
			})

			status, err := controller.Start(context.Background(), test.drive, StartOptions{})
			if err != nil {
				t.Fatalf("Start() error = %v", err)
			}
			if status.Mode != ModeReadOnly || status.WriteState != WriteStateDisabled {
				t.Fatalf("fallback status = %#v", status)
			}
			if builders.buildCalls != 0 || endpoint.config.Writer != nil || connector.config.Mode != mountos.ModeReadOnly {
				t.Fatalf("read-only fallback built writer or attached writable: builds=%d endpoint=%#v connector=%#v", builders.buildCalls, endpoint.config, connector.config)
			}
		})
	}
}

func TestStartHonorsReadOnlyOverrideAndRejectsUnavailableWritableRequest(t *testing.T) {
	t.Parallel()

	builders := &fakeWriterBuilder{session: &fakeWriterSession{}}
	controller := newTestController(t, Dependencies{
		Filesystems: &fakeFilesystemBuilder{content: &fakeContent{}},
		Writers:     builders,
		Endpoint:    &fakeEndpoint{endpoint: testEndpoint},
		Connector:   &fakeConnector{},
	})
	status, err := controller.Start(context.Background(), personalDrive(), StartOptions{Mode: ModeReadOnly})
	if err != nil || status.Mode != ModeReadOnly || builders.buildCalls != 0 {
		t.Fatalf("read-only override = (%#v, %v), builds=%d", status, err, builders.buildCalls)
	}
	if _, err := controller.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}

	shared := Drive{ID: 33, Title: "Shared", Kind: DriveKindShared}
	if _, err := controller.Start(context.Background(), shared, StartOptions{Mode: ModeReadWrite}); !errors.Is(err, ErrWritableUnavailable) {
		t.Fatalf("explicit ineligible writable error = %v, want ErrWritableUnavailable", err)
	}
	if _, err := controller.Start(context.Background(), personalDrive(), StartOptions{Mode: "invalid"}); !errors.Is(err, ErrInvalidMode) {
		t.Fatalf("invalid mode error = %v, want ErrInvalidMode", err)
	}
}

func TestConcurrentEquivalentStartsCoalesceAndConflictIsTyped(t *testing.T) {
	t.Parallel()

	attachEntered := make(chan struct{})
	attachRelease := make(chan struct{})
	endpoint := &fakeEndpoint{endpoint: testEndpoint}
	connector := &fakeConnector{attachEntered: attachEntered, attachRelease: attachRelease}
	controller := newTestController(t, Dependencies{
		Filesystems: &fakeFilesystemBuilder{content: &fakeContent{}},
		Endpoint:    endpoint,
		Connector:   connector,
	})

	first := make(chan startResult, 1)
	second := make(chan startResult, 1)
	go func() {
		status, err := controller.Start(context.Background(), personalDrive(), StartOptions{WindowsDrive: "t"})
		first <- startResult{status: status, err: err}
	}()
	<-attachEntered
	go func() {
		status, err := controller.Start(context.Background(), personalDrive(), StartOptions{WindowsDrive: "T:"})
		second <- startResult{status: status, err: err}
	}()

	_, err := controller.Start(context.Background(), Drive{ID: 22, Title: "Other", Kind: DriveKindShared}, StartOptions{})
	var conflict *ConflictError
	if !errors.As(err, &conflict) || !errors.Is(err, ErrConflict) {
		t.Fatalf("conflicting Start() error = %v, want *ConflictError", err)
	}
	if conflict.ActiveDriveID != personalDrive().ID || conflict.RequestedDriveID != 22 {
		t.Fatalf("ConflictError = %#v", conflict)
	}

	close(attachRelease)
	for index, result := range []startResult{<-first, <-second} {
		if result.err != nil || result.status.Phase != PhaseMounted {
			t.Fatalf("start result %d = (%#v, %v)", index, result.status, result.err)
		}
	}
	if endpoint.startCalls != 1 || connector.attachCalls != 1 {
		t.Fatalf("start calls: endpoint=%d connector=%d", endpoint.startCalls, connector.attachCalls)
	}

	_, err = controller.Start(context.Background(), personalDrive(), StartOptions{WindowsDrive: "Q:"})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("drive-letter conflict error = %v, want ErrConflict", err)
	}
}

func TestStartAttachFailureRollsBackAndSanitizesPublicErrors(t *testing.T) {
	t.Parallel()

	events := &eventLog{}
	content := &fakeContent{events: events}
	endpoint := &fakeEndpoint{events: events, endpoint: testEndpoint}
	secretError := errors.New("failed for " + testEndpoint)
	connector := &fakeConnector{events: events, attachErr: secretError}
	controller := newTestController(t, Dependencies{
		Filesystems: &fakeFilesystemBuilder{events: events, content: content},
		Endpoint:    endpoint,
		Connector:   connector,
	})

	status, err := controller.Start(context.Background(), personalDrive(), StartOptions{})
	if err == nil {
		t.Fatal("Start() error = nil")
	}
	if !errors.Is(err, ErrStartFailed) || errors.Unwrap(err) != nil {
		t.Fatalf("Start() error classification = %v, unwrap = %v", err, errors.Unwrap(err))
	}
	assertCapabilityFree(t, err.Error())
	assertCapabilityFree(t, status.Error)
	assertStatus(t, status, PhaseFailed, false, false)
	if got := events.snapshot(); strings.Join(got, ",") != "build,endpoint.start,connector.attach,endpoint.stop,content.close" {
		t.Fatalf("rollback order = %v", got)
	}
	if !content.closed {
		t.Fatal("content was not closed")
	}
	payload, marshalErr := json.Marshal(status)
	if marshalErr != nil {
		t.Fatal(marshalErr)
	}
	assertCapabilityFree(t, string(payload))
}

func TestStartReturnsOnlySafePlatformAttachGuidance(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		cause          error
		want           string
		classification error
	}{
		{
			name:           "occupied drive",
			cause:          mountos.ErrDriveOccupied,
			want:           "Windows drive T: is already in use. Free it and try again.",
			classification: mountos.ErrDriveOccupied,
		},
		{
			name:           "WebDAV unavailable",
			cause:          mountos.ErrWindowsWebDAVUnavailable,
			want:           "Windows WebDAV is unavailable. Start or enable the WebClient service, then try again.",
			classification: mountos.ErrWindowsWebDAVUnavailable,
		},
		{
			name:           "Linux desktop unavailable",
			cause:          errors.Join(mountos.ErrLinuxDesktopUnavailable, errors.New(testEndpoint)),
			want:           "Linux desktop mounting is unavailable. Run TDrive inside a graphical desktop session with GIO and GVfs available, then try again.",
			classification: mountos.ErrLinuxDesktopUnavailable,
		},
		{
			name:           "Linux WebDAV unavailable",
			cause:          errors.Join(mountos.ErrLinuxWebDAVUnavailable, errors.New(testEndpoint)),
			want:           "Linux WebDAV mounting is unavailable. Enable the GIO/GVfs WebDAV backend for your desktop session, then try again.",
			classification: mountos.ErrLinuxWebDAVUnavailable,
		},
		{
			name:           "cross-platform verification failure",
			cause:          mountos.ErrVerificationFailed,
			want:           "TDrive could not attach the read-only mount",
			classification: ErrStartFailed,
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			controller := newTestController(t, Dependencies{
				Filesystems: &fakeFilesystemBuilder{content: &fakeContent{}},
				Endpoint:    &fakeEndpoint{endpoint: testEndpoint},
				Connector:   &fakeConnector{attachErr: test.cause},
			})

			status, err := controller.Start(context.Background(), personalDrive(), StartOptions{})
			if err == nil || !errors.Is(err, test.classification) {
				t.Fatalf("Start() error = %v, want classification %v", err, test.classification)
			}
			if err.Error() != test.want || status.Error != test.want {
				t.Fatalf("safe error = %q / %q, want %q", err, status.Error, test.want)
			}
			assertCapabilityFree(t, err.Error())
			assertCapabilityFree(t, status.Error)
		})
	}
}

func TestStartPreservesCancellationWithoutPublishingCapability(t *testing.T) {
	t.Parallel()

	controller := newTestController(t, Dependencies{
		Filesystems: &fakeFilesystemBuilder{content: &fakeContent{}},
		Endpoint:    &fakeEndpoint{endpoint: testEndpoint},
		Connector: &fakeConnector{attachErr: errors.Join(
			context.Canceled,
			errors.New("canceled while attaching "+testEndpoint),
		)},
	})

	status, err := controller.Start(context.Background(), personalDrive(), StartOptions{})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Start(canceled attach) = %v; want context canceled", err)
	}
	assertCapabilityFree(t, err.Error())
	assertCapabilityFree(t, status.Error)
	assertStatus(t, status, PhaseFailed, false, false)
	payload, marshalErr := json.Marshal(status)
	if marshalErr != nil {
		t.Fatal(marshalErr)
	}
	assertCapabilityFree(t, string(payload))
}

func TestStartKeepsWindowsAttachCauseWhenRollbackAlsoFails(t *testing.T) {
	t.Parallel()

	controller := newTestController(t, Dependencies{
		Filesystems: &fakeFilesystemBuilder{content: &fakeContent{}},
		Endpoint: &fakeEndpoint{
			endpoint: testEndpoint,
			stopErr:  context.Canceled,
		},
		Connector: &fakeConnector{attachErr: mountos.ErrWindowsWebDAVUnavailable},
	})

	status, err := controller.Start(context.Background(), personalDrive(), StartOptions{})
	if err == nil || !errors.Is(err, mountos.ErrWindowsWebDAVUnavailable) {
		t.Fatalf("Start() error = %v, want Windows WebDAV unavailable", err)
	}
	const want = "Windows WebDAV is unavailable. Start or enable the WebClient service, then try again."
	if err.Error() != want || status.Error != want {
		t.Fatalf("safe error = %q / %q, want %q", err, status.Error, want)
	}
}

func TestStartRollsBackWhenEndpointDiesDuringAttachment(t *testing.T) {
	t.Parallel()

	events := &eventLog{}
	content := &fakeContent{events: events}
	endpoint := &fakeEndpoint{events: events, endpoint: testEndpoint, healthDown: true}
	connector := &fakeConnector{events: events}
	controller := newTestController(t, Dependencies{
		Filesystems: &fakeFilesystemBuilder{events: events, content: content},
		Endpoint:    endpoint,
		Connector:   connector,
	})

	status, err := controller.Start(context.Background(), personalDrive(), StartOptions{})
	if err == nil {
		t.Fatal("Start() error = nil")
	}
	assertStatus(t, status, PhaseFailed, false, false)
	assertCapabilityFree(t, err.Error())
	if got := events.snapshot(); strings.Join(got, ",") != "build,endpoint.start,connector.attach,connector.detach,endpoint.stop,content.close" {
		t.Fatalf("dead-endpoint rollback order = %v", got)
	}
}

func TestStartPreservesStaleMountWhenDeadEndpointRollbackCannotDetach(t *testing.T) {
	t.Parallel()

	content := &fakeContent{}
	endpoint := &fakeEndpoint{endpoint: testEndpoint, healthDown: true}
	connector := &fakeConnector{detachErr: errors.New("busy " + testEndpoint)}
	controller := newTestController(t, Dependencies{
		Filesystems: &fakeFilesystemBuilder{content: content},
		Endpoint:    endpoint,
		Connector:   connector,
	})

	status, err := controller.Start(context.Background(), personalDrive(), StartOptions{})
	if err == nil {
		t.Fatal("Start() error = nil")
	}
	assertStatus(t, status, PhaseFailed, false, true)
	assertCapabilityFree(t, status.Error)
	if endpoint.stopCalls != 0 || content.closed {
		t.Fatalf("stale mount resources were released: endpoint stops=%d content.closed=%v", endpoint.stopCalls, content.closed)
	}

	connector.detachErr = nil
	if status, err = controller.Stop(context.Background()); err != nil || status.Phase != PhaseStopped {
		t.Fatalf("Stop() stale mount = (%#v, %v)", status, err)
	}
}

func TestStartRejectsIncompleteFilesystemResources(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		builder *fakeFilesystemBuilder
	}{
		{
			name: "nil filesystem",
			builder: &fakeFilesystemBuilder{
				nilFS:   true,
				content: &fakeContent{},
			},
		},
		{
			name:    "nil content lifetime",
			builder: &fakeFilesystemBuilder{},
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			endpoint := &fakeEndpoint{endpoint: testEndpoint}
			controller := newTestController(t, Dependencies{
				Filesystems: test.builder,
				Endpoint:    endpoint,
				Connector:   &fakeConnector{},
			})

			status, err := controller.Start(context.Background(), personalDrive(), StartOptions{})
			if err == nil || !errors.Is(err, ErrInvalidConfiguration) {
				t.Fatalf("Start() error = %v, want ErrInvalidConfiguration", err)
			}
			assertStatus(t, status, PhaseFailed, false, false)
			if endpoint.startCalls != 0 {
				t.Fatalf("endpoint Start calls = %d, want 0", endpoint.startCalls)
			}
			if content, ok := test.builder.content.(*fakeContent); ok && !content.closed {
				t.Fatal("orphaned content lifetime was not closed")
			}
		})
	}
}

func TestStopDetachesBeforeEndpointAndContent(t *testing.T) {
	t.Parallel()

	events := &eventLog{}
	content := &fakeContent{events: events}
	endpoint := &fakeEndpoint{events: events, endpoint: testEndpoint}
	connector := &fakeConnector{events: events}
	controller := newTestController(t, Dependencies{
		Filesystems: &fakeFilesystemBuilder{events: events, content: content},
		Endpoint:    endpoint,
		Connector:   connector,
	})
	if _, err := controller.Start(context.Background(), personalDrive(), StartOptions{}); err != nil {
		t.Fatal(err)
	}
	events.reset()

	stopped, err := controller.Stop(context.Background())
	if err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
	assertStatus(t, stopped, PhaseStopped, false, false)
	if got := events.snapshot(); strings.Join(got, ",") != "connector.detach,endpoint.stop,content.close" {
		t.Fatalf("stop order = %v", got)
	}
	if _, err := controller.Stop(context.Background()); err != nil {
		t.Fatalf("idempotent Stop() error = %v", err)
	}
	if connector.detachCalls != 1 || endpoint.stopCalls != 1 || content.closeCalls != 1 {
		t.Fatalf("idempotent stop repeated cleanup: detach=%d endpoint=%d content=%d", connector.detachCalls, endpoint.stopCalls, content.closeCalls)
	}
}

func TestWritableStopDrainsBeforeDetachAndPublishesDraining(t *testing.T) {
	t.Parallel()

	events := &eventLog{}
	drainEntered := make(chan struct{})
	drainRelease := make(chan struct{})
	writes := &fakeWriterSession{events: events, drainEntered: drainEntered, drainRelease: drainRelease}
	controller := newTestController(t, Dependencies{
		Filesystems: &fakeFilesystemBuilder{events: events, content: &fakeContent{events: events}},
		Writers:     &fakeWriterBuilder{events: events, session: writes},
		Endpoint:    &fakeEndpoint{events: events, endpoint: testEndpoint},
		Connector:   &fakeConnector{events: events},
	})
	if _, err := controller.Start(context.Background(), personalDrive(), StartOptions{}); err != nil {
		t.Fatal(err)
	}
	events.reset()

	result := make(chan startResult, 1)
	go func() {
		status, err := controller.Stop(context.Background())
		result <- startResult{status: status, err: err}
	}()
	<-drainEntered
	status := controller.Status()
	if status.Phase != PhaseDraining || status.WriteState != WriteStateDraining || !status.Mounted {
		t.Fatalf("draining status = %#v", status)
	}
	if writes.statusCalls == 0 {
		t.Fatal("writer status was not consulted while draining")
	}
	close(drainRelease)
	stopped := <-result
	if stopped.err != nil || stopped.status.Phase != PhaseStopped {
		t.Fatalf("Stop() = (%#v, %v)", stopped.status, stopped.err)
	}
	if got := strings.Join(events.snapshot(), ","); got != "writer.drain,connector.detach,endpoint.stop,writer.close,content.close" {
		t.Fatalf("writable stop order = %s", got)
	}
}

func TestWritableDrainFailureKeepsMountedResourcesAndAllowsSafeRetry(t *testing.T) {
	t.Parallel()

	events := &eventLog{}
	writes := &fakeWriterSession{events: events, drainErr: errors.New("Telegram commit pending " + testEndpoint)}
	endpoint := &fakeEndpoint{events: events, endpoint: testEndpoint}
	connector := &fakeConnector{events: events}
	content := &fakeContent{events: events}
	controller := newTestController(t, Dependencies{
		Filesystems: &fakeFilesystemBuilder{content: content},
		Writers:     &fakeWriterBuilder{session: writes},
		Endpoint:    endpoint,
		Connector:   connector,
	})
	if _, err := controller.Start(context.Background(), personalDrive(), StartOptions{}); err != nil {
		t.Fatal(err)
	}
	events.reset()

	status, err := controller.Stop(context.Background())
	if err == nil || !errors.Is(err, ErrStopFailed) {
		t.Fatalf("Stop() error = %v, want ErrStopFailed", err)
	}
	if status.Phase != PhaseFailed || !status.Mounted || status.WriteState != WriteStateDraining {
		t.Fatalf("failed drain status = %#v", status)
	}
	assertCapabilityFree(t, status.Error)
	if connector.detachCalls != 0 || endpoint.stopCalls != 0 || content.closed || writes.closeCalls != 0 {
		t.Fatalf("failed drain released mounted resources")
	}

	writes.drainErr = nil
	if status, err = controller.Stop(context.Background()); err != nil || status.Phase != PhaseStopped {
		t.Fatalf("retry Stop() = (%#v, %v)", status, err)
	}
}

func TestDetachFailurePreservesRecoverableMountedSession(t *testing.T) {
	t.Parallel()

	events := &eventLog{}
	content := &fakeContent{events: events}
	endpoint := &fakeEndpoint{events: events, endpoint: testEndpoint}
	connector := &fakeConnector{events: events, detachErr: errors.New("cannot detach " + testEndpoint)}
	controller := newTestController(t, Dependencies{
		Filesystems: &fakeFilesystemBuilder{content: content},
		Endpoint:    endpoint,
		Connector:   connector,
	})
	if _, err := controller.Start(context.Background(), personalDrive(), StartOptions{}); err != nil {
		t.Fatal(err)
	}
	events.reset()

	status, err := controller.Stop(context.Background())
	if err == nil {
		t.Fatal("Stop() error = nil")
	}
	if !errors.Is(err, ErrStopFailed) || errors.Unwrap(err) != nil {
		t.Fatalf("Stop() error classification = %v, unwrap = %v", err, errors.Unwrap(err))
	}
	assertCapabilityFree(t, err.Error())
	assertStatus(t, status, PhaseFailed, true, true)
	if endpoint.stopCalls != 0 || content.closed {
		t.Fatalf("failed detach released live resources: endpoint stops=%d content.closed=%v", endpoint.stopCalls, content.closed)
	}
	if got := events.snapshot(); strings.Join(got, ",") != "connector.detach" {
		t.Fatalf("failed detach order = %v", got)
	}

	connector.detachErr = nil
	if status, err = controller.Stop(context.Background()); err != nil || status.Phase != PhaseStopped {
		t.Fatalf("retry Stop() = (%#v, %v)", status, err)
	}
	if endpoint.stopCalls != 1 || !content.closed {
		t.Fatalf("retry did not release resources: endpoint stops=%d content.closed=%v", endpoint.stopCalls, content.closed)
	}
}

func TestUnexpectedEndpointFailureIsHonestAndStopStillDetaches(t *testing.T) {
	t.Parallel()

	events := &eventLog{}
	content := &fakeContent{events: events}
	endpoint := &fakeEndpoint{events: events, endpoint: testEndpoint}
	connector := &fakeConnector{events: events}
	controller := newTestController(t, Dependencies{
		Filesystems: &fakeFilesystemBuilder{events: events, content: content},
		Endpoint:    endpoint,
		Connector:   connector,
	})
	if _, err := controller.Start(context.Background(), personalDrive(), StartOptions{}); err != nil {
		t.Fatal(err)
	}

	endpoint.mu.Lock()
	endpoint.running = false
	endpoint.mu.Unlock()

	status := controller.Status()
	assertStatus(t, status, PhaseFailed, false, true)
	assertCapabilityFree(t, status.Error)
	events.reset()

	stopped, err := controller.Stop(context.Background())
	if err != nil {
		t.Fatalf("Stop() after endpoint failure error = %v", err)
	}
	assertStatus(t, stopped, PhaseStopped, false, false)
	if got := events.snapshot(); strings.Join(got, ",") != "connector.detach,endpoint.stop,content.close" {
		t.Fatalf("cleanup after endpoint failure = %v", got)
	}
}

func TestNilContextsAreTyped(t *testing.T) {
	t.Parallel()

	controller := newTestController(t, Dependencies{
		Filesystems: &fakeFilesystemBuilder{content: &fakeContent{}},
		Endpoint:    &fakeEndpoint{endpoint: testEndpoint},
		Connector:   &fakeConnector{},
	})
	if _, err := controller.Start(nil, personalDrive(), StartOptions{}); !errors.Is(err, ErrInvalidContext) {
		t.Fatalf("Start(nil) error = %v", err)
	}
	if _, err := controller.Stop(nil); !errors.Is(err, ErrInvalidContext) {
		t.Fatalf("Stop(nil) error = %v", err)
	}
	if err := controller.Open(nil); !errors.Is(err, ErrInvalidContext) {
		t.Fatalf("Open(nil) error = %v", err)
	}
}

func TestOpenRequiresMountedStateAndCloseUsesStop(t *testing.T) {
	t.Parallel()

	connector := &fakeConnector{}
	controller := newTestController(t, Dependencies{
		Filesystems: &fakeFilesystemBuilder{content: &fakeContent{}},
		Endpoint:    &fakeEndpoint{endpoint: testEndpoint},
		Connector:   connector,
	})
	if err := controller.Open(context.Background()); !errors.Is(err, ErrNotMounted) {
		t.Fatalf("Open() before Start error = %v, want ErrNotMounted", err)
	}
	if _, err := controller.Start(context.Background(), personalDrive(), StartOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := controller.Open(context.Background()); err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	if connector.openCalls != 1 {
		t.Fatalf("Open calls = %d, want 1", connector.openCalls)
	}
	connector.openErr = errors.New("open failed for " + testEndpoint)
	if err := controller.Open(context.Background()); !errors.Is(err, ErrOpenFailed) || errors.Unwrap(err) != nil {
		t.Fatalf("failed Open() error = %v, unwrap = %v", err, errors.Unwrap(err))
	} else {
		assertCapabilityFree(t, err.Error())
	}
	if err := controller.Close(context.Background()); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if controller.Status().Phase != PhaseStopped {
		t.Fatalf("status after Close = %#v", controller.Status())
	}
}

func TestWaitingStartHonorsCancellation(t *testing.T) {
	t.Parallel()

	attachEntered := make(chan struct{})
	attachRelease := make(chan struct{})
	controller := newTestController(t, Dependencies{
		Filesystems: &fakeFilesystemBuilder{content: &fakeContent{}},
		Endpoint:    &fakeEndpoint{endpoint: testEndpoint},
		Connector:   &fakeConnector{attachEntered: attachEntered, attachRelease: attachRelease},
	})
	first := make(chan startResult, 1)
	go func() {
		status, err := controller.Start(context.Background(), personalDrive(), StartOptions{})
		first <- startResult{status: status, err: err}
	}()
	<-attachEntered

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	status, err := controller.Start(ctx, personalDrive(), StartOptions{})
	if !errors.Is(err, context.Canceled) || status.Phase != PhaseAttaching {
		t.Fatalf("canceled coalesced Start() = (%#v, %v)", status, err)
	}
	close(attachRelease)
	if result := <-first; result.err != nil {
		t.Fatal(result.err)
	}
}

func newTestController(t *testing.T, dependencies Dependencies) *Controller {
	t.Helper()
	controller, err := NewWithDependencies(dependencies)
	if err != nil {
		t.Fatalf("NewWithDependencies() error = %v", err)
	}
	return controller
}

func personalDrive() Drive {
	return Drive{ID: 11, Title: "Personal", Kind: DriveKindPersonal}
}

func assertStatus(t *testing.T, status Status, phase Phase, running, mounted bool) {
	t.Helper()
	if status.Phase != phase || status.Running != running || status.Mounted != mounted {
		t.Fatalf("status = %#v, want phase=%q running=%v mounted=%v", status, phase, running, mounted)
	}
}

func assertCapabilityFree(t *testing.T, value string) {
	t.Helper()
	if strings.Contains(value, "tdrive-") || strings.Contains(value, "127.0.0.1") {
		t.Fatalf("public value leaked mount capability: %q", value)
	}
}

const testEndpoint = "http://127.0.0.1:49152/tdrive-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa/"

type startResult struct {
	status Status
	err    error
}

type eventLog struct {
	mu     sync.Mutex
	events []string
}

func (log *eventLog) add(event string) {
	if log == nil {
		return
	}
	log.mu.Lock()
	log.events = append(log.events, event)
	log.mu.Unlock()
}

func (log *eventLog) snapshot() []string {
	if log == nil {
		return nil
	}
	log.mu.Lock()
	defer log.mu.Unlock()
	return append([]string(nil), log.events...)
}

func (log *eventLog) reset() {
	if log == nil {
		return
	}
	log.mu.Lock()
	log.events = nil
	log.mu.Unlock()
}

type fakeContent struct {
	mu         sync.Mutex
	events     *eventLog
	closed     bool
	closeCalls int
}

func (content *fakeContent) Close() {
	content.mu.Lock()
	defer content.mu.Unlock()
	content.events.add("content.close")
	content.closed = true
	content.closeCalls++
}

type fakeFilesystemBuilder struct {
	mu      sync.Mutex
	events  *eventLog
	entered chan struct{}
	release <-chan struct{}
	content ContentLifetime
	fs      *mountfs.FS
	nilFS   bool
	options mountfs.Options
	err     error
}

func (builder *fakeFilesystemBuilder) Build(ctx context.Context, _ int64, options mountfs.Options) (*mountfs.FS, ContentLifetime, error) {
	builder.mu.Lock()
	builder.options = options
	builder.mu.Unlock()
	builder.events.add("build")
	if builder.entered != nil {
		close(builder.entered)
	}
	if builder.release != nil {
		select {
		case <-builder.release:
		case <-ctx.Done():
			return nil, nil, ctx.Err()
		}
	}
	if builder.err != nil || builder.nilFS {
		return builder.fs, builder.content, builder.err
	}
	if builder.fs == nil {
		filesystem, err := mountfs.New(1, emptyDirectorySource{}, emptyContentOpener{})
		if err != nil {
			return nil, builder.content, err
		}
		builder.fs = filesystem
	}
	return builder.fs, builder.content, nil
}

type emptyDirectorySource struct{}

func (emptyDirectorySource) ListDirectory(context.Context, int64, string) ([]mountfs.SourceEntry, error) {
	return []mountfs.SourceEntry{}, nil
}

type emptyContentOpener struct{}

func (emptyContentOpener) OpenContent(context.Context, int64, mountfs.SourceEntry) (mountfs.RandomAccessContent, error) {
	return nil, mountfs.ErrNotFound
}

type fakeEndpoint struct {
	mu         sync.Mutex
	events     *eventLog
	endpoint   string
	startErr   error
	stopErr    error
	healthDown bool
	running    bool
	startCalls int
	stopCalls  int
	config     EndpointConfig
}

func (endpoint *fakeEndpoint) Start(_ context.Context, config EndpointConfig) (EndpointStatus, error) {
	endpoint.mu.Lock()
	defer endpoint.mu.Unlock()
	endpoint.events.add("endpoint.start")
	endpoint.startCalls++
	endpoint.config = config
	if endpoint.startErr != nil {
		return EndpointStatus{}, endpoint.startErr
	}
	endpoint.running = true
	return EndpointStatus{Endpoint: endpoint.endpoint}, nil
}

type fakeWriterBuilder struct {
	mu         sync.Mutex
	events     *eventLog
	session    WriteSession
	drive      Drive
	fs         *mountfs.FS
	buildErr   error
	buildCalls int
}

func (builder *fakeWriterBuilder) Build(_ context.Context, drive Drive, fs *mountfs.FS) (WriteSession, error) {
	builder.mu.Lock()
	defer builder.mu.Unlock()
	builder.events.add("writer.build")
	builder.drive = drive
	builder.fs = fs
	builder.buildCalls++
	return builder.session, builder.buildErr
}

type fakeWriterSession struct {
	mu           sync.Mutex
	events       *eventLog
	drainEntered chan struct{}
	drainRelease <-chan struct{}
	drainErr     error
	closeErr     error
	drainCalls   int
	closeCalls   int
	statusCalls  int
}

func (writer *fakeWriterSession) Drain(ctx context.Context) error {
	writer.mu.Lock()
	writer.events.add("writer.drain")
	writer.drainCalls++
	if writer.drainEntered != nil {
		close(writer.drainEntered)
		writer.drainEntered = nil
	}
	release := writer.drainRelease
	err := writer.drainErr
	writer.mu.Unlock()
	if release != nil {
		select {
		case <-release:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return err
}

func (writer *fakeWriterSession) Close(context.Context) error {
	writer.mu.Lock()
	defer writer.mu.Unlock()
	writer.events.add("writer.close")
	writer.closeCalls++
	return writer.closeErr
}

func (writer *fakeWriterSession) WriteStatus() WriteStatus {
	writer.mu.Lock()
	defer writer.mu.Unlock()
	writer.statusCalls++
	return WriteStatus{Accepting: writer.drainCalls == 0, Active: 1}
}

func (endpoint *fakeEndpoint) Health() EndpointHealth {
	endpoint.mu.Lock()
	defer endpoint.mu.Unlock()
	return EndpointHealth{Running: endpoint.running && !endpoint.healthDown}
}

func (endpoint *fakeEndpoint) Stop(_ context.Context) error {
	endpoint.mu.Lock()
	defer endpoint.mu.Unlock()
	endpoint.events.add("endpoint.stop")
	endpoint.stopCalls++
	endpoint.running = false
	return endpoint.stopErr
}

type fakeConnector struct {
	mu            sync.Mutex
	events        *eventLog
	config        mountos.Config
	attachEntered chan struct{}
	attachRelease <-chan struct{}
	attachErr     error
	detachErr     error
	openErr       error
	attachCalls   int
	detachCalls   int
	openCalls     int
}

func (connector *fakeConnector) Attach(ctx context.Context, config mountos.Config) (mountos.Attachment, error) {
	connector.mu.Lock()
	connector.config = config
	connector.attachCalls++
	connector.mu.Unlock()
	connector.events.add("connector.attach")
	if connector.attachEntered != nil {
		close(connector.attachEntered)
	}
	if connector.attachRelease != nil {
		select {
		case <-connector.attachRelease:
		case <-ctx.Done():
			return mountos.Attachment{}, ctx.Err()
		}
	}
	connector.mu.Lock()
	err := connector.attachErr
	connector.mu.Unlock()
	return mountos.NewAttachment(mountos.KindDarwin, "/Volumes/Tdrive personal"), err
}

func (connector *fakeConnector) Detach(_ context.Context, _ mountos.Attachment) error {
	connector.mu.Lock()
	defer connector.mu.Unlock()
	connector.events.add("connector.detach")
	connector.detachCalls++
	return connector.detachErr
}

func (connector *fakeConnector) Open(_ context.Context, _ mountos.Attachment) error {
	connector.mu.Lock()
	defer connector.mu.Unlock()
	connector.events.add("connector.open")
	connector.openCalls++
	return connector.openErr
}

var _ FilesystemBuilder = (*fakeFilesystemBuilder)(nil)
var _ WriterBuilder = (*fakeWriterBuilder)(nil)
var _ WriteSession = (*fakeWriterSession)(nil)
var _ Endpoint = (*fakeEndpoint)(nil)
var _ mountos.Connector = (*fakeConnector)(nil)
