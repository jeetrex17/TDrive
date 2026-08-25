package mountcontroller

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"TDrive/backend/mountdav"
)

func TestRootedWriteSessionRoutesPersonalPaths(t *testing.T) {
	t.Parallel()

	delegate := &recordingAggregateWriter{}
	writer, err := newRootedWriteSession("Personal", delegate)
	if err != nil {
		t.Fatalf("newRootedWriteSession() error = %v", err)
	}

	result, err := writer.Put(context.Background(), mountdav.PutRequest{
		OperationID:   "operation",
		Path:          "/personal/Notes/todo.txt",
		ContentLength: 4,
		Conditions: mountdav.MutationConditions{DAVIf: []mountdav.DAVConditionList{
			{ResourcePath: "/personal/Notes/todo.txt"},
		}},
	}, strings.NewReader("todo"))
	if err != nil {
		t.Fatalf("Put() error = %v", err)
	}
	if !result.Created || delegate.put.Path != "/Notes/todo.txt" {
		t.Fatalf("Put() = (%#v, %#v)", result, delegate.put)
	}
	if got := delegate.put.Conditions.DAVIf[0].ResourcePath; got != "/Notes/todo.txt" {
		t.Fatalf("DAV If resource = %q", got)
	}
}

func TestRootedWriteSessionRejectsSharedRootAndCrossDriveMove(t *testing.T) {
	t.Parallel()

	delegate := &recordingAggregateWriter{}
	writer, err := newRootedWriteSession("Personal", delegate)
	if err != nil {
		t.Fatal(err)
	}

	_, err = writer.Put(context.Background(), mountdav.PutRequest{
		OperationID:   "operation",
		Path:          "/Shared — Team/new.txt",
		ContentLength: 1,
	}, strings.NewReader("x"))
	if !errors.Is(err, mountdav.ErrWriteForbidden) {
		t.Fatalf("shared Put() error = %v, want ErrWriteForbidden", err)
	}
	_, err = writer.Move(context.Background(), mountdav.MoveRequest{
		OperationID:     "operation",
		SourcePath:      "/Personal/file.txt",
		DestinationPath: "/Shared — Team/file.txt",
	})
	if !errors.Is(err, mountdav.ErrWriteForbidden) {
		t.Fatalf("cross-drive Move() error = %v, want ErrWriteForbidden", err)
	}
	if delegate.putCalls != 0 || delegate.moveCalls != 0 {
		t.Fatalf("forbidden operation reached delegate: put=%d move=%d", delegate.putCalls, delegate.moveCalls)
	}
}

func TestRootedWriteSessionRejectsVirtualRootMutation(t *testing.T) {
	t.Parallel()

	delegate := &recordingAggregateWriter{}
	writer, err := newRootedWriteSession("Personal", delegate)
	if err != nil {
		t.Fatal(err)
	}

	for _, path := range []string{"/", "/Personal"} {
		_, err := writer.Delete(context.Background(), mountdav.DeleteRequest{OperationID: "operation", Path: path})
		if !errors.Is(err, mountdav.ErrWriteForbidden) {
			t.Fatalf("Delete(%q) error = %v, want ErrWriteForbidden", path, err)
		}
	}
	if delegate.deleteCalls != 0 {
		t.Fatalf("virtual-root delete reached delegate %d times", delegate.deleteCalls)
	}
}

type recordingAggregateWriter struct {
	fakeWriterSession
	put         mountdav.PutRequest
	move        mountdav.MoveRequest
	putCalls    int
	moveCalls   int
	deleteCalls int
}

func (writer *recordingAggregateWriter) Put(_ context.Context, request mountdav.PutRequest, _ io.Reader) (mountdav.MutationResult, error) {
	writer.put = request
	writer.putCalls++
	return mountdav.MutationResult{Created: true}, nil
}

func (*recordingAggregateWriter) Mkdir(context.Context, mountdav.MkdirRequest) (mountdav.MutationResult, error) {
	return mountdav.MutationResult{Created: true}, nil
}

func (writer *recordingAggregateWriter) Move(_ context.Context, request mountdav.MoveRequest) (mountdav.MutationResult, error) {
	writer.move = request
	writer.moveCalls++
	return mountdav.MutationResult{}, nil
}

func (writer *recordingAggregateWriter) Delete(_ context.Context, _ mountdav.DeleteRequest) (mountdav.MutationResult, error) {
	writer.deleteCalls++
	return mountdav.MutationResult{}, nil
}
