package mountos

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"os/exec"
	"time"
)

const (
	attachTimeout = 30 * time.Second
	detachTimeout = 20 * time.Second
	openTimeout   = 10 * time.Second
)

type commandRunner interface {
	Run(context.Context, commandPlan) error
}

type commandOutputRunner interface {
	Output(context.Context, commandPlan, int) ([]byte, error)
}

type execCommandRunner struct{}

func (execCommandRunner) Run(ctx context.Context, plan commandPlan) error {
	// Never log plan.Args: for the mount/unmount commands this package builds,
	// they carry the loopback capability URL (see commandPlan.Args callers in
	// plans.go), which is a bearer credential for this mount's endpoint.
	slog.Debug("mountos: running command", "path", plan.Path)
	command := exec.CommandContext(ctx, plan.Path, plan.Args...)
	// OS command output can repeat the capability URL. It is neither retained
	// nor surfaced, which also keeps output memory strictly bounded.
	command.Stdout = io.Discard
	command.Stderr = io.Discard
	err := command.Run()
	if err != nil {
		slog.Debug("mountos: command failed", "path", plan.Path, "error", err)
	}
	return err
}

func (execCommandRunner) Output(ctx context.Context, plan commandPlan, limit int) ([]byte, error) {
	if limit <= 0 {
		return nil, errors.New("invalid output limit")
	}
	slog.Debug("mountos: running command for output", "path", plan.Path, "limit", limit)
	output := &limitedBuffer{remaining: limit}
	command := exec.CommandContext(ctx, plan.Path, plan.Args...)
	command.Stdout = output
	command.Stderr = io.Discard
	if err := command.Run(); err != nil {
		slog.Debug("mountos: command for output failed", "path", plan.Path, "error", err)
		return nil, err
	}
	return output.bytes(), nil
}

type limitedBuffer struct {
	buffer    bytes.Buffer
	remaining int
}

func (b *limitedBuffer) Write(value []byte) (int, error) {
	if len(value) > b.remaining {
		written := 0
		if b.remaining > 0 {
			written, _ = b.buffer.Write(value[:b.remaining])
			b.remaining = 0
		}
		return written, errors.New("command output limit exceeded")
	}
	written, err := b.buffer.Write(value)
	b.remaining -= written
	return written, err
}

func (b *limitedBuffer) bytes() []byte {
	return append([]byte(nil), b.buffer.Bytes()...)
}

func (b *limitedBuffer) String() string { return b.buffer.String() }

func boundedContext(parent context.Context, timeout time.Duration) (context.Context, context.CancelFunc, error) {
	if parent == nil {
		return nil, nil, ErrInvalidContext
	}
	ctx, cancel := context.WithTimeout(parent, timeout)
	return ctx, cancel, nil
}
