//go:build !darwin && !linux && !windows

package mountos

import "context"

type unsupportedConnector struct{}

func newPlatformConnector() Connector { return unsupportedConnector{} }

func (unsupportedConnector) Attach(ctx context.Context, config Config) (Attachment, error) {
	if ctx == nil {
		return Attachment{}, ErrInvalidContext
	}
	if _, err := validateConfig(config); err != nil {
		return Attachment{}, err
	}
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
