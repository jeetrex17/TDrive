package mountcontroller

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"TDrive/backend/core"
	"TDrive/backend/mountcontent"
	"TDrive/backend/mountfs"
	"TDrive/backend/mountos"
	"TDrive/backend/tgclient"
)

const (
	defaultMountSnapshotTTL = 15 * time.Second
	cleanupTimeout          = 5 * time.Second
	writeDrainTimeout       = 30 * time.Second
)

type operationKind uint8

const (
	operationStart operationKind = iota + 1
	operationStop
)

type operation struct {
	kind         operationKind
	driveID      int64
	selection    string
	windowsDrive string
	mode         Mode
	done         chan struct{}
	status       Status
	err          error
}

type session struct {
	drive        Drive
	drives       []Drive
	selection    string
	label        string
	windowsDrive string
	mode         Mode
	writeState   WriteState
	key          MountKeyLease
	content      ContentLifetime
	writer       WriteSession
	attachment   mountos.Attachment
}

// Controller serializes the lifecycle of one mounted drive. Expensive work is
// performed without holding mu so Status remains responsive during OS calls.
type Controller struct {
	mu          sync.Mutex
	filesystems FilesystemBuilder
	writers     WriterBuilder
	keys        MountKeyLeaser
	endpoint    Endpoint
	connector   mountos.Connector
	fsOptions   mountfs.Options
	status      Status
	session     *session
	operation   *operation
	lastErr     error
}

// New creates a production controller around the Engine already owned by the
// daemon or GUI process.
func New(engine *core.Engine) (*Controller, error) {
	return NewWithConnector(engine, mountos.New())
}

// NewWithConnector is a production constructor with an injectable OS boundary.
// It is useful for application tests that still exercise the real filesystem
// and WebDAV stack.
func NewWithConnector(engine *core.Engine, connector mountos.Connector) (*Controller, error) {
	if engine == nil {
		return nil, fmt.Errorf("%w: engine is required", ErrInvalidConfiguration)
	}
	reads := engine.ReadService()
	if reads == nil || reads.DB == nil {
		return nil, fmt.Errorf("%w: database is not ready", ErrInvalidConfiguration)
	}
	ranges, ok := engine.Telegram().(tgclient.RangeClient)
	if !ok || ranges == nil {
		return nil, fmt.Errorf("%w: Telegram range reads are unavailable", ErrInvalidConfiguration)
	}

	return NewWithDependencies(Dependencies{
		Filesystems: &engineFilesystemBuilder{
			reads:  reads,
			peers:  engine,
			ranges: ranges,
		},
		Writers:   newEngineWriterBuilder(engine),
		Keys:      engineMountKeyLeaser{engine: engine},
		Endpoint:  newWebDAVEndpoint(),
		Connector: connector,
	})
}

// NewWithDependencies constructs a controller from deterministic lifecycle
// boundaries. Dependencies are never replaced after construction.
func NewWithDependencies(dependencies Dependencies) (*Controller, error) {
	if dependencies.Filesystems == nil || dependencies.Endpoint == nil || dependencies.Connector == nil {
		return nil, ErrInvalidConfiguration
	}
	options := dependencies.SnapshotOptions
	if options.SnapshotTTL < 0 || options.MaxCachedDirectories < 0 || options.MaxCachedEntries < 0 || options.MaxConcurrentSnapshotLoads < 0 {
		return nil, ErrInvalidConfiguration
	}
	if options.SnapshotTTL == 0 {
		options.SnapshotTTL = defaultMountSnapshotTTL
	}

	return &Controller{
		filesystems: dependencies.Filesystems,
		writers:     dependencies.Writers,
		keys:        dependencies.Keys,
		endpoint:    dependencies.Endpoint,
		connector:   dependencies.Connector,
		fsOptions:   options,
		status:      Status{Phase: PhaseStopped},
	}, nil
}

// Start mounts one drive without changing the Engine's active drive. It keeps
// the existing single-drive layout used by the CLI.
func (controller *Controller) Start(ctx context.Context, requested Drive, options StartOptions) (Status, error) {
	return controller.startDrives(ctx, []Drive{requested}, options)
}

// StartDrives mounts an immutable selection below one virtual TDrive root.
// A one-drive selection preserves the existing direct-root layout.
func (controller *Controller) StartDrives(ctx context.Context, requested []Drive, options StartOptions) (Status, error) {
	return controller.startDrives(ctx, requested, options)
}

func (controller *Controller) startDrives(ctx context.Context, requested []Drive, options StartOptions) (Status, error) {
	if controller == nil {
		return Status{Phase: PhaseFailed, Error: "TDrive mount is unavailable"}, ErrInvalidConfiguration
	}
	if ctx == nil {
		return controller.Status(), ErrInvalidContext
	}
	if err := ctx.Err(); err != nil {
		return controller.Status(), err
	}
	drives, err := normalizeDriveSelection(requested)
	if err != nil {
		return controller.Status(), err
	}
	drive := drives[0]
	label, err := selectionLabel(drives)
	if err != nil {
		return controller.Status(), err
	}
	windowsDrive, err := normalizeWindowsDrive(options.WindowsDrive)
	if err != nil {
		return controller.Status(), err
	}
	mode, err := resolveSelectionMode(options.Mode, drives, controller.writers)
	if err != nil {
		return controller.Status(), err
	}
	selection := driveSelectionSignature(drives)

	for {
		controller.refreshHealth()
		controller.mu.Lock()
		if current := controller.operation; current != nil {
			if current.kind == operationStart {
				if err := selectionConflictError(
					current.selection,
					selection,
					current.driveID,
					current.windowsDrive,
					current.mode,
					drive.ID,
					windowsDrive,
					mode,
				); err != nil {
					status := controller.status
					controller.mu.Unlock()
					return status, err
				}
				status, err := controller.waitLocked(ctx, current)
				return status, err
			}
			status, err := controller.waitLocked(ctx, current)
			if err != nil {
				return status, err
			}
			continue
		}

		if controller.session != nil {
			if err := selectionConflictError(
				controller.session.selection,
				selection,
				controller.session.drive.ID,
				controller.session.windowsDrive,
				controller.session.mode,
				drive.ID,
				windowsDrive,
				mode,
			); err != nil {
				status := controller.status
				controller.mu.Unlock()
				return status, err
			}
			status, existingErr := controller.status, controller.lastErr
			controller.mu.Unlock()
			return status, existingErr
		}

		current := &operation{
			kind:         operationStart,
			driveID:      drive.ID,
			selection:    selection,
			windowsDrive: windowsDrive,
			mode:         mode,
			done:         make(chan struct{}),
		}
		active := &session{
			drive:        drive,
			drives:       append([]Drive(nil), drives...),
			selection:    selection,
			label:        label,
			windowsDrive: windowsDrive,
			mode:         mode,
			writeState:   initialWriteState(mode),
		}
		controller.operation = current
		controller.session = active
		controller.lastErr = nil
		controller.status = statusFor(active, PhasePreparing, false, false, "")
		controller.mu.Unlock()

		return controller.runStart(ctx, current, active)
	}
}

func (controller *Controller) runStart(ctx context.Context, current *operation, active *session) (Status, error) {
	filesystem, err := controller.prepareSession(ctx, active)
	if err != nil {
		_ = closeWriterForRollback(ctx, active.writer)
		if active.content != nil {
			active.content.Close()
		}
		closeSessionKey(active)
		return controller.failStart(current, active, mountFailureMessage("prepare", active.mode), err)
	}

	endpointStatus, err := controller.endpoint.Start(ctx, EndpointConfig{
		FS:           filesystem,
		DriveID:      active.drive.ID,
		DriveTitle:   active.label,
		WindowsDrive: active.windowsDrive,
		Mode:         active.mode,
		Writer:       active.writer,
	})
	if err != nil {
		_ = closeWriterForRollback(ctx, active.writer)
		active.content.Close()
		closeSessionKey(active)
		return controller.failStart(current, active, mountFailureMessage("start", active.mode), err)
	}
	controller.mu.Lock()
	controller.status = statusFor(active, PhaseAttaching, true, false, "")
	controller.mu.Unlock()

	attachment, err := controller.connector.Attach(ctx, mountos.Config{
		Endpoint:     endpointStatus.Endpoint,
		Label:        active.label,
		WindowsDrive: active.windowsDrive,
		Mode:         mountOSMode(active.mode),
	})
	if err != nil {
		cleanupErr := controller.stopEndpointForRollback(ctx)
		writerErr := closeWriterForRollback(ctx, active.writer)
		active.content.Close()
		closeSessionKey(active)
		return controller.failStart(
			current,
			active,
			mountFailureMessage("attach", active.mode),
			errors.Join(err, cleanupErr, writerErr),
		)
	}
	controller.setAttachment(active, attachment)
	if !controller.endpoint.Health().Running {
		return controller.recoverFailedEndpointAfterAttach(ctx, current, active)
	}

	controller.mu.Lock()
	active.writeState = readyWriteState(active.mode)
	controller.status = statusFor(active, PhaseMounted, true, true, "")
	controller.lastErr = nil
	status := controller.status
	controller.completeLocked(current, status, nil)
	controller.mu.Unlock()
	return status, nil
}

func (controller *Controller) failStart(current *operation, active *session, message string, cause error) (Status, error) {
	publicMessage := startFailureMessage(message, cause, active)
	publicErr := &operationError{message: publicMessage, kind: classifyOperationError(cause, ErrStartFailed)}
	controller.mu.Lock()
	if controller.session == active {
		controller.session = nil
	}
	controller.lastErr = publicErr
	controller.status = statusFor(active, PhaseFailed, false, false, publicMessage)
	status := controller.status
	controller.completeLocked(current, status, publicErr)
	controller.mu.Unlock()
	return status, publicErr
}

// Stop detaches the OS mount before releasing the endpoint and content cache.
// If detachment fails, those resources are intentionally kept alive so an OS
// client never points at a dead local server.
func (controller *Controller) Stop(ctx context.Context) (Status, error) {
	if controller == nil {
		return Status{Phase: PhaseStopped}, nil
	}
	if ctx == nil {
		return controller.Status(), ErrInvalidContext
	}
	if err := ctx.Err(); err != nil {
		return controller.Status(), err
	}

	for {
		controller.refreshHealth()
		controller.mu.Lock()
		if current := controller.operation; current != nil {
			status, err := controller.waitLocked(ctx, current)
			if err != nil {
				return status, err
			}
			if current.kind == operationStop {
				return status, nil
			}
			continue
		}
		active := controller.session
		if active == nil {
			controller.session = nil
			controller.lastErr = nil
			controller.status = Status{Phase: PhaseStopped}
			status := controller.status
			controller.mu.Unlock()
			return status, nil
		}

		current := &operation{kind: operationStop, done: make(chan struct{})}
		controller.operation = current
		controller.lastErr = nil
		phase := PhaseDetaching
		if active.writer != nil {
			phase = PhaseDraining
			active.writeState = WriteStateDraining
		}
		controller.status = statusFor(
			active,
			phase,
			controller.status.Running,
			controller.status.Mounted,
			"",
		)
		controller.mu.Unlock()
		return controller.runStop(ctx, current, active)
	}
}

func (controller *Controller) runStop(ctx context.Context, current *operation, active *session) (Status, error) {
	if active.writer != nil {
		drainCtx, cancel := context.WithTimeout(ctx, writeDrainTimeout)
		drainErr := active.writer.Drain(drainCtx)
		cancel()
		if drainErr != nil {
			const message = "TDrive could not finish pending changes; the drive remains mounted"
			return controller.failStop(current, active, message, drainErr, true)
		}
		controller.mu.Lock()
		active.writeState = WriteStateDrained
		controller.status = statusFor(
			active,
			PhaseDetaching,
			controller.status.Running,
			controller.status.Mounted,
			"",
		)
		controller.mu.Unlock()
	}

	if err := controller.connector.Detach(ctx, active.attachment); err != nil {
		controller.mu.Lock()
		running := controller.status.Running
		controller.mu.Unlock()
		message := "TDrive could not disconnect the mount; it remains available"
		if !running {
			message = "TDrive could not disconnect the stale mount; retry disconnecting"
		}
		return controller.failStop(current, active, message, err, true)
	}

	controller.mu.Lock()
	controller.status = statusFor(
		active,
		PhaseDetaching,
		controller.status.Running,
		false,
		"",
	)
	controller.mu.Unlock()

	stopErr := controller.endpoint.Stop(ctx)
	writerErr := closeWriteSession(ctx, active.writer)
	if active.content != nil {
		active.content.Close()
	}
	closeSessionKey(active)

	if stopErr != nil || writerErr != nil {
		const message = "TDrive disconnected, but local mount cleanup did not finish cleanly"
		return controller.failStop(current, active, message, errors.Join(stopErr, writerErr), false)
	}
	controller.mu.Lock()
	controller.session = nil
	controller.lastErr = nil
	controller.status = Status{Phase: PhaseStopped}
	status := controller.status
	controller.completeLocked(current, status, nil)
	controller.mu.Unlock()
	return status, nil
}

func (controller *Controller) failStop(
	current *operation,
	active *session,
	message string,
	cause error,
	keepMounted bool,
) (Status, error) {
	publicErr := &operationError{message: message, kind: classifyOperationError(cause, ErrStopFailed)}
	controller.mu.Lock()
	if keepMounted {
		controller.status = statusFor(active, PhaseFailed, controller.status.Running, true, message)
	} else {
		if controller.session == active {
			controller.session = nil
		}
		controller.status = Status{
			Phase:      PhaseFailed,
			Mode:       active.mode,
			WriteState: active.writeState,
			Error:      message,
		}
	}
	controller.lastErr = publicErr
	status := controller.status
	controller.completeLocked(current, status, publicErr)
	controller.mu.Unlock()
	return status, publicErr
}

// Open asks the host file manager to reveal the verified attachment. It never
// opens the capability URL directly.
func (controller *Controller) Open(ctx context.Context) error {
	if controller == nil {
		return ErrNotMounted
	}
	if ctx == nil {
		return ErrInvalidContext
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	controller.mu.Lock()
	if controller.session == nil || !controller.status.Mounted {
		controller.mu.Unlock()
		return ErrNotMounted
	}
	attachment := controller.session.attachment
	controller.mu.Unlock()

	if err := controller.connector.Open(ctx, attachment); err != nil {
		return &operationError{
			message: "TDrive could not open the mounted drive",
			kind:    classifyOperationError(err, ErrOpenFailed),
		}
	}
	return nil
}

// Status returns an immutable, capability-free snapshot.
func (controller *Controller) Status() Status {
	if controller == nil {
		return Status{Phase: PhaseStopped}
	}
	controller.refreshHealth()
	controller.mu.Lock()
	status := controller.status
	active := controller.session
	status = withWriteStatus(status, active)
	controller.mu.Unlock()
	return status
}

// Close applies the same safe detach-first lifecycle as Stop.
func (controller *Controller) Close(ctx context.Context) error {
	_, err := controller.Stop(ctx)
	return err
}

func (controller *Controller) waitLocked(ctx context.Context, current *operation) (Status, error) {
	status := controller.status
	controller.mu.Unlock()
	select {
	case <-current.done:
		return current.status, current.err
	case <-ctx.Done():
		return status, ctx.Err()
	}
}

func (controller *Controller) completeLocked(current *operation, status Status, err error) {
	current.status = status
	current.err = err
	if controller.operation == current {
		controller.operation = nil
	}
	close(current.done)
}

func (controller *Controller) refreshHealth() {
	controller.mu.Lock()
	if controller.operation != nil || controller.session == nil || !controller.status.Running {
		controller.mu.Unlock()
		return
	}
	active := controller.session
	controller.mu.Unlock()

	if controller.endpoint.Health().Running {
		return
	}

	controller.mu.Lock()
	defer controller.mu.Unlock()
	if controller.operation != nil || controller.session != active || !controller.status.Running {
		return
	}
	const message = "TDrive local mount server stopped unexpectedly; eject the mount before trying again"
	publicErr := &operationError{message: message, kind: ErrEndpointUnavailable}
	controller.lastErr = publicErr
	controller.status = statusFor(
		controller.session,
		PhaseFailed,
		false,
		controller.status.Mounted,
		message,
	)
}

func (controller *Controller) recoverFailedEndpointAfterAttach(
	ctx context.Context,
	current *operation,
	active *session,
) (Status, error) {
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), cleanupTimeout)
	defer cancel()
	if detachErr := controller.connector.Detach(cleanupCtx, active.attachment); detachErr != nil {
		const message = "TDrive attached, but its local mount server stopped; retry ejecting the stale mount"
		publicErr := &operationError{message: message, kind: classifyOperationError(detachErr, ErrStopFailed)}
		controller.mu.Lock()
		controller.lastErr = publicErr
		controller.status = statusFor(active, PhaseFailed, false, true, message)
		status := controller.status
		controller.completeLocked(current, status, publicErr)
		controller.mu.Unlock()
		return status, publicErr
	}
	stopErr := controller.endpoint.Stop(cleanupCtx)
	writerErr := closeWriteSession(cleanupCtx, active.writer)
	if active.content != nil {
		active.content.Close()
	}
	closeSessionKey(active)
	return controller.failStart(
		current,
		active,
		"TDrive local read server stopped during attachment",
		errors.Join(stopErr, writerErr),
	)
}

func (controller *Controller) stopEndpointForRollback(ctx context.Context) error {
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), cleanupTimeout)
	defer cancel()
	return controller.endpoint.Stop(cleanupCtx)
}

func statusFor(active *session, phase Phase, running, mounted bool, message string) Status {
	if active == nil {
		return Status{Phase: phase, Running: running, Mounted: mounted, Error: message}
	}
	status := Status{
		Phase:        phase,
		Running:      running,
		Mounted:      mounted,
		Mode:         active.mode,
		WriteState:   active.writeState,
		WindowsDrive: active.windowsDrive,
		Error:        message,
	}
	if len(active.drives) <= 1 {
		status.DriveID = active.drive.ID
		status.DriveTitle = active.drive.Title
		status.DriveKind = active.drive.Kind
		status.DriveEncrypted = active.drive.Encrypted
		status.DriveEncryptionUnlocked = active.drive.EncryptionUnlocked
	}
	status.Label = active.label
	status = withWriteStatus(status, active)
	if mounted {
		status.Location = active.attachment.Location()
		status.AttachmentKind = active.attachment.Kind()
	}
	return status
}

// setWriter and setAttachment publish these session fields under mu. Building
// the writer or attaching the OS mount happens outside mu so Status stays
// responsive during slow OS/network calls (see the Controller doc comment);
// these short, separately locked setters are what let a concurrent Status or
// Open call observe them safely once they exist instead of racing runStart.
func (controller *Controller) setWriter(active *session, writer WriteSession) {
	controller.mu.Lock()
	active.writer = writer
	controller.mu.Unlock()
}

func (controller *Controller) setAttachment(active *session, attachment mountos.Attachment) {
	controller.mu.Lock()
	active.attachment = attachment
	controller.mu.Unlock()
}

func withWriteStatus(status Status, active *session) Status {
	if active == nil || active.writer == nil {
		return status
	}
	writerStatus := active.writer.WriteStatus()
	status.AcceptingWrites = writerStatus.Accepting && active.writeState == WriteStateReady
	status.ActiveWrites = max(writerStatus.Active, 0)
	return status
}

func conflictError(
	activeID int64,
	activeDrive string,
	activeMode Mode,
	requestedID int64,
	requestedDrive string,
	requestedMode Mode,
) error {
	if activeID == requestedID && activeDrive == requestedDrive && activeMode == requestedMode {
		return nil
	}
	return &ConflictError{
		ActiveDriveID:         activeID,
		RequestedDriveID:      requestedID,
		ActiveWindowsDrive:    activeDrive,
		RequestedWindowsDrive: requestedDrive,
		ActiveMode:            activeMode,
		RequestedMode:         requestedMode,
	}
}

func selectionConflictError(
	activeSelection string,
	requestedSelection string,
	activeID int64,
	activeDrive string,
	activeMode Mode,
	requestedID int64,
	requestedDrive string,
	requestedMode Mode,
) error {
	if activeSelection != requestedSelection {
		return &ConflictError{
			ActiveDriveID:         activeID,
			RequestedDriveID:      requestedID,
			SelectionChanged:      true,
			ActiveWindowsDrive:    activeDrive,
			RequestedWindowsDrive: requestedDrive,
			ActiveMode:            activeMode,
			RequestedMode:         requestedMode,
		}
	}
	return conflictError(activeID, activeDrive, activeMode, requestedID, requestedDrive, requestedMode)
}

func resolveSelectionMode(requested Mode, drives []Drive, writers WriterBuilder) (Mode, error) {
	if requested != ModeAuto && requested != ModeReadOnly && requested != ModeReadWrite {
		return "", ErrInvalidMode
	}
	personal, hasPersonal := personalDriveIn(drives)
	if hasPersonal && personal.Encrypted && !personal.EncryptionUnlocked {
		return "", ErrEncryptionPasswordRequired
	}
	if requested == ModeReadOnly {
		return ModeReadOnly, nil
	}
	eligible := writers != nil && hasPersonal
	if requested == ModeReadWrite && !eligible {
		return "", ErrWritableUnavailable
	}
	if eligible {
		return ModeReadWrite, nil
	}
	return ModeReadOnly, nil
}

func normalizeDriveSelection(requested []Drive) ([]Drive, error) {
	if len(requested) == 0 || len(requested) > 256 {
		return nil, fmt.Errorf("%w: select between 1 and 256 drives", ErrInvalidDrive)
	}
	drives := make([]Drive, len(requested))
	seen := make(map[int64]struct{}, len(requested))
	personalCount := 0
	for index, candidate := range requested {
		drive, err := normalizeDrive(candidate)
		if err != nil {
			return nil, err
		}
		if _, exists := seen[drive.ID]; exists {
			return nil, fmt.Errorf("%w: duplicate drive %d", ErrInvalidDrive, drive.ID)
		}
		seen[drive.ID] = struct{}{}
		if drive.Kind == DriveKindPersonal {
			personalCount++
			if personalCount > 1 {
				return nil, fmt.Errorf("%w: multiple personal drives", ErrInvalidDrive)
			}
		}
		drives[index] = drive
	}
	sort.Slice(drives, func(left, right int) bool {
		if drives[left].Kind != drives[right].Kind {
			return drives[left].Kind == DriveKindPersonal
		}
		leftTitle := mountfs.NameKey(drives[left].Title)
		rightTitle := mountfs.NameKey(drives[right].Title)
		if leftTitle != rightTitle {
			return leftTitle < rightTitle
		}
		return drives[left].ID < drives[right].ID
	})
	return drives, nil
}

func selectionLabel(drives []Drive) (string, error) {
	if len(drives) == 1 {
		return displayLabel(drives[0])
	}
	return "Tdrive", nil
}

func driveSelectionSignature(drives []Drive) string {
	var builder strings.Builder
	for index, drive := range drives {
		if index > 0 {
			builder.WriteByte(',')
		}
		builder.WriteString(strconv.FormatInt(drive.ID, 10))
	}
	return builder.String()
}

func initialWriteState(mode Mode) WriteState {
	if mode == ModeReadWrite {
		return WriteStateStarting
	}
	return WriteStateDisabled
}

func readyWriteState(mode Mode) WriteState {
	if mode == ModeReadWrite {
		return WriteStateReady
	}
	return WriteStateDisabled
}

func mountOSMode(mode Mode) mountos.Mode {
	if mode == ModeReadWrite {
		return mountos.ModeReadWrite
	}
	return mountos.ModeReadOnly
}

func mountFailureMessage(action string, mode Mode) string {
	if mode == ModeReadWrite {
		return fmt.Sprintf("TDrive could not %s the writable mount", action)
	}
	return fmt.Sprintf("TDrive could not %s the read-only mount", action)
}

func closeWriterForRollback(ctx context.Context, writer WriteSession) error {
	if writer == nil {
		return nil
	}
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), cleanupTimeout)
	defer cancel()
	return writer.Close(cleanupCtx)
}

func closeWriteSession(ctx context.Context, writer WriteSession) error {
	if writer == nil {
		return nil
	}
	return writer.Close(ctx)
}

func normalizeWindowsDrive(value string) (string, error) {
	value = strings.ToUpper(strings.TrimSpace(value))
	if value == "" {
		return defaultWindowsDrive, nil
	}
	if len(value) == 1 && value[0] >= 'A' && value[0] <= 'Z' {
		return value + ":", nil
	}
	if len(value) == 2 && value[0] >= 'A' && value[0] <= 'Z' && value[1] == ':' {
		return value, nil
	}
	return "", fmt.Errorf("%w: use a letter such as T:", ErrInvalidWindowsDrive)
}

func classifyOperationError(cause, fallback error) error {
	switch {
	case errors.Is(cause, ErrEncryptionPasswordRequired):
		return ErrEncryptionPasswordRequired
	case errors.Is(cause, ErrInvalidConfiguration):
		return ErrInvalidConfiguration
	case errors.Is(cause, mountos.ErrDriveOccupied):
		return mountos.ErrDriveOccupied
	case errors.Is(cause, mountos.ErrWindowsWebDAVUnavailable):
		return mountos.ErrWindowsWebDAVUnavailable
	case errors.Is(cause, mountos.ErrLinuxDesktopUnavailable):
		return mountos.ErrLinuxDesktopUnavailable
	case errors.Is(cause, mountos.ErrLinuxWebDAVUnavailable):
		return mountos.ErrLinuxWebDAVUnavailable
	case errors.Is(cause, context.Canceled):
		return context.Canceled
	case errors.Is(cause, context.DeadlineExceeded):
		return context.DeadlineExceeded
	default:
		return fallback
	}
}

func startFailureMessage(fallback string, cause error, active *session) string {
	switch {
	case errors.Is(cause, ErrEncryptionPasswordRequired):
		return "TDrive encryption password required. Unlock encryption, then mount again."
	case errors.Is(cause, mountos.ErrDriveOccupied):
		drive := defaultWindowsDrive
		if active != nil && active.windowsDrive != "" {
			drive = active.windowsDrive
		}
		return fmt.Sprintf("Windows drive %s is already in use. Free it and try again.", drive)
	case errors.Is(cause, mountos.ErrWindowsWebDAVUnavailable):
		return "Windows WebDAV is unavailable. Start or enable the WebClient service, then try again."
	case errors.Is(cause, mountos.ErrLinuxDesktopUnavailable):
		return "Linux desktop mounting is unavailable. Run TDrive inside a graphical desktop session with GIO and GVfs available, then try again."
	case errors.Is(cause, mountos.ErrLinuxWebDAVUnavailable):
		return "Linux WebDAV mounting is unavailable. Enable the GIO/GVfs WebDAV backend for your desktop session, then try again."
	default:
		return fallback
	}
}

var _ mountcontent.PeerResolver = (*core.Engine)(nil)
