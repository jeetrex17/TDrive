// Package mountdav exposes one pinned TDrive as a local, read-only WebDAV
// endpoint. It deliberately does not implement write verbs or disk caching.
package mountdav

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/http"
	"runtime"
	"strings"
	"sync"
	"time"

	"TDrive/backend/mountfs"

	"golang.org/x/net/webdav"
)

const (
	defaultWindowsDrive      = "T:"
	defaultReadHeaderTimeout = 5 * time.Second
	defaultReadTimeout       = 15 * time.Second
	defaultIdleTimeout       = 60 * time.Second
	defaultShutdownTimeout   = 5 * time.Second
	defaultMaxHeaderBytes    = 32 << 10
	sanitizedServeError      = "WebDAV server stopped unexpectedly"
)

var ErrAlreadyRunning = errors.New("mountdav: server already running with different configuration")

type StartConfig struct {
	FS           *mountfs.FS
	DriveID      int64
	DriveTitle   string
	WindowsDrive string
}

type Status struct {
	Running      bool     `json:"running"`
	URL          string   `json:"url,omitempty"`
	Mode         string   `json:"mode,omitempty"`
	DriveID      int64    `json:"drive_id,omitempty"`
	DriveTitle   string   `json:"drive_title,omitempty"`
	WindowsDrive string   `json:"windows_drive,omitempty"`
	Commands     Commands `json:"commands,omitempty"`
	Error        string   `json:"error,omitempty"`
}

type Commands struct {
	WindowsMap    string `json:"windows_map,omitempty"`
	WindowsUnmap  string `json:"windows_unmap,omitempty"`
	MacFinder     string `json:"mac_finder,omitempty"`
	LinuxMount    string `json:"linux_mount,omitempty"`
	LinuxUnmount  string `json:"linux_unmount,omitempty"`
	ActiveOSMount string `json:"active_os_mount,omitempty"`
}

type activeConfig struct {
	fs           *mountfs.FS
	driveID      int64
	driveTitle   string
	windowsDrive string
}

type Server struct {
	mu              sync.Mutex
	server          *http.Server
	listener        net.Listener
	active          activeConfig
	status          Status
	shutdownTimeout time.Duration
}

func NewServer() *Server {
	return &Server{}
}

func (server *Server) Start(ctx context.Context, config StartConfig) (Status, error) {
	active, err := validateStartConfig(ctx, config)
	if err != nil {
		return Status{}, err
	}

	server.mu.Lock()
	defer server.mu.Unlock()
	if server.server != nil {
		if server.active == active {
			return server.status, nil
		}
		return Status{}, ErrAlreadyRunning
	}

	capability, err := randomCapability()
	if err != nil {
		return Status{}, err
	}
	capabilityPath := "/" + capability
	listener, err := (&net.ListenConfig{}).Listen(ctx, "tcp4", "127.0.0.1:0")
	if err != nil {
		return Status{}, fmt.Errorf("mountdav: listen on loopback: %w", err)
	}
	authority := listener.Addr().String()
	filesystem := NewFileSystem(config.FS)
	application := &readApplication{
		capabilityPath: capabilityPath,
		fs:             filesystem,
		lockSystem:     webdav.NewMemLS(),
	}
	handler := newProtectedHandler(protectionConfig{
		capabilityPath: capabilityPath,
		authority:      authority,
		maxConcurrent:  defaultMaxConcurrent,
	}, application)
	httpServer := &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: defaultReadHeaderTimeout,
		ReadTimeout:       defaultReadTimeout,
		IdleTimeout:       defaultIdleTimeout,
		MaxHeaderBytes:    defaultMaxHeaderBytes,
	}
	url := "http://" + authority + capabilityPath + "/"
	status := Status{
		Running:      true,
		URL:          url,
		Mode:         "read-only",
		DriveID:      active.driveID,
		DriveTitle:   active.driveTitle,
		WindowsDrive: active.windowsDrive,
		Commands:     commandHints(url, active.windowsDrive),
	}
	server.server = httpServer
	server.listener = listener
	server.active = active
	server.status = status
	go server.serve(httpServer, listener)
	return status, nil
}

func (server *Server) Status() Status {
	server.mu.Lock()
	defer server.mu.Unlock()
	return server.status
}

func (server *Server) Stop(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		timeout := server.shutdownTimeout
		if timeout <= 0 {
			timeout = defaultShutdownTimeout
		}
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	server.mu.Lock()
	httpServer := server.server
	listener := server.listener
	server.server = nil
	server.listener = nil
	server.active = activeConfig{}
	server.status = Status{}
	server.mu.Unlock()
	if httpServer == nil {
		return nil
	}

	shutdownErr := httpServer.Shutdown(ctx)
	if shutdownErr != nil {
		_ = httpServer.Close()
	}
	if listener != nil {
		_ = listener.Close()
	}
	if shutdownErr != nil && !errors.Is(shutdownErr, http.ErrServerClosed) {
		return fmt.Errorf("mountdav: shutdown: %w", shutdownErr)
	}
	return nil
}

func (server *Server) serve(httpServer *http.Server, listener net.Listener) {
	err := httpServer.Serve(listener)
	if err == nil || errors.Is(err, http.ErrServerClosed) {
		return
	}
	_ = listener.Close()
	_ = httpServer.Close()
	server.recordServeFailure(httpServer)
}

func (server *Server) recordServeFailure(failed *http.Server) {
	server.mu.Lock()
	defer server.mu.Unlock()
	if server.server != failed {
		return
	}
	server.server = nil
	server.listener = nil
	server.active = activeConfig{}
	server.status = Status{
		Mode:         server.status.Mode,
		DriveID:      server.status.DriveID,
		DriveTitle:   server.status.DriveTitle,
		WindowsDrive: server.status.WindowsDrive,
		Error:        sanitizedServeError,
	}
}

func validateStartConfig(ctx context.Context, config StartConfig) (activeConfig, error) {
	if ctx == nil {
		return activeConfig{}, fmt.Errorf("mountdav: context required")
	}
	if err := ctx.Err(); err != nil {
		return activeConfig{}, fmt.Errorf("mountdav: start canceled: %w", err)
	}
	if config.FS == nil {
		return activeConfig{}, fmt.Errorf("mountdav: filesystem required")
	}
	if config.DriveID <= 0 {
		return activeConfig{}, fmt.Errorf("mountdav: positive drive id required")
	}
	drive, err := validateWindowsDrive(config.WindowsDrive)
	if err != nil {
		return activeConfig{}, err
	}
	return activeConfig{
		fs:           config.FS,
		driveID:      config.DriveID,
		driveTitle:   config.DriveTitle,
		windowsDrive: drive,
	}, nil
}

func validateWindowsDrive(value string) (string, error) {
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
	return "", fmt.Errorf("mountdav: invalid Windows drive %q", value)
}

func randomCapability() (string, error) {
	var entropy [32]byte
	if _, err := rand.Read(entropy[:]); err != nil {
		return "", fmt.Errorf("mountdav: generate capability: %w", err)
	}
	return "tdrive-" + hex.EncodeToString(entropy[:]), nil
}

func commandHints(url, windowsDrive string) Commands {
	commands := Commands{
		WindowsMap:   fmt.Sprintf("net use %s %s /persistent:no", windowsDrive, url),
		WindowsUnmap: fmt.Sprintf("net use %s /delete", windowsDrive),
		MacFinder:    fmt.Sprintf("Finder > Go > Connect to Server... > %s", url),
		LinuxMount:   fmt.Sprintf("gio mount %s", url),
		LinuxUnmount: fmt.Sprintf("gio mount -u %s", url),
	}
	switch runtime.GOOS {
	case "windows":
		commands.ActiveOSMount = commands.WindowsMap
	case "darwin":
		commands.ActiveOSMount = commands.MacFinder
	case "linux":
		commands.ActiveOSMount = commands.LinuxMount
	}
	return commands
}
