//go:build windows

package mountos

import (
	"context"
	"errors"
	"log/slog"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

var wnetGetConnection = windows.NewLazySystemDLL("mpr.dll").NewProc("WNetGetConnectionW")

const windowsMappingVerificationTimeout = 3 * time.Second

type windowsDependencies struct {
	runner            commandRunner
	systemPaths       func() (netExecutable string, explorerExecutable string, err error)
	logicalDrives     func() (uint32, error)
	remoteFor         func(string) (remote string, mapped bool, err error)
	isRemoteDrive     func(string) (bool, error)
	prepareWebClient  func(context.Context) error
	verificationDelay func(context.Context) error
}

type windowsConnector struct {
	mu                sync.Mutex
	owner             *ownerMarker
	runner            commandRunner
	systemPaths       func() (string, string, error)
	logicalDrives     func() (uint32, error)
	remoteFor         func(string) (string, bool, error)
	isRemoteDrive     func(string) (bool, error)
	prepareWebClient  func(context.Context) error
	verificationDelay func(context.Context) error
	serial            uint64
	active            map[string]windowsActive
}

type windowsActive struct {
	id     uint64
	remote string
}

func newPlatformConnector() Connector {
	return newWindowsConnector(windowsDependencies{})
}

func newWindowsConnector(dependencies windowsDependencies) *windowsConnector {
	runner := dependencies.runner
	if runner == nil {
		runner = execCommandRunner{}
	}
	systemPaths := dependencies.systemPaths
	if systemPaths == nil {
		systemPaths = windowsSystemPaths
	}
	logicalDrives := dependencies.logicalDrives
	if logicalDrives == nil {
		logicalDrives = windows.GetLogicalDrives
	}
	remoteFor := dependencies.remoteFor
	if remoteFor == nil {
		remoteFor = windowsRemoteFor
	}
	isRemoteDrive := dependencies.isRemoteDrive
	if isRemoteDrive == nil {
		isRemoteDrive = windowsIsRemoteDrive
	}
	prepareWebClient := dependencies.prepareWebClient
	if prepareWebClient == nil {
		prepareWebClient = ensureWindowsWebClient
	}
	verificationDelay := dependencies.verificationDelay
	if verificationDelay == nil {
		verificationDelay = func(ctx context.Context) error {
			return waitForPoll(ctx, 100*time.Millisecond)
		}
	}
	return &windowsConnector{
		owner:             &ownerMarker{},
		runner:            runner,
		systemPaths:       systemPaths,
		logicalDrives:     logicalDrives,
		remoteFor:         remoteFor,
		isRemoteDrive:     isRemoteDrive,
		prepareWebClient:  prepareWebClient,
		verificationDelay: verificationDelay,
		active:            make(map[string]windowsActive),
	}
}

func (c *windowsConnector) Attach(parent context.Context, config Config) (Attachment, error) {
	ctx, cancel, err := boundedContext(parent, attachTimeout)
	if err != nil {
		return Attachment{}, err
	}
	defer cancel()
	validated, err := validateConfig(config)
	if err != nil {
		return Attachment{}, err
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	// validated.endpoint and the "remote" UNC strings returned by
	// c.remoteFor/windowsRemoteFor embed the loopback capability token and
	// must never be logged. Only validated.drive (e.g. "T:") is safe.
	drives, err := c.logicalDrives()
	if err != nil {
		return Attachment{}, ErrAttachFailed
	}
	if drives&(uint32(1)<<uint(validated.drive[0]-'A')) != 0 {
		slog.Warn("mountos: windows attach rejected, drive letter occupied", "drive", validated.drive)
		return Attachment{}, ErrDriveOccupied
	}
	if err := c.prepareWebClient(ctx); err != nil {
		if ctx.Err() != nil {
			return Attachment{}, ctx.Err()
		}
		slog.Warn("mountos: windows WebClient service unavailable", "drive", validated.drive, "error", err)
		return Attachment{}, ErrWindowsWebDAVUnavailable
	}
	netExecutable, _, err := c.systemPaths()
	if err != nil {
		return Attachment{}, ErrAttachFailed
	}
	slog.Debug("mountos: windows attaching", "drive", validated.drive, "mode", validated.mode)
	if err := c.runner.Run(ctx, windowsAttachPlan(netExecutable, validated.drive, validated.endpoint)); err != nil {
		slog.Warn("mountos: windows net use failed", "drive", validated.drive, "error", err)
		c.rollbackUnknown(parent, netExecutable, validated.drive, validated.endpoint)
		if ctx.Err() != nil {
			return Attachment{}, ctx.Err()
		}
		return Attachment{}, ErrWindowsWebDAVUnavailable
	}
	verificationCtx, verificationCancel := context.WithTimeout(ctx, windowsMappingVerificationTimeout)
	remote, err := c.waitForVerifiedMapping(verificationCtx, validated.drive, validated.endpoint)
	verificationCancel()
	if err != nil {
		if ctx.Err() != nil {
			return Attachment{}, ctx.Err()
		}
		if errors.Is(err, ErrAttachmentChanged) {
			// A concurrent actor or provider anomaly rebound the drive. It is not
			// ours, so never attempt to remove it.
			slog.Warn("mountos: windows drive was rebound by another actor during attach", "drive", validated.drive)
			return Attachment{}, ErrAttachmentChanged
		}
		slog.Warn("mountos: windows attach verification failed", "drive", validated.drive, "error", err)
		if remote != "" && windowsRemoteMatchesEndpoint(remote, validated.endpoint) {
			c.rollback(parent, netExecutable, validated.drive, remote)
		} else {
			c.rollbackUnknown(parent, netExecutable, validated.drive, validated.endpoint)
		}
		return Attachment{}, ErrVerificationFailed
	}

	id := c.nextID()
	c.active[validated.drive] = windowsActive{id: id, remote: remote}
	slog.Info("mountos: windows attached", "drive", validated.drive, "mode", validated.mode)
	return Attachment{
		owner:    c.owner,
		id:       id,
		kind:     KindWindows,
		location: validated.drive + `\`,
	}, nil
}

func (c *windowsConnector) waitForVerifiedMapping(ctx context.Context, drive, endpoint string) (string, error) {
	lastOwnedRemote := ""
	for {
		remote, mapped, err := c.remoteFor(drive)
		if err == nil && mapped && remote != "" {
			if !windowsRemoteMatchesEndpoint(remote, endpoint) {
				return "", ErrAttachmentChanged
			}
			lastOwnedRemote = remote
			remoteDrive, driveErr := c.isRemoteDrive(drive + `\`)
			if driveErr == nil && remoteDrive {
				return remote, nil
			}
		}
		if err := c.verificationDelay(ctx); err != nil {
			return lastOwnedRemote, ErrVerificationFailed
		}
	}
}

func (c *windowsConnector) Detach(parent context.Context, attachment Attachment) error {
	ctx, cancel, err := boundedContext(parent, detachTimeout)
	if err != nil {
		return err
	}
	defer cancel()

	c.mu.Lock()
	defer c.mu.Unlock()
	drive, active, ok := c.ownedDrive(attachment)
	if !ok {
		return ErrInvalidAttachment
	}
	current, mapped, err := c.remoteFor(drive)
	if err != nil {
		return ErrVerificationFailed
	}
	if !mapped {
		delete(c.active, drive)
		return nil
	}
	if current != active.remote {
		return ErrAttachmentChanged
	}
	netExecutable, _, err := c.systemPaths()
	if err != nil {
		return ErrDetachFailed
	}
	if err := c.runner.Run(ctx, windowsDetachPlan(netExecutable, drive)); err != nil {
		slog.Warn("mountos: windows net use delete failed", "drive", drive, "error", err)
		return ErrDetachFailed
	}
	_, mapped, err = c.remoteFor(drive)
	if err != nil || mapped {
		return ErrVerificationFailed
	}
	delete(c.active, drive)
	slog.Info("mountos: windows detached", "drive", drive)
	return nil
}

func (c *windowsConnector) Open(parent context.Context, attachment Attachment) error {
	ctx, cancel, err := boundedContext(parent, openTimeout)
	if err != nil {
		return err
	}
	defer cancel()

	c.mu.Lock()
	defer c.mu.Unlock()
	drive, active, ok := c.ownedDrive(attachment)
	if !ok {
		return ErrInvalidAttachment
	}
	current, mapped, err := c.remoteFor(drive)
	if err != nil {
		return ErrVerificationFailed
	}
	if !mapped || current != active.remote {
		return ErrAttachmentChanged
	}
	remoteDrive, err := c.isRemoteDrive(attachment.location)
	if err != nil {
		return ErrVerificationFailed
	}
	if !remoteDrive {
		return ErrAttachmentChanged
	}
	_, explorerExecutable, err := c.systemPaths()
	if err != nil {
		return ErrOpenFailed
	}
	if err := c.runner.Run(ctx, windowsOpenPlan(explorerExecutable, attachment.location)); err != nil {
		return ErrOpenFailed
	}
	return nil
}

func (c *windowsConnector) ownedDrive(attachment Attachment) (string, windowsActive, bool) {
	if attachment.owner != c.owner || attachment.kind != KindWindows || len(attachment.location) != 3 {
		return "", windowsActive{}, false
	}
	drive := attachment.location[:2]
	active, exists := c.active[drive]
	return drive, active, exists && attachment.id != 0 && active.id == attachment.id
}

func (c *windowsConnector) nextID() uint64 {
	c.serial++
	if c.serial == 0 {
		c.serial++
	}
	return c.serial
}

func (c *windowsConnector) rollbackUnknown(parent context.Context, netExecutable, drive, endpoint string) {
	remote, mapped, err := c.remoteFor(drive)
	if err != nil || !mapped || !windowsRemoteMatchesEndpoint(remote, endpoint) {
		return
	}
	c.rollback(parent, netExecutable, drive, remote)
}

func (c *windowsConnector) rollback(parent context.Context, netExecutable, drive, expectedRemote string) {
	current, mapped, err := c.remoteFor(drive)
	if err != nil || !mapped || current != expectedRemote {
		return
	}
	var cleanupParent context.Context = context.Background()
	if parent != nil {
		cleanupParent = context.WithoutCancel(parent)
	}
	ctx, cancel := context.WithTimeout(cleanupParent, detachTimeout)
	defer cancel()
	_ = c.runner.Run(ctx, windowsDetachPlan(netExecutable, drive))
	_, _, _ = c.remoteFor(drive)
}

func windowsSystemPaths() (string, string, error) {
	systemDir, err := windows.GetSystemDirectory()
	if err != nil || !filepath.IsAbs(systemDir) {
		return "", "", ErrAttachFailed
	}
	windowsDir, err := windows.GetWindowsDirectory()
	if err != nil || !filepath.IsAbs(windowsDir) {
		return "", "", ErrAttachFailed
	}
	return filepath.Join(systemDir, "net.exe"), filepath.Join(windowsDir, "explorer.exe"), nil
}

func windowsRemoteFor(drive string) (string, bool, error) {
	localName, err := windows.UTF16PtrFromString(drive)
	if err != nil {
		return "", false, err
	}
	size := uint32(512)
	for attempts := 0; attempts < 2; attempts++ {
		buffer := make([]uint16, size)
		result, _, _ := wnetGetConnection.Call(
			uintptr(unsafe.Pointer(localName)),
			uintptr(unsafe.Pointer(&buffer[0])),
			uintptr(unsafe.Pointer(&size)),
		)
		status := syscall.Errno(result)
		switch status {
		case 0:
			return windows.UTF16ToString(buffer), true, nil
		case windows.ERROR_NOT_CONNECTED, windows.ERROR_CONNECTION_UNAVAIL:
			return "", false, nil
		case windows.ERROR_MORE_DATA:
			continue
		default:
			return "", false, status
		}
	}
	return "", false, windows.ERROR_MORE_DATA
}

func windowsIsRemoteDrive(root string) (bool, error) {
	rootName, err := windows.UTF16PtrFromString(root)
	if err != nil {
		return false, err
	}
	return windows.GetDriveType(rootName) == windows.DRIVE_REMOTE, nil
}

func windowsRemoteMatchesEndpoint(remote, endpoint string) bool {
	if remote == endpoint {
		return true
	}
	remainder := strings.TrimPrefix(endpoint, "http://")
	separator := strings.IndexByte(remainder, '/')
	if separator < 0 {
		return false
	}
	host := strings.ReplaceAll(remainder[:separator], ":", "@")
	path := strings.ReplaceAll(strings.TrimSuffix(remainder[separator:], "/"), "/", `\`)
	want := `\\` + host + `\DavWWWRoot` + path
	return strings.EqualFold(strings.TrimSuffix(remote, `\`), strings.TrimSuffix(want, `\`))
}
