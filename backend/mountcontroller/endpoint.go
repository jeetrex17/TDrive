package mountcontroller

import (
	"context"

	"TDrive/backend/mountdav"
)

type webDAVEndpoint struct {
	server *mountdav.Server
}

func newWebDAVEndpoint() Endpoint {
	return &webDAVEndpoint{server: mountdav.NewServer()}
}

func (endpoint *webDAVEndpoint) Start(ctx context.Context, config EndpointConfig) (EndpointStatus, error) {
	status, err := endpoint.server.Start(ctx, mountdav.StartConfig{
		FS:           config.FS,
		DriveID:      config.DriveID,
		DriveTitle:   config.DriveTitle,
		WindowsDrive: config.WindowsDrive,
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
