package mountcontroller

import (
	"context"
	"fmt"

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
	status, err := endpoint.server.Start(ctx, mountdav.StartConfig{
		FS:           config.FS,
		DriveID:      config.DriveID,
		DriveTitle:   config.DriveTitle,
		WindowsDrive: config.WindowsDrive,
		Writable:     writable,
		Writer:       writer,
	})
	if err != nil {
		return EndpointStatus{}, err
	}
	return EndpointStatus{Endpoint: status.URL}, nil
}

func (endpoint *webDAVEndpoint) Health() EndpointHealth {
	status := endpoint.server.Status()
	return EndpointHealth{Running: status.Running}
}

func (endpoint *webDAVEndpoint) Stop(ctx context.Context) error {
	return endpoint.server.Stop(ctx)
}
