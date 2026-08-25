//go:build darwin

package mountos

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"golang.org/x/sys/unix"
)

type darwinDependencies struct {
	runner       commandRunner
	userCacheDir func() (string, error)
	inspectMount func(string) (darwinMountInspection, error)
}

type darwinMountInspection struct {
	mounted  bool
	readOnly bool
	source   string
}

type darwinConnector struct {
	mu           sync.Mutex
	owner        *ownerMarker
	runner       commandRunner
	userCacheDir func() (string, error)
	inspectMount func(string) (darwinMountInspection, error)
	serial       uint64
	active       map[string]darwinActive
}

type darwinActive struct {
	id       uint64
	endpoint string
	mode     Mode
}

// macOS exposes a legacy f_mntfromname[90] value for WebDAV mounts: at most
// 89 URL bytes plus the terminating NUL. TDrive's 256-bit capability makes
// the reported prefix long enough to retain more than 200 bits of identity.
const darwinLegacyMountSourceMaxBytes = 89

func newPlatformConnector() Connector {
	return newDarwinConnector(darwinDependencies{})
}

func newDarwinConnector(dependencies darwinDependencies) *darwinConnector {
	runner := dependencies.runner
	if runner == nil {
		runner = execCommandRunner{}
	}
	userCacheDir := dependencies.userCacheDir
	if userCacheDir == nil {
		userCacheDir = os.UserCacheDir
	}
	inspectMount := dependencies.inspectMount
	if inspectMount == nil {
		inspectMount = inspectDarwinMount
	}
	return &darwinConnector{
		owner:        &ownerMarker{},
		runner:       runner,
		userCacheDir: userCacheDir,
		inspectMount: inspectMount,
		active:       make(map[string]darwinActive),
	}
}

func (c *darwinConnector) Attach(parent context.Context, config Config) (Attachment, error) {
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

	target, err := c.createMountpoint()
	if err != nil {
		return Attachment{}, ErrAttachFailed
	}
	succeeded := false
	defer func() {
		if !succeeded {
			c.rollback(parent, target, validated.endpoint)
		}
	}()

	if err := c.runner.Run(ctx, darwinAttachPlan(validated.endpoint, validated.label, target, validated.mode)); err != nil {
		return Attachment{}, ErrAttachFailed
	}
	inspection, err := c.inspectMount(target)
	if err != nil || !inspection.mounted || !darwinModeMatchesInspection(validated.mode, inspection) || !darwinSourceMatchesEndpoint(inspection.source, validated.endpoint) {
		return Attachment{}, ErrVerificationFailed
	}

	id := c.nextID()
	c.active[target] = darwinActive{id: id, endpoint: validated.endpoint, mode: validated.mode}
	succeeded = true
	return Attachment{
		owner:    c.owner,
		id:       id,
		kind:     KindDarwin,
		location: target,
	}, nil
}

func (c *darwinConnector) Detach(parent context.Context, attachment Attachment) error {
	ctx, cancel, err := boundedContext(parent, detachTimeout)
	if err != nil {
		return err
	}
	defer cancel()

	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.owns(attachment) {
		return ErrInvalidAttachment
	}

	active := c.active[attachment.location]
	inspection, err := c.inspectMount(attachment.location)
	if err != nil {
		return ErrVerificationFailed
	}
	if inspection.mounted && (!darwinModeMatchesInspection(active.mode, inspection) || !darwinSourceMatchesEndpoint(inspection.source, active.endpoint)) {
		return ErrAttachmentChanged
	}
	if inspection.mounted {
		if err := c.runner.Run(ctx, darwinDetachPlan(attachment.location)); err != nil {
			return ErrDetachFailed
		}
		inspection, err = c.inspectMount(attachment.location)
		if err != nil || inspection.mounted {
			return ErrVerificationFailed
		}
	}
	if err := removeEmptyDirectory(attachment.location); err != nil {
		return ErrDetachFailed
	}
	delete(c.active, attachment.location)
	return nil
}

func (c *darwinConnector) Open(parent context.Context, attachment Attachment) error {
	ctx, cancel, err := boundedContext(parent, openTimeout)
	if err != nil {
		return err
	}
	defer cancel()

	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.owns(attachment) {
		return ErrInvalidAttachment
	}
	active := c.active[attachment.location]
	inspection, err := c.inspectMount(attachment.location)
	if err != nil {
		return ErrVerificationFailed
	}
	if !inspection.mounted || !darwinModeMatchesInspection(active.mode, inspection) || !darwinSourceMatchesEndpoint(inspection.source, active.endpoint) {
		return ErrAttachmentChanged
	}
	if err := c.runner.Run(ctx, darwinOpenPlan(attachment.location)); err != nil {
		return ErrOpenFailed
	}
	return nil
}

func darwinModeMatchesInspection(mode Mode, inspection darwinMountInspection) bool {
	if mode == ModeReadWrite {
		return !inspection.readOnly
	}
	return inspection.readOnly
}

func (c *darwinConnector) owns(attachment Attachment) bool {
	if attachment.owner != c.owner || attachment.kind != KindDarwin || attachment.location == "" {
		return false
	}
	active, exists := c.active[attachment.location]
	return exists && attachment.id != 0 && active.id == attachment.id
}

func (c *darwinConnector) nextID() uint64 {
	c.serial++
	if c.serial == 0 {
		c.serial++
	}
	return c.serial
}

func (c *darwinConnector) createMountpoint() (string, error) {
	cacheDir, err := c.userCacheDir()
	if err != nil || !filepath.IsAbs(cacheDir) {
		return "", ErrAttachFailed
	}
	appDir := filepath.Join(cacheDir, "TDrive")
	if err := ensurePrivateDirectory(appDir); err != nil {
		return "", err
	}
	mountsDir := filepath.Join(appDir, "mounts")
	if err := ensurePrivateDirectory(mountsDir); err != nil {
		return "", err
	}
	target, err := os.MkdirTemp(mountsDir, "mount-")
	if err != nil {
		return "", err
	}
	if err := os.Chmod(target, 0o700); err != nil {
		_ = os.Remove(target)
		return "", err
	}
	info, err := os.Lstat(target)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm() != 0o700 {
		_ = os.Remove(target)
		return "", ErrAttachFailed
	}
	entries, err := os.ReadDir(target)
	if err != nil || len(entries) != 0 {
		_ = os.Remove(target)
		return "", ErrAttachFailed
	}
	return target, nil
}

func (c *darwinConnector) rollback(parent context.Context, target, expectedEndpoint string) {
	inspection, err := c.inspectMount(target)
	if err != nil {
		return
	}
	if !inspection.mounted {
		_ = removeEmptyDirectory(target)
		return
	}
	if !darwinSourceMatchesEndpoint(inspection.source, expectedEndpoint) {
		return
	}
	cleanupParent := context.Background()
	if parent != nil {
		cleanupParent = context.WithoutCancel(parent)
	}
	ctx, cancel := context.WithTimeout(cleanupParent, detachTimeout)
	defer cancel()
	_ = c.runner.Run(ctx, darwinDetachPlan(target))
	_ = removeEmptyDirectory(target)
}

func ensurePrivateDirectory(path string) error {
	if err := os.MkdirAll(path, 0o700); err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return ErrAttachFailed
	}
	return os.Chmod(path, 0o700)
}

func removeEmptyDirectory(path string) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return ErrDetachFailed
	}
	entries, err := os.ReadDir(path)
	if err != nil || len(entries) != 0 {
		return ErrDetachFailed
	}
	return os.Remove(path)
}

func inspectDarwinMount(target string) (darwinMountInspection, error) {
	var stat unix.Statfs_t
	if err := unix.Statfs(target, &stat); err != nil {
		return darwinMountInspection{}, err
	}
	mountpoint := nulTerminatedString(stat.Mntonname[:])
	filesystem := nulTerminatedString(stat.Fstypename[:])
	if filepath.Clean(mountpoint) != filepath.Clean(target) || filesystem != "webdav" {
		return darwinMountInspection{}, nil
	}
	return darwinMountInspection{
		mounted:  true,
		readOnly: stat.Flags&unix.MNT_RDONLY != 0,
		source:   nulTerminatedString(stat.Mntfromname[:]),
	}, nil
}

func darwinSourceMatchesEndpoint(source, endpoint string) bool {
	reported := strings.TrimSuffix(source, "/")
	expected := strings.TrimSuffix(endpoint, "/")
	if reported == expected {
		return true
	}
	return len(source) == darwinLegacyMountSourceMaxBytes &&
		len(endpoint) > len(source) &&
		strings.HasPrefix(endpoint, source)
}

func nulTerminatedString(value []byte) string {
	for index, char := range value {
		if char == 0 {
			return string(value[:index])
		}
	}
	return string(value)
}
