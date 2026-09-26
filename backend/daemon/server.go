// Package daemon runs the headless TDrive backend and exposes it to the tdrive
// CLI over a local socket. It is the sole holder of the core engine, the
// Telegram session, the unlocked vault key, the mount controller and the
// persisted CLI shell state — the CLI keeps nothing between invocations, which
// is why pwd and cd are round trips rather than process state.
//
// Server and client live in the same package deliberately, so a protocol change
// cannot land on one side only. There is no backward compatibility: every
// request carries the protocol version and a mismatch is a hard error, because
// an upgrade routinely leaves a new CLI talking to the daemon the previous
// install left running.
//
// The wire is newline-delimited JSON. Exactly three commands stream — login,
// upload and download — and a streaming request ends its connection. Events are
// broadcast to all subscribers with a non-blocking send and dropped on
// overflow: progress is best-effort, and a slow terminal must never stall a
// transfer. Only one transfer runs at a time, not for throughput reasons but
// because backend progress events carry no request id and could not otherwise
// be attributed to a caller.
//
// Four locks with distinct jobs. A write lock makes the Telegram-send plus
// local-projection sequence single-writer while reads stay concurrent; a second
// guards only the persisted CLI state; a third enforces the single transfer;
// and a mount lifecycle gate serializes mount start and stop against vault
// unlock, lock and logout. That gate encodes the security-relevant ordering:
// locking the vault or logging out must eject the mount first and clear the key
// only after, and if ejecting fails the key is deliberately retained rather
// than pulled out from under a live mount. Logout is terminal for the process —
// the mount lifecycle is marked permanently unusable before caches are cleared,
// so a queued start fails at the gate instead of part-way through.
//
// Only the socket path, listen, dial and cleanup are per-OS. Unix uses a 0600
// socket inside a uid-checked 0700 directory, kept short because socket paths
// have tight platform length limits. Windows uses a named pipe whose protected
// ACL grants the current user's SID alone; the SID and descriptor string
// handling is deliberately left untagged so that security boundary stays
// testable on every development OS.
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
	// authMu serializes status checks with login startup so one auth key is
	// never probed by multiple temporary clients in this daemon process.
	authMu         sync.Mutex
	authReady      bool
	authFlowActive bool
	drivePrepared  bool

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
