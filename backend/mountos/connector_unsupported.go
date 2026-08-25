//go:build !darwin && !linux && !windows

package mountos

import (
	"context"
	"log/slog"
)

type unsupportedConnector struct{}

func newPlatformConnector() Connector { return unsupportedConnector{} }

func (unsupportedConnector) Attach(ctx context.Context, config Config) (Attachment, error) {
	if ctx == nil {
		return Attachment{}, ErrInvalidContext
	}
	if _, err := validateConfig(config); err != nil {
		return Attachment{}, err
	}
	slog.Warn("mountos: attach requested on an unsupported platform")
	return Attachment{}, ErrNotSupported
}

func (unsupportedConnector) Detach(ctx context.Context, attachment Attachment) error {
	if ctx == nil {
		return ErrInvalidContext
	}
	return ErrNotSupported
}

func (unsupportedConnector) Open(ctx context.Context, attachment Attachment) error {
	if ctx == nil {
		return ErrInvalidContext
	}
	return ErrNotSupported
}
