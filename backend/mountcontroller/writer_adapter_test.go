package mountcontroller

import (
	"context"
	"errors"
	"io"
	"testing"

	"TDrive/backend/mountdav"
	"TDrive/backend/mountwrite"
)

func TestAdapterSessionMapsStatusAndDelegatesLifecycle(t *testing.T) {
	t.Parallel()

	drainErr := errors.New("drain failed")
	canonical := &fakeCanonicalWriteSession{
		status:   mountwrite.Status{Accepting: true, Active: 3, Executing: 2, Queued: 1},
		drainErr: drainErr,
	}
	session, err := newAdapterSession(canonical)
	if err != nil {
		t.Fatalf("newAdapterSession() error = %v", err)
	}
	if got := session.WriteStatus(); got != (WriteStatus{Accepting: true, Active: 3}) {
		t.Fatalf("WriteStatus() = %#v", got)
	}
	if err := session.Drain(context.Background()); !errors.Is(err, drainErr) {
		t.Fatalf("Drain() error = %v", err)
	}
	if err := session.Close(context.Background()); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if canonical.drainCalls != 1 || canonical.closeCalls != 1 {
		t.Fatalf("lifecycle calls = drain:%d close:%d", canonical.drainCalls, canonical.closeCalls)
	}
	if _, ok := any(session).(mountdav.WriteCoordinator); !ok {
		t.Fatal("adapter session does not preserve the complete WebDAV writer")
	}
}

func TestAdapterSessionRejectsMissingCanonicalSession(t *testing.T) {
	t.Parallel()

	if _, err := newAdapterSession(nil); !errors.Is(err, ErrInvalidConfiguration) {
		t.Fatalf("newAdapterSession(nil) error = %v", err)
	}
}

type fakeCanonicalWriteSession struct {
	status     mountwrite.Status
	drainErr   error
	drainCalls int
	closeCalls int
}

func (*fakeCanonicalWriteSession) Put(context.Context, mountdav.PutRequest, io.Reader) (mountdav.MutationResult, error) {
	return mountdav.MutationResult{}, nil
}

func (*fakeCanonicalWriteSession) Mkdir(context.Context, mountdav.MkdirRequest) (mountdav.MutationResult, error) {
	return mountdav.MutationResult{}, nil
}

func (*fakeCanonicalWriteSession) Move(context.Context, mountdav.MoveRequest) (mountdav.MutationResult, error) {
	return mountdav.MutationResult{}, nil
}

func (*fakeCanonicalWriteSession) Delete(context.Context, mountdav.DeleteRequest) (mountdav.MutationResult, error) {
	return mountdav.MutationResult{}, nil
}

func (session *fakeCanonicalWriteSession) Status() mountwrite.Status { return session.status }

func (session *fakeCanonicalWriteSession) Drain(context.Context) error {
	session.drainCalls++
	return session.drainErr
}

func (session *fakeCanonicalWriteSession) Close(context.Context) error {
	session.closeCalls++
	return nil
}

var _ canonicalWriteSession = (*fakeCanonicalWriteSession)(nil)
