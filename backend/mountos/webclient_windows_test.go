//go:build windows

package mountos

import (
	"context"
	"errors"
	"sync"
	"testing"

	"golang.org/x/sys/windows"
)

// fakeWindowsServiceHandle drives ensureWindowsWebClientService through a
// scripted sequence of SERVICE_STATUS.CurrentState values, so the real
// Service Control Manager state machine can be exercised without a live
// Windows service.
type fakeWindowsServiceHandle struct {
	mu         sync.Mutex
	states     []uint32
	stateErr   error
	startErr   error
	startCalls int
	closeCalls int
}

func (h *fakeWindowsServiceHandle) State() (uint32, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.stateErr != nil {
		return 0, h.stateErr
	}
	if len(h.states) == 0 {
		return 0, errors.New("fakeWindowsServiceHandle: no more scripted states")
	}
	state := h.states[0]
	if len(h.states) > 1 {
		h.states = h.states[1:]
	}
	return state, nil
}

func (h *fakeWindowsServiceHandle) Start() error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.startCalls++
	return h.startErr
}

func (h *fakeWindowsServiceHandle) Close() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.closeCalls++
}

type fakeWindowsServiceOpener struct {
	handle   *fakeWindowsServiceHandle
	canStart bool
	openErr  error
}

func (o fakeWindowsServiceOpener) Open(name string) (windowsServiceHandle, bool, error) {
	if name != windowsWebClientServiceName {
		return nil, false, errors.New("unexpected service name")
	}
	if o.openErr != nil {
		return nil, false, o.openErr
	}
	return o.handle, o.canStart, nil
}

func noWindowsServiceWait(context.Context) error { return nil }

func TestEnsureWindowsWebClientServiceRejectsNilContext(t *testing.T) {
	if err := ensureWindowsWebClientService(nil, fakeWindowsServiceOpener{}, noWindowsServiceWait); !errors.Is(err, ErrInvalidContext) {
		t.Fatalf("error = %v, want ErrInvalidContext", err)
	}
}

func TestEnsureWindowsWebClientServiceAlreadyRunningSkipsStart(t *testing.T) {
	handle := &fakeWindowsServiceHandle{states: []uint32{windows.SERVICE_RUNNING}}
	opener := fakeWindowsServiceOpener{handle: handle, canStart: true}

	if err := ensureWindowsWebClientService(context.Background(), opener, noWindowsServiceWait); err != nil {
		t.Fatalf("error = %v, want nil", err)
	}
	if handle.startCalls != 0 {
		t.Fatalf("startCalls = %d, want 0: an already-running service must never be started", handle.startCalls)
	}
	if handle.closeCalls != 1 {
		t.Fatalf("closeCalls = %d, want 1", handle.closeCalls)
	}
}

func TestEnsureWindowsWebClientServiceStartsStoppedServiceThenObservesRunning(t *testing.T) {
	handle := &fakeWindowsServiceHandle{states: []uint32{
		windows.SERVICE_STOPPED,
		windows.SERVICE_START_PENDING,
		windows.SERVICE_RUNNING,
	}}
	opener := fakeWindowsServiceOpener{handle: handle, canStart: true}

	if err := ensureWindowsWebClientService(context.Background(), opener, noWindowsServiceWait); err != nil {
		t.Fatalf("error = %v, want nil", err)
	}
	if handle.startCalls != 1 {
		t.Fatalf("startCalls = %d, want exactly 1", handle.startCalls)
	}
}

func TestEnsureWindowsWebClientServiceStoppedWithoutStartRightsIsTolerated(t *testing.T) {
	handle := &fakeWindowsServiceHandle{states: []uint32{windows.SERVICE_STOPPED}}
	opener := fakeWindowsServiceOpener{handle: handle, canStart: false}

	if err := ensureWindowsWebClientService(context.Background(), opener, noWindowsServiceWait); err != nil {
		t.Fatalf("error = %v, want nil: the network provider may still trigger-start the service", err)
	}
	if handle.startCalls != 0 {
		t.Fatalf("startCalls = %d, want 0: SERVICE_START was never granted", handle.startCalls)
	}
}

func TestEnsureWindowsWebClientServiceStoppedAgainAfterStartRequestFails(t *testing.T) {
	handle := &fakeWindowsServiceHandle{states: []uint32{
		windows.SERVICE_STOPPED,
		windows.SERVICE_STOPPED,
	}}
	opener := fakeWindowsServiceOpener{handle: handle, canStart: true}

	err := ensureWindowsWebClientService(context.Background(), opener, noWindowsServiceWait)
	if err == nil || err.Error() != "WebClient stopped after start request" {
		t.Fatalf("error = %v, want the stopped-after-start-request failure", err)
	}
	if handle.startCalls != 1 {
		t.Fatalf("startCalls = %d, want exactly 1: must not retry StartService in a loop", handle.startCalls)
	}
}

func TestEnsureWindowsWebClientServiceStartAccessDeniedIsTolerated(t *testing.T) {
	handle := &fakeWindowsServiceHandle{
		states:   []uint32{windows.SERVICE_STOPPED},
		startErr: windows.ERROR_ACCESS_DENIED,
	}
	opener := fakeWindowsServiceOpener{handle: handle, canStart: true}

	if err := ensureWindowsWebClientService(context.Background(), opener, noWindowsServiceWait); err != nil {
		t.Fatalf("error = %v, want nil: an access-denied StartService call should not fail the attach", err)
	}
}

func TestEnsureWindowsWebClientServiceStartAlreadyRunningIsNotFatal(t *testing.T) {
	handle := &fakeWindowsServiceHandle{
		states:   []uint32{windows.SERVICE_STOPPED, windows.SERVICE_RUNNING},
		startErr: windows.ERROR_SERVICE_ALREADY_RUNNING,
	}
	opener := fakeWindowsServiceOpener{handle: handle, canStart: true}

	if err := ensureWindowsWebClientService(context.Background(), opener, noWindowsServiceWait); err != nil {
		t.Fatalf("error = %v, want nil", err)
	}
}

func TestEnsureWindowsWebClientServiceStartOtherErrorFails(t *testing.T) {
	failure := errors.New("start failed for an unrelated reason")
	handle := &fakeWindowsServiceHandle{
		states:   []uint32{windows.SERVICE_STOPPED},
		startErr: failure,
	}
	opener := fakeWindowsServiceOpener{handle: handle, canStart: true}

	err := ensureWindowsWebClientService(context.Background(), opener, noWindowsServiceWait)
	if !errors.Is(err, failure) {
		t.Fatalf("error = %v, want it to wrap %v", err, failure)
	}
}

func TestEnsureWindowsWebClientServiceUnknownStateFails(t *testing.T) {
	handle := &fakeWindowsServiceHandle{states: []uint32{windows.SERVICE_PAUSED}}
	opener := fakeWindowsServiceOpener{handle: handle, canStart: true}

	err := ensureWindowsWebClientService(context.Background(), opener, noWindowsServiceWait)
	if err == nil {
		t.Fatal("error = nil, want the unavailable-state failure")
	}
}

func TestEnsureWindowsWebClientServiceQueryErrorFails(t *testing.T) {
	failure := errors.New("query failed")
	handle := &fakeWindowsServiceHandle{stateErr: failure}
	opener := fakeWindowsServiceOpener{handle: handle, canStart: true}

	err := ensureWindowsWebClientService(context.Background(), opener, noWindowsServiceWait)
	if !errors.Is(err, failure) {
		t.Fatalf("error = %v, want it to wrap %v", err, failure)
	}
}

func TestEnsureWindowsWebClientServiceOpenErrorPropagatesWithoutClose(t *testing.T) {
	failure := errors.New("service does not exist")
	opener := fakeWindowsServiceOpener{openErr: failure}

	err := ensureWindowsWebClientService(context.Background(), opener, noWindowsServiceWait)
	if !errors.Is(err, failure) {
		t.Fatalf("error = %v, want it to wrap %v", err, failure)
	}
}

func TestEnsureWindowsWebClientServiceStuckPendingHonorsWaitTimeout(t *testing.T) {
	handle := &fakeWindowsServiceHandle{states: []uint32{windows.SERVICE_START_PENDING}}
	opener := fakeWindowsServiceOpener{handle: handle, canStart: true}
	waitErr := errors.New("bounded poll gave up")
	stuck := func(context.Context) error { return waitErr }

	err := ensureWindowsWebClientService(context.Background(), opener, stuck)
	if !errors.Is(err, waitErr) {
		t.Fatalf("error = %v, want it to wrap %v", err, waitErr)
	}
}

func TestEnsureWindowsWebClientRealOpenerRejectsUnknownServiceName(t *testing.T) {
	_, _, err := scmServiceOpener{}.Open("a service name that does not exist on any Windows install")
	if err == nil {
		t.Fatal("error = nil, want a failure opening a nonexistent service")
	}
}
