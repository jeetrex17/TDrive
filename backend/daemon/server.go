package daemon

import (
	"TDrive/backend/applog"
	"TDrive/backend/core"
	"TDrive/backend/mountlifecycle"
	"TDrive/backend/processlock"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"sync"
	"time"
)

const (
	// Writable shutdown may spend 30 seconds finishing accepted mutations.
	// Preserve the platform's 20-second detach budget and another five seconds
	// for local endpoint/writer cleanup after the OS releases the mount.
	daemonMountShutdownTimeout = 55 * time.Second
)

type ServerConfig struct {
	CoreConfig core.Config
	Warnf      func(format string, args ...any)
}

type Server struct {
	engine *core.Engine
	lock   *processlock.Lock
	// mountMu only protects lazy controller construction. The controller owns
	// its own lifecycle state and never takes the daemon-wide write lock.
	mountMu         sync.Mutex
	mountController daemonMountController
	// mountEncryptionPolicyRefresh is injected only by tests. Production uses
	// Engine.EnsureEncryptionPolicy to establish full-history authority.
	mountEncryptionPolicyRefresh func(context.Context, int64) error
	// mountLifecycle serializes mount Start/Stop/Close with vault lock/logout.
	// The gate must cover both controller shutdown and encryption-key erasure so
	// a racing Start cannot observe a stale unlocked vault.
	mountLifecycle         mountlifecycle.Gate
	mountLifecycleTerminal bool // guarded by mountLifecycle
	warnf                  func(format string, args ...any)
	state                  *state

	mu        sync.Mutex
	eventMu   sync.Mutex
	eventSubs map[chan Event]struct{}
	// writeMu serializes operations that mutate daemon state or the remote
	// projection. The daemon can accept concurrent clients, but the underlying
	// Telegram send + local projection sequence is intentionally single-writer.
	writeMu sync.Mutex
	// streamMu keeps progress events request-scoped in v1. Backend services emit
	// global transfer events, and download_progress has no transfer id, so only
	// one streaming transfer may be active until events carry a request id.
	streamMu sync.Mutex
	stopOnce sync.Once
	stop     context.CancelFunc
	wg       sync.WaitGroup
}

func Run(ctx context.Context, cfg ServerConfig) error {
	if ctx == nil {
		ctx = context.Background()
	}
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	s := &Server{
		warnf: cfg.Warnf,
		stop:  cancel,
	}
	if s.warnf == nil {
		s.warnf = func(format string, args ...any) {
			fmt.Printf(format, args...)
		}
	}
	if cfg.CoreConfig.Events == nil {
		cfg.CoreConfig.Events = s
	} else {
		cfg.CoreConfig.Events = multiEventSink{s, cfg.CoreConfig.Events}
	}

	lock, err := processlock.Acquire("daemon")
	if err != nil {
		return err
	}
	s.lock = lock
	defer func() {
		if err := s.lock.Release(); err != nil {
			s.warnf("daemon: release lock: %v\n", err)
		}
	}()
	applog.Init()
	defer applog.Close()

	engine, err := core.New(runCtx, cfg.CoreConfig)
	if err != nil {
		return err
	}
	s.engine = engine
	defer s.engine.Close()
	defer s.wg.Wait()
	defer func() {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), daemonMountShutdownTimeout)
		defer stopCancel()
		if err := s.stopMountServer(stopCtx); err != nil {
			s.warnf("daemon: stop mount: %v\n", err)
		}
	}()

	if err := s.loadState(); err != nil {
		s.warnf("daemon: load cli state: %v\n", err)
	}

	socketPath, err := SocketPath()
	if err != nil {
		return err
	}
	ln, err := listenSocket(socketPath)
	if err != nil {
		return err
	}
	defer cleanupSocket(socketPath)
	defer func() { _ = ln.Close() }()

	go func() {
		<-runCtx.Done()
		_ = ln.Close()
	}()

	slog.Info("daemon: listening")
	s.warnf("TDrive daemon listening on %s\n", socketPath)
	for {
		conn, err := ln.Accept()
		if err != nil {
			if runCtx.Err() != nil || errors.Is(err, net.ErrClosed) {
				slog.Info("daemon: listener closed, shutting down")
				return nil
			}
			slog.Warn("daemon: accept failed", "error", err)
			s.warnf("daemon: accept: %v\n", err)
			continue
		}
		s.wg.Go(func() {
			s.handleConn(runCtx, conn)
		})
	}
}
