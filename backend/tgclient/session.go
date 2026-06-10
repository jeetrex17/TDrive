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

	mu     sync.Mutex
	closed bool
	scope  *connScope
}

type connScope struct {
	cancel  context.CancelFunc
	readyCh chan struct{}
	doneCh  chan struct{}
	ready   bool
	done    bool
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
	for {
		l.mu.Lock()
		if l.closed {
			l.mu.Unlock()
			return errScopeClosed
		}
		if l.scope == nil || l.scope.done {
			l.start()
		}
		scope := l.scope
		readyCh, doneCh := scope.readyCh, scope.doneCh
		l.mu.Unlock()

		select {
		case <-readyCh:
			l.mu.Lock()
			if l.scope != scope {
				l.mu.Unlock()
				continue
			}
			if scope.done {
				wasReady, err := scope.ready, scope.err
				l.scope = nil
				l.mu.Unlock()
				if wasReady {
					continue
				}
				if err == nil {
					err = errScopeClosed
				}
				return err
			}
			l.mu.Unlock()
			return nil
		case <-doneCh:
			l.mu.Lock()
			if l.scope != scope {
				l.mu.Unlock()
				continue
			}
			wasReady, err := scope.ready, scope.err
			l.scope = nil
			l.mu.Unlock()
			if wasReady {
				continue
			}
			if err == nil {
				err = errScopeClosed
			}
			return err
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// start launches the scope goroutine. Caller must hold l.mu.
func (l *liveConn) start() {
	runCtx, cancel := context.WithCancel(context.Background())
	scope := &connScope{
		cancel:  cancel,
		readyCh: make(chan struct{}),
		doneCh:  make(chan struct{}),
	}
	l.scope = scope

	var once sync.Once
	ready := func() {
		once.Do(func() {
			l.mu.Lock()
			scope.ready = true
			l.mu.Unlock()
			close(scope.readyCh)
		})
	}

	go func() {
		err := l.scopeFn(runCtx, ready)
		l.mu.Lock()
		scope.err = err
		scope.done = true
		l.mu.Unlock()
		cancel()
		close(scope.doneCh)
	}()
}

// Close tears down the running scope (if any) and blocks until it exits.
// Idempotent; after Close, acquire returns errScopeClosed.
func (l *liveConn) Close() {
	l.mu.Lock()
	l.closed = true
	scope := l.scope
	l.scope = nil
	l.mu.Unlock()

	if scope != nil {
		scope.cancel()
		<-scope.doneCh
	}
}
