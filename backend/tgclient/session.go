package tgclient

import (
	"context"
	"errors"
	"sync"
)

var errScopeClosed = errors.New("tgclient: connection scope closed")

// liveConn manages a single long-lived connection "scope" shared by every
// caller, replacing the previous dial-per-call style.
//
// scopeFn must: establish the connection, call ready() exactly once when the
// connection is usable, then block until its ctx is cancelled (returning that
// ctx's error). In production scopeFn wraps gotd's client.Run; isolating it
// here keeps this lifecycle unit-testable without a real Telegram connection.
//
// The scope starts lazily on the first acquire and is torn down by Close. A
// scope that exits on its own (dial error, dropped connection) is restarted on
// the next acquire, so a transient failure doesn't permanently wedge the app.
type liveConn struct {
	scopeFn func(ctx context.Context, ready func()) error

	mu      sync.Mutex
	closed  bool
	cancel  context.CancelFunc
	readyCh chan struct{}
	doneCh  chan struct{}
	err     error
}

func newLiveConn(scopeFn func(ctx context.Context, ready func()) error) *liveConn {
	return &liveConn{scopeFn: scopeFn}
}

// acquire ensures the scope is running and blocks until it is ready. Returns
// nil once ready; the caller may then use the connection until its own ctx
// ends. Returns the caller's ctx error if it is cancelled first, or the
// scope's error if the scope failed before becoming ready.
func (l *liveConn) acquire(ctx context.Context) error {
	l.mu.Lock()
	if l.closed {
		l.mu.Unlock()
		return errScopeClosed
	}
	if l.readyCh == nil {
		l.start()
	}
	readyCh, doneCh := l.readyCh, l.doneCh
	l.mu.Unlock()

	select {
	case <-readyCh:
		return nil
	case <-doneCh:
		l.mu.Lock()
		err := l.err
		// Drop the dead scope so the next acquire starts a fresh one.
		if l.readyCh == readyCh {
			l.readyCh, l.doneCh = nil, nil
		}
		l.mu.Unlock()
		if err == nil {
			err = errScopeClosed
		}
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

// start launches the scope goroutine. Caller must hold l.mu.
func (l *liveConn) start() {
	runCtx, cancel := context.WithCancel(context.Background())
	readyCh := make(chan struct{})
	doneCh := make(chan struct{})
	l.cancel = cancel
	l.readyCh = readyCh
	l.doneCh = doneCh
	l.err = nil

	var once sync.Once
	ready := func() { once.Do(func() { close(readyCh) }) }

	go func() {
		err := l.scopeFn(runCtx, ready)
		l.mu.Lock()
		l.err = err
		l.mu.Unlock()
		cancel()
		close(doneCh)
	}()
}

// Close tears down the running scope (if any) and blocks until it exits.
// Idempotent; after Close, acquire returns errScopeClosed.
func (l *liveConn) Close() {
	l.mu.Lock()
	l.closed = true
	cancel := l.cancel
	doneCh := l.doneCh
	l.cancel, l.readyCh, l.doneCh = nil, nil, nil
	l.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	if doneCh != nil {
		<-doneCh
	}
}
