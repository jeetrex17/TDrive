//go:build linux

package mountos

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"sync"
	"time"
)

const (
	maxGIOListBytes                 = 1 << 20
	linuxAttachVerificationTimeout  = 2 * time.Second
	linuxAttachVerificationInterval = 100 * time.Millisecond
)

type contextDelay func(context.Context, time.Duration) error

type linuxDependencies struct {
	runner              commandRunner
	outputRunner        commandOutputRunner
	inspectMount        func(context.Context, string) (bool, error)
	delay               contextDelay
	verificationTimeout time.Duration
}

type linuxConnector struct {
	mu            sync.Mutex
	owner         *ownerMarker
	runner        commandRunner
	inspectMount  func(context.Context, string) (bool, error)
	delay         contextDelay
	verifyTimeout time.Duration
	serial        uint64
	active        map[uint64]string
	byEndpoint    map[string]uint64
}

func newPlatformConnector() Connector {
	return newLinuxConnector(linuxDependencies{})
}

func newLinuxConnector(dependencies linuxDependencies) *linuxConnector {
	runner := dependencies.runner
	if runner == nil {
		runner = execCommandRunner{}
	}
	inspectMount := dependencies.inspectMount
	if inspectMount == nil {
		outputRunner := dependencies.outputRunner
		if outputRunner == nil {
			if combined, ok := runner.(commandOutputRunner); ok {
				outputRunner = combined
			} else {
				outputRunner = execCommandRunner{}
			}
		}
		inspectMount = func(ctx context.Context, endpoint string) (bool, error) {
			return inspectLinuxMount(ctx, outputRunner, endpoint)
		}
	}
	delay := dependencies.delay
	if delay == nil {
		delay = waitForPoll
	}
	verificationTimeout := dependencies.verificationTimeout
	if verificationTimeout <= 0 {
		verificationTimeout = linuxAttachVerificationTimeout
	}
	return &linuxConnector{
		owner:         &ownerMarker{},
		runner:        runner,
		inspectMount:  inspectMount,
		delay:         delay,
		verifyTimeout: verificationTimeout,
		active:        make(map[uint64]string),
		byEndpoint:    make(map[string]uint64),
	}
}

func (c *linuxConnector) Attach(parent context.Context, config Config) (Attachment, error) {
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
	// mountURI embeds the loopback capability token (see linuxWebDAVURI) and
	// must never be logged; log validated.label (the Finder/GIO-visible name)
	// and attachment IDs instead.
	mountURI := linuxWebDAVURI(validated.endpoint)
	if _, exists := c.byEndpoint[mountURI]; exists {
		slog.Warn("mountos: linux attach rejected, endpoint already tracked", "label", validated.label)
		return Attachment{}, ErrAttachFailed
	}
	mounted, err := c.inspectMount(ctx, mountURI)
	if err != nil {
		slog.Warn("mountos: linux desktop mount inspection failed", "label", validated.label, "error", err)
		return Attachment{}, linuxUnavailableOrContext(ctx, ErrLinuxDesktopUnavailable)
	}
	if mounted {
		slog.Warn("mountos: linux attach found an unexpected pre-existing mount", "label", validated.label)
		return Attachment{}, ErrAttachmentChanged
	}
	slog.Debug("mountos: linux attaching", "label", validated.label, "mode", validated.mode)
	if err := c.runner.Run(ctx, linuxAttachPlan(mountURI)); err != nil {
		slog.Warn("mountos: linux gio mount failed", "label", validated.label, "error", err)
		return Attachment{}, linuxUnavailableOrContext(ctx, ErrLinuxWebDAVUnavailable)
	}
	if err := c.verifyAttached(ctx, mountURI); err != nil {
		slog.Warn("mountos: linux attach verification failed", "label", validated.label, "error", err)
		c.rollback(parent, mountURI)
		return Attachment{}, err
	}
	id := c.nextID()
	c.active[id] = mountURI
	c.byEndpoint[mountURI] = id
	slog.Info("mountos: linux attached", "label", validated.label, "mode", validated.mode)
	return Attachment{
		owner:    c.owner,
		id:       id,
		kind:     KindLinux,
		location: validated.label,
	}, nil
}

func (c *linuxConnector) verifyAttached(parent context.Context, mountURI string) error {
	ctx, cancel := context.WithTimeout(parent, c.verifyTimeout)
	defer cancel()

	for {
		mounted, err := c.inspectMount(ctx, mountURI)
		if err != nil {
			if parentErr := parent.Err(); parentErr != nil {
				return parentErr
			}
			if ctx.Err() != nil {
				return ErrVerificationFailed
			}
			return ErrLinuxDesktopUnavailable
		}
		if mounted {
			return nil
		}
		if err := c.delay(ctx, linuxAttachVerificationInterval); err != nil {
			if parentErr := parent.Err(); parentErr != nil {
				return parentErr
			}
			return ErrVerificationFailed
		}
	}
}

func (c *linuxConnector) Detach(parent context.Context, attachment Attachment) error {
	ctx, cancel, err := boundedContext(parent, detachTimeout)
	if err != nil {
		return err
	}
	defer cancel()

	c.mu.Lock()
	defer c.mu.Unlock()
	endpoint, ok := c.ownedEndpoint(attachment)
	if !ok {
		return ErrInvalidAttachment
	}
	mounted, err := c.inspectMount(ctx, endpoint)
	if err != nil {
		return ErrVerificationFailed
	}
	if mounted {
		if err := c.runner.Run(ctx, linuxDetachPlan(endpoint)); err != nil {
			slog.Warn("mountos: linux gio unmount failed", "attachment_id", attachment.id, "error", err)
			return ErrDetachFailed
		}
		mounted, err = c.inspectMount(ctx, endpoint)
		if err != nil || mounted {
			return ErrVerificationFailed
		}
	}
	delete(c.active, attachment.id)
	delete(c.byEndpoint, endpoint)
	slog.Info("mountos: linux detached", "attachment_id", attachment.id)
	return nil
}

func (c *linuxConnector) Open(parent context.Context, attachment Attachment) error {
	ctx, cancel, err := boundedContext(parent, openTimeout)
	if err != nil {
		return err
	}
	defer cancel()

	c.mu.Lock()
	defer c.mu.Unlock()
	endpoint, ok := c.ownedEndpoint(attachment)
	if !ok {
		return ErrInvalidAttachment
	}
	mounted, err := c.inspectMount(ctx, endpoint)
	if err != nil {
		return ErrVerificationFailed
	}
	if !mounted {
		return ErrAttachmentChanged
	}
	if err := c.runner.Run(ctx, linuxOpenPlan(endpoint)); err != nil {
		return ErrOpenFailed
	}
	return nil
}

func (c *linuxConnector) ownedEndpoint(attachment Attachment) (string, bool) {
	if attachment.owner != c.owner || attachment.kind != KindLinux || attachment.id == 0 {
		return "", false
	}
	endpoint, exists := c.active[attachment.id]
	return endpoint, exists
}

func (c *linuxConnector) nextID() uint64 {
	c.serial++
	if c.serial == 0 {
		c.serial++
	}
	return c.serial
}

func (c *linuxConnector) rollback(parent context.Context, endpoint string) {
	var cleanupParent context.Context = context.Background()
	if parent != nil {
		cleanupParent = context.WithoutCancel(parent)
	}
	ctx, cancel := context.WithTimeout(cleanupParent, detachTimeout)
	defer cancel()
	_ = c.runner.Run(ctx, linuxDetachPlan(endpoint))
}

func linuxUnavailableOrContext(ctx context.Context, unavailable error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return unavailable
}

func inspectLinuxMount(ctx context.Context, runner commandOutputRunner, mountURI string) (bool, error) {
	output, err := runner.Output(ctx, linuxListPlan(), maxGIOListBytes)
	if err != nil {
		return false, err
	}
	for _, identifier := range linuxMountIdentifiers(mountURI) {
		if mountOutputContainsURI(output, identifier) {
			return true, nil
		}
	}
	return false, nil
}

func linuxMountIdentifiers(mountURI string) []string {
	path := strings.TrimPrefix(mountURI, "dav://")
	return []string{
		mountURI,
		"dav+http://" + path,
	}
}

func mountOutputContainsURI(output []byte, uri string) bool {
	for _, candidate := range []string{uri, strings.TrimSuffix(uri, "/")} {
		remaining := output
		for {
			index := bytes.Index(remaining, []byte(candidate))
			if index < 0 {
				break
			}
			after := index + len(candidate)
			beforeMatches := index == 0 || isURIOutputBoundary(remaining[index-1])
			afterMatches := after == len(remaining) || isURIOutputBoundary(remaining[after])
			if beforeMatches && afterMatches {
				return true
			}
			remaining = remaining[index+1:]
		}
	}
	return false
}

func isURIOutputBoundary(value byte) bool {
	switch value {
	case ' ', '\t', '\r', '\n', '(', ')', '[', ']', '<', '>', '\'', '"', ',':
		return true
	default:
		return false
	}
}

func linuxWebDAVURI(endpoint string) string {
	return "dav://" + strings.TrimPrefix(endpoint, "http://")
}
