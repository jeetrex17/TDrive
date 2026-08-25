package mountcontroller

import (
	"context"
	"fmt"
	"log/slog"

	"TDrive/backend/mountdav"
)

type webDAVEndpoint struct {
	server *mountdav.Server
}

func newWebDAVEndpoint() Endpoint {
	return &webDAVEndpoint{server: mountdav.NewServer()}
}

func (endpoint *webDAVEndpoint) Start(ctx context.Context, config EndpointConfig) (EndpointStatus, error) {
	var writer mountdav.WriteCoordinator
	writable := config.Mode == ModeReadWrite
	if writable {
		var ok bool
		writer, ok = config.Writer.(mountdav.WriteCoordinator)
		if !ok || writer == nil {
			return EndpointStatus{}, fmt.Errorf("%w: complete WebDAV writer is required", ErrInvalidConfiguration)
		}
	} else if config.Writer != nil {
		return EndpointStatus{}, fmt.Errorf("%w: writer supplied for read-only mount", ErrInvalidConfiguration)
	}
	slog.Debug("mountcontroller: starting WebDAV endpoint", "drive_id", config.DriveID, "drive_title", config.DriveTitle, "writable", writable)
	status, err := endpoint.server.Start(ctx, mountdav.StartConfig{
		FS:           config.FS,
		DriveID:      config.DriveID,
		DriveTitle:   config.DriveTitle,
		WindowsDrive: config.WindowsDrive,
		Writable:     writable,
		Writer:       writer,
	})
	if err != nil {
		slog.Warn("mountcontroller: WebDAV endpoint start failed", "drive_id", config.DriveID, "error", err)
		return EndpointStatus{}, err
	}
	// status.URL is never logged: it carries the loopback capability token.
	return EndpointStatus{Endpoint: status.URL}, nil
}

func (endpoint *webDAVEndpoint) Health() EndpointHealth {
	status := endpoint.server.Status()
	return EndpointHealth{Running: status.Running}
}

func (endpoint *webDAVEndpoint) Stop(ctx context.Context) error {
	slog.Debug("mountcontroller: stopping WebDAV endpoint")
	return endpoint.server.Stop(ctx)
}
