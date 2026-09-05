package nativeplayer

import (
	"errors"
	"io"
	"sync"
	"testing"
	"time"
)

type manualCloseTimer struct {
	callback func()
	fired    bool
}

func (t *manualCloseTimer) Stop() bool { return !t.fired }

func (t *manualCloseTimer) Fire() {
	t.fired = true
	t.callback()
}

type timedWriteCloser struct {
	mu         sync.Mutex
	closeCount int
	write      func([]byte) (int, error)
}

func (w *timedWriteCloser) Write(payload []byte) (int, error) { return w.write(payload) }

func (w *timedWriteCloser) Close() error {
	w.mu.Lock()
	w.closeCount++
	w.mu.Unlock()
	return nil
}

func (w *timedWriteCloser) closes() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.closeCount
}

func TestWriteAndCloseWithTimerReturnsTimeoutWithoutDoubleClose(t *testing.T) {
	var timer *manualCloseTimer
	writer := &timedWriteCloser{}
	writer.write = func([]byte) (int, error) {
		timer.Fire()
		return 0, errors.New("write on closed pipe")
	}
	schedule := func(_ time.Duration, callback func()) stoppableTimer {
		timer = &manualCloseTimer{callback: callback}
		return timer
	}

	err := writeAndCloseWithTimer(writer, []byte("command"), time.Second, schedule)
	if !errors.Is(err, errIPCWriteTimeout) {
		t.Fatalf("writeAndCloseWithTimer error = %v, want errIPCWriteTimeout", err)
	}
	if got := writer.closes(); got != 1 {
		t.Fatalf("Close calls = %d, want 1", got)
	}
}

func TestWriteAndCloseWithTimerKeepsSlowSuccessfulWrite(t *testing.T) {
	var timer *manualCloseTimer
	writer := &timedWriteCloser{}
	writer.write = func(payload []byte) (int, error) {
		timer.Fire()
		return len(payload), nil
	}
	schedule := func(_ time.Duration, callback func()) stoppableTimer {
		timer = &manualCloseTimer{callback: callback}
		return timer
	}

	if err := writeAndCloseWithTimer(writer, []byte("command"), time.Second, schedule); err != nil {
		t.Fatalf("completed write reported as failure: %v", err)
	}
	if got := writer.closes(); got != 1 {
		t.Fatalf("Close calls = %d, want 1", got)
	}
}

func TestWriteAndCloseWithTimerHandlesSuccessAndShortWrites(t *testing.T) {
	neverFires := func(_ time.Duration, callback func()) stoppableTimer {
		return &manualCloseTimer{callback: callback}
	}

	success := &timedWriteCloser{write: func(payload []byte) (int, error) { return len(payload), nil }}
	if err := writeAndCloseWithTimer(success, []byte("ok"), time.Second, neverFires); err != nil {
		t.Fatalf("successful write: %v", err)
	}
	if got := success.closes(); got != 1 {
		t.Fatalf("successful Close calls = %d, want 1", got)
	}

	short := &timedWriteCloser{write: func([]byte) (int, error) { return 1, nil }}
	if err := writeAndCloseWithTimer(short, []byte("short"), time.Second, neverFires); !errors.Is(err, io.ErrShortWrite) {
		t.Fatalf("short write error = %v, want io.ErrShortWrite", err)
	}
	if got := short.closes(); got != 1 {
		t.Fatalf("short-write Close calls = %d, want 1", got)
	}
}
