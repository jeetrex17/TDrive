package mountadapter

import (
	"bytes"
	"context"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"
	"time"

	"TDrive/backend/mountdav"
	"TDrive/backend/mountfs"
	"TDrive/backend/mountpath"
	"TDrive/backend/mountwrite"
)

const testDriveID int64 = 77

func TestSessionPutMapsPortablePathAndRevision(t *testing.T) {
	resolver := newFakeResolver(
		Node{ObjectID: "d:docs", ParentID: "", Name: "Docs", Kind: mountfs.KindDirectory, Revision: 4},
		Node{ObjectID: "f:9", ParentID: "d:docs", Name: "old.txt", Kind: mountfs.KindFile, Revision: 6, ContentHash: "old"},
	)
	engine := &fakeEngine{putResult: mountwrite.MutationResult{ObjectID: "f:9", Revision: 7}}
	session := newTestSession(resolver, engine)

	result, err := session.Put(context.Background(), mountdav.PutRequest{
		OperationID:   "op-put",
		Path:          "/Docs/old.txt",
		ContentLength: 3,
		Conditions: mountdav.MutationConditions{IfMatch: mountdav.ETagConditions{
			Present: true,
			Tags: []mountdav.EntityTag{{
				Opaque: opaqueETag(mustETag(t, "f:9", 6, "old")),
			}},
		}},
	}, bytes.NewBufferString("new"))
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	if result.Created || result.ETag == "" {
		t.Fatalf("result = %+v, want replaced object with ETag", result)
	}
	want := mountwrite.PutRequest{
		OperationID:      "op-put",
		DriveID:          testDriveID,
		ParentID:         "d:docs",
		Name:             "old.txt",
		ExistingObjectID: "f:9",
		ExpectedRevision: 6,
		ContentLength:    3,
		MaxBytes:         defaultMaxObjectBytes,
	}
	if !reflect.DeepEqual(engine.putRequest, want) {
		t.Fatalf("Put request = %+v, want %+v", engine.putRequest, want)
	}
}

func TestSessionPutCreateAndPreconditions(t *testing.T) {
	resolver := newFakeResolver(Node{ObjectID: "d:docs", Name: "Docs", Kind: mountfs.KindDirectory, Revision: 1})
	engine := &fakeEngine{putResult: mountwrite.MutationResult{ObjectID: "f:10", Revision: 1, Created: true}}
	session := newTestSession(resolver, engine)

	result, err := session.Put(context.Background(), mountdav.PutRequest{
		Path:          "/Docs/new.txt",
		ContentLength: 0,
		Conditions: mountdav.MutationConditions{
			IfNoneMatch: mountdav.ETagConditions{Present: true, Any: true},
		},
	}, bytes.NewReader(nil))
	if err != nil {
		t.Fatalf("Put create: %v", err)
	}
	if !result.Created || !engine.putRequest.CreateOnly {
		t.Fatalf("result/request = %+v / %+v, want create", result, engine.putRequest)
	}

	_, err = session.Put(context.Background(), mountdav.PutRequest{
		Path:          "/Docs/new.txt",
		ContentLength: 0,
		Conditions: mountdav.MutationConditions{DAVIf: []mountdav.DAVConditionList{{
			ResourcePath: "/secret/other",
			Conditions:   []mountdav.DAVCondition{{ETag: &mountdav.EntityTag{Opaque: "x"}}},
		}}},
	}, bytes.NewReader(nil))
	if !errors.Is(err, mountdav.ErrWritePreconditionFailed) {
		t.Fatalf("unrelated tagged condition error = %v", err)
	}
}

func TestSessionRejectsInvalidWritePathsBeforeEngine(t *testing.T) {
	resolver := newFakeResolver(Node{ObjectID: "d:docs", Name: "Docs", Kind: mountfs.KindDirectory, Revision: 1})
	engine := &fakeEngine{}
	session := newTestSession(resolver, engine)

	tests := []struct {
		name string
		call func() error
	}{
		{"root put", func() error {
			_, err := session.Put(context.Background(), mountdav.PutRequest{Path: "/", ContentLength: 0}, bytes.NewReader(nil))
			return err
		}},
		{"reserved name", func() error {
			_, err := session.Mkdir(context.Background(), mountdav.MkdirRequest{Path: "/Docs/CON"})
			return err
		}},
		{"nil body", func() error {
			_, err := session.Put(context.Background(), mountdav.PutRequest{Path: "/Docs/x", ContentLength: 0}, nil)
			return err
		}},
		{"relative", func() error {
			_, err := session.Delete(context.Background(), mountdav.DeleteRequest{Path: "Docs"})
			return err
		}},
		{"component too long", func() error {
			_, err := session.Delete(context.Background(), mountdav.DeleteRequest{
				Path: "/" + strings.Repeat("a", mountpath.MaxComponentBytes+1),
			})
			return err
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := test.call(); !errors.Is(err, mountdav.ErrWriteInvalid) && !errors.Is(err, mountdav.ErrWriteForbidden) {
				t.Fatalf("error = %v, want invalid/forbidden", err)
			}
		})
	}
	if engine.callCount != 0 {
		t.Fatalf("engine called %d times for rejected requests", engine.callCount)
	}
}

func TestSessionMoveUsesExactSourceAndOverwriteCAS(t *testing.T) {
	resolver := newFakeResolver(
		Node{ObjectID: "d:a", Name: "A", Kind: mountfs.KindDirectory, Revision: 1},
		Node{ObjectID: "d:b", Name: "B", Kind: mountfs.KindDirectory, Revision: 1},
		Node{ObjectID: "f:1", ParentID: "d:a", Name: "source.txt", Kind: mountfs.KindFile, Revision: 5},
		Node{ObjectID: "f:2", ParentID: "d:b", Name: "target.txt", Kind: mountfs.KindFile, Revision: 8},
	)
	engine := &fakeEngine{moveResult: mountwrite.MutationResult{ObjectID: "f:1", Revision: 6}}
	session := newTestSession(resolver, engine)

	result, err := session.Move(context.Background(), mountdav.MoveRequest{
		OperationID:     "op-move",
		SourcePath:      "/A/source.txt",
		DestinationPath: "/B/target.txt",
		Overwrite:       true,
	})
	if err != nil {
		t.Fatalf("Move: %v", err)
	}
	if result.Created {
		t.Fatalf("Move result = %+v, overwrite must be 204", result)
	}
	want := mountwrite.MoveRequest{
		OperationID:            "op-move",
		DriveID:                testDriveID,
		ObjectID:               "f:1",
		SourceParentID:         "d:a",
		DestinationParentID:    "d:b",
		DestinationName:        "target.txt",
		ExpectedSourceRevision: 5,
		OverwriteTargetID:      "f:2",
		ExpectedTargetRevision: 8,
	}
	if engine.moveRequest != want {
		t.Fatalf("Move request = %+v, want %+v", engine.moveRequest, want)
	}
}

func TestSessionMoveResponseETagPreservesSourceContentHash(t *testing.T) {
	resolver := newFakeResolver(
		Node{ObjectID: "d:a", Name: "A", Kind: mountfs.KindDirectory, Revision: 1},
		Node{ObjectID: "d:b", Name: "B", Kind: mountfs.KindDirectory, Revision: 1},
		Node{ObjectID: "f:1", ParentID: "d:a", Name: "source.txt", Kind: mountfs.KindFile, Revision: 5, ContentHash: "sha256:source"},
	)
	engine := &fakeEngine{moveResult: mountwrite.MutationResult{ObjectID: "f:1", Revision: 6}}
	session := newTestSession(resolver, engine)
	result, err := session.Move(context.Background(), mountdav.MoveRequest{
		SourcePath: "/A/source.txt", DestinationPath: "/B/moved.txt",
	})
	if err != nil {
		t.Fatalf("Move: %v", err)
	}
	want := mustETag(t, "f:1", 6, "sha256:source")
	if result.ETag != want {
		t.Fatalf("MOVE ETag = %q, want read-round-trip %q", result.ETag, want)
	}
}

func TestSessionRejectsMutationsOfLegacyEncryptedEntries(t *testing.T) {
	tests := []struct {
		name     string
		resolver *fakeResolver
		call     func(*Session) error
	}{
		{
			name:     "replace",
			resolver: newFakeResolver(Node{ObjectID: "f:1", Name: "secret.txt", Kind: mountfs.KindFile, Revision: 1, Encrypted: true}),
			call: func(session *Session) error {
				_, err := session.Put(context.Background(), mountdav.PutRequest{Path: "/secret.txt", ContentLength: 0}, bytes.NewReader(nil))
				return err
			},
		},
		{
			name: "move source",
			resolver: newFakeResolver(
				Node{ObjectID: "d:dst", Name: "Dst", Kind: mountfs.KindDirectory, Revision: 1},
				Node{ObjectID: "f:1", Name: "secret.txt", Kind: mountfs.KindFile, Revision: 1, Encrypted: true},
			),
			call: func(session *Session) error {
				_, err := session.Move(context.Background(), mountdav.MoveRequest{SourcePath: "/secret.txt", DestinationPath: "/Dst/secret.txt"})
				return err
			},
		},
		{
			name: "overwrite destination",
			resolver: newFakeResolver(
				Node{ObjectID: "d:dst", Name: "Dst", Kind: mountfs.KindDirectory, Revision: 1},
				Node{ObjectID: "f:1", Name: "plain.txt", Kind: mountfs.KindFile, Revision: 1},
				Node{ObjectID: "f:2", ParentID: "d:dst", Name: "secret.txt", Kind: mountfs.KindFile, Revision: 1, Encrypted: true},
			),
			call: func(session *Session) error {
				_, err := session.Move(context.Background(), mountdav.MoveRequest{SourcePath: "/plain.txt", DestinationPath: "/Dst/secret.txt", Overwrite: true})
				return err
			},
		},
		{
			name:     "delete",
			resolver: newFakeResolver(Node{ObjectID: "f:1", Name: "secret.txt", Kind: mountfs.KindFile, Revision: 1, Encrypted: true}),
			call: func(session *Session) error {
				_, err := session.Delete(context.Background(), mountdav.DeleteRequest{Path: "/secret.txt"})
				return err
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			engine := &fakeEngine{}
			session := newTestSession(test.resolver, engine)
			if err := test.call(session); !errors.Is(err, mountdav.ErrWriteForbidden) {
				t.Fatalf("error = %v, want forbidden", err)
			}
			if engine.callCount != 0 {
				t.Fatalf("engine called %d times", engine.callCount)
			}
		})
	}
}

func TestUnlockedEncryptedSessionEncryptsPutsAndAllowsEncryptedMetadataMutations(t *testing.T) {
	masterKey := bytes.Repeat([]byte{9}, 32)

	t.Run("replace", func(t *testing.T) {
		resolver := newFakeResolver(Node{
			ObjectID: "f:1", Name: "secret.txt", Kind: mountfs.KindFile,
			Revision: 4, Encrypted: true,
		})
		engine := &fakeEngine{putResult: mountwrite.MutationResult{ObjectID: "f:1", Revision: 5}}
		session := newTestSession(resolver, engine)
		session.encryptWrites = true
		session.masterKeys = staticMountKeyProvider(masterKey)

		_, err := session.Put(
			context.Background(),
			mountdav.PutRequest{Path: "/secret.txt", ContentLength: 5},
			bytes.NewReader([]byte("plain")),
		)
		if err != nil {
			t.Fatalf("encrypted Put() error = %v", err)
		}
		if engine.putRequest.EncryptionVersion != mountwrite.EncryptionTDE1 || engine.putRequest.ContentLength != 5 {
			t.Fatalf("encrypted PutRequest = %#v", engine.putRequest)
		}
		if !bytes.Equal(engine.putRequest.MasterKey, masterKey) || &engine.putRequest.MasterKey[0] == &masterKey[0] {
			t.Fatal("encrypted PutRequest does not own an independent mount-key copy")
		}
	})

	t.Run("move and delete", func(t *testing.T) {
		resolver := newFakeResolver(
			Node{ObjectID: "d:dst", Name: "Dst", Kind: mountfs.KindDirectory, Revision: 1},
			Node{ObjectID: "f:1", Name: "secret.txt", Kind: mountfs.KindFile, Revision: 4, Encrypted: true},
		)
		engine := &fakeEngine{
			moveResult:   mountwrite.MutationResult{ObjectID: "f:1", Revision: 5},
			deleteResult: mountwrite.MutationResult{ObjectID: "f:1", Revision: 6},
		}
		session := newTestSession(resolver, engine)
		session.encryptWrites = true
		session.masterKeys = staticMountKeyProvider(masterKey)

		if _, err := session.Move(context.Background(), mountdav.MoveRequest{
			SourcePath: "/secret.txt", DestinationPath: "/Dst/secret.txt",
		}); err != nil {
			t.Fatalf("encrypted Move() error = %v", err)
		}
		resolver.byPath["/secret.txt"] = Node{
			ObjectID: "f:1", Name: "secret.txt", Kind: mountfs.KindFile,
			Revision: 5, Encrypted: true,
		}
		if _, err := session.Delete(context.Background(), mountdav.DeleteRequest{Path: "/secret.txt"}); err != nil {
			t.Fatalf("encrypted Delete() error = %v", err)
		}
		if engine.callCount != 2 {
			t.Fatalf("metadata engine calls = %d, want 2", engine.callCount)
		}
	})

	t.Run("unknown content length", func(t *testing.T) {
		session := newTestSession(newFakeResolver(), &fakeEngine{})
		session.encryptWrites = true
		session.masterKeys = staticMountKeyProvider(masterKey)
		_, err := session.Put(context.Background(), mountdav.PutRequest{
			Path: "/unknown.bin", ContentLength: -1,
		}, bytes.NewReader([]byte("plain")))
		if !errors.Is(err, mountdav.ErrWriteLengthRequired) {
			t.Fatalf("encrypted unknown-length Put error = %v, want length required", err)
		}
	})
}

func TestSessionDeleteUsesThirtyDayTrashAndRecursiveFolders(t *testing.T) {
	resolver := newFakeResolver(
		Node{ObjectID: "d:docs", Name: "Docs", Kind: mountfs.KindDirectory, Revision: 9},
	)
	engine := &fakeEngine{deleteResult: mountwrite.MutationResult{ObjectID: "d:docs", Revision: 10}}
	session := newTestSession(resolver, engine)

	if _, err := session.Delete(context.Background(), mountdav.DeleteRequest{OperationID: "op-delete", Path: "/Docs"}); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if !engine.deleteRequest.Recursive || engine.deleteRequest.TrashRetention != defaultTrashRetention {
		t.Fatalf("Delete request = %+v", engine.deleteRequest)
	}
}

func TestSessionMapsCoordinatorErrorsAndLifecycle(t *testing.T) {
	resolver := newFakeResolver(Node{ObjectID: "d:docs", Name: "Docs", Kind: mountfs.KindDirectory, Revision: 1})
	engine := &fakeEngine{mkdirErr: mountwrite.ErrConflict}
	session := newTestSession(resolver, engine)

	_, err := session.Mkdir(context.Background(), mountdav.MkdirRequest{Path: "/Docs/New"})
	if !errors.Is(err, mountdav.ErrWriteConflict) {
		t.Fatalf("Mkdir error = %v, want conflict", err)
	}

	if status := session.Status(); status != (mountwrite.Status{Accepting: true, Active: 2}) {
		t.Fatalf("Status = %+v", status)
	}
	if err := session.Drain(context.Background()); err != nil {
		t.Fatalf("Drain: %v", err)
	}
	if err := session.Close(context.Background()); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if engine.drainCalls != 1 || engine.closeCalls != 1 {
		t.Fatalf("lifecycle calls = drain %d close %d", engine.drainCalls, engine.closeCalls)
	}
}

// TestSessionMkdirRetryOntoExistingDirectorySucceeds covers a client retrying
// an MKCOL whose response it lost. mountdav mints a fresh OperationID per HTTP
// request (writes.go randomOperationID), so the retry carries no signal
// linking it to the original, already-successful attempt, and the write
// coordinator's OperationID-keyed idempotency machinery never sees it as a
// retry (see mountwrite.Coordinator.createOrLoad). Session.Mkdir treats a
// directory already at the exact requested path as that retry succeeding
// again, without ever consulting the coordinator.
func TestSessionMkdirRetryOntoExistingDirectorySucceeds(t *testing.T) {
	resolver := newFakeResolver(
		Node{ObjectID: "d:docs", Name: "Docs", Kind: mountfs.KindDirectory, Revision: 1},
		Node{ObjectID: "d:new", Name: "New", ParentID: "d:docs", Kind: mountfs.KindDirectory, Revision: 1},
	)
	engine := &fakeEngine{}
	session := newTestSession(resolver, engine)

	_, err := session.Mkdir(context.Background(), mountdav.MkdirRequest{
		OperationID: "retry-of-earlier-mkcol",
		Path:        "/Docs/New",
	})
	if err != nil {
		t.Fatalf("Mkdir retry error = %v, want nil", err)
	}
	if engine.callCount != 0 {
		t.Fatalf("engine.Mkdir called %d times, want 0: the retry is resolved before the coordinator is ever consulted", engine.callCount)
	}
}

// TestSessionMkdirOntoExistingFileStillConflicts ensures the retry tolerance
// above stays narrow: a file already at the requested path is a genuine
// naming collision, not a retry, and must still be rejected.
func TestSessionMkdirOntoExistingFileStillConflicts(t *testing.T) {
	resolver := newFakeResolver(
		Node{ObjectID: "d:docs", Name: "Docs", Kind: mountfs.KindDirectory, Revision: 1},
		Node{ObjectID: "f:new", Name: "New", ParentID: "d:docs", Kind: mountfs.KindFile, Revision: 1},
	)
	engine := &fakeEngine{}
	session := newTestSession(resolver, engine)

	_, err := session.Mkdir(context.Background(), mountdav.MkdirRequest{
		OperationID: "genuine-collision",
		Path:        "/Docs/New",
	})
	if !errors.Is(err, mountdav.ErrWriteConflict) {
		t.Fatalf("Mkdir onto existing file error = %v, want conflict", err)
	}
	if engine.callCount != 0 {
		t.Fatalf("engine.Mkdir called %d times, want 0", engine.callCount)
	}
}

func TestWriteErrorMappingIsProtocolSafe(t *testing.T) {
	tests := []struct {
		name string
		in   error
		want error
	}{
		{name: "nil", in: nil, want: nil},
		{name: "invalid", in: mountwrite.ErrInvalidRequest, want: mountdav.ErrWriteInvalid},
		{name: "length mismatch", in: mountwrite.ErrLengthMismatch, want: mountdav.ErrWriteInvalid},
		{name: "forbidden", in: mountwrite.ErrForbidden, want: mountdav.ErrWriteForbidden},
		{name: "not found", in: mountwrite.ErrNotFound, want: mountdav.ErrWriteNotFound},
		{name: "conflict", in: mountwrite.ErrConflict, want: mountdav.ErrWriteConflict},
		{name: "operation exists", in: mountwrite.ErrOperationExists, want: mountdav.ErrWriteConflict},
		{name: "operation in progress", in: mountwrite.ErrOperationInProgress, want: mountdav.ErrWriteConflict},
		{name: "precondition", in: mountwrite.ErrPreconditionFailed, want: mountdav.ErrWritePreconditionFailed},
		{name: "too large", in: mountwrite.ErrTooLarge, want: mountdav.ErrWriteTooLarge},
		{name: "locked", in: mountwrite.ErrLocked, want: mountdav.ErrWriteLocked},
		{name: "quota", in: mountwrite.ErrQuotaExceeded, want: mountdav.ErrWriteInsufficientStorage},
		{name: "unavailable", in: mountwrite.ErrUnavailable, want: mountdav.ErrWriteUnavailable},
		{name: "busy", in: mountwrite.ErrBusy, want: mountdav.ErrWriteUnavailable},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := mapWriteError(test.in)
			if test.want == nil {
				if got != nil {
					t.Fatalf("mapWriteError(%v) = %v, want nil", test.in, got)
				}
				return
			}
			if !errors.Is(got, test.want) {
				t.Fatalf("mapWriteError(%v) = %v, want %v", test.in, got, test.want)
			}
		})
	}

	if got := mapResolveError(nil); got != nil {
		t.Fatalf("mapResolveError(nil) = %v", got)
	}
	for _, input := range []error{context.Canceled, context.DeadlineExceeded, errors.New("database details")} {
		if got := mapResolveError(input); !errors.Is(got, mountdav.ErrWriteUnavailable) {
			t.Fatalf("mapResolveError(%v) = %v", input, got)
		}
	}
}

func TestSessionLifecycleRejectsUnusableReceiversAndContexts(t *testing.T) {
	var session *Session
	if status := session.Status(); status != (mountwrite.Status{}) {
		t.Fatalf("nil Status = %+v", status)
	}
	if report := session.RecoveryReport(); report != (mountwrite.RecoveryReport{}) {
		t.Fatalf("nil RecoveryReport = %+v", report)
	}
	if err := session.Drain(context.Background()); !errors.Is(err, mountwrite.ErrInvalidRequest) {
		t.Fatalf("nil Drain = %v", err)
	}
	if err := session.Close(context.Background()); !errors.Is(err, mountwrite.ErrInvalidRequest) {
		t.Fatalf("nil Close = %v", err)
	}

	usable := newTestSession(newFakeResolver(), &fakeEngine{})
	if err := usable.Drain(nil); !errors.Is(err, mountwrite.ErrInvalidRequest) {
		t.Fatalf("nil-context Drain = %v", err)
	}
	if err := usable.Close(nil); !errors.Is(err, mountwrite.ErrInvalidRequest) {
		t.Fatalf("nil-context Close = %v", err)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := usable.Mkdir(canceled, mountdav.MkdirRequest{Path: "/x"}); !errors.Is(err, mountdav.ErrWriteUnavailable) {
		t.Fatalf("canceled mutation = %v", err)
	}
}

type fakeResolver struct {
	byPath map[string]Node
}

func newFakeResolver(nodes ...Node) *fakeResolver {
	resolver := &fakeResolver{byPath: map[string]Node{"/": {Kind: mountfs.KindDirectory, Revision: 1}}}
	for _, item := range nodes {
		path := "/" + item.Name
		if item.ParentID != "" {
			for candidatePath, candidate := range resolver.byPath {
				if candidate.ObjectID == item.ParentID {
					path = candidatePath + "/" + item.Name
				}
			}
		}
		resolver.byPath[path] = item
	}
	return resolver
}

func (r *fakeResolver) Resolve(_ context.Context, path string) (Node, bool, error) {
	item, ok := r.byPath[path]
	return item, ok, nil
}

type fakeEngine struct {
	putRequest    mountwrite.PutRequest
	mkdirRequest  mountwrite.MkdirRequest
	moveRequest   mountwrite.MoveRequest
	deleteRequest mountwrite.DeleteRequest
	putResult     mountwrite.MutationResult
	mkdirResult   mountwrite.MutationResult
	moveResult    mountwrite.MutationResult
	deleteResult  mountwrite.MutationResult
	putErr        error
	mkdirErr      error
	moveErr       error
	deleteErr     error
	callCount     int
	drainCalls    int
	closeCalls    int
	recoverCalls  int
}

func (e *fakeEngine) Put(_ context.Context, request mountwrite.PutRequest, body io.Reader) (mountwrite.MutationResult, error) {
	e.callCount++
	e.putRequest = request
	e.putRequest.MasterKey = append([]byte(nil), request.MasterKey...)
	_, _ = io.Copy(io.Discard, body)
	return e.putResult, e.putErr
}

type staticMountKeyProvider []byte

func (provider staticMountKeyProvider) Key() ([]byte, error) {
	return append([]byte(nil), provider...), nil
}

func (e *fakeEngine) Mkdir(_ context.Context, request mountwrite.MkdirRequest) (mountwrite.MutationResult, error) {
	e.callCount++
	e.mkdirRequest = request
	return e.mkdirResult, e.mkdirErr
}

func (e *fakeEngine) Move(_ context.Context, request mountwrite.MoveRequest) (mountwrite.MutationResult, error) {
	e.callCount++
	e.moveRequest = request
	return e.moveResult, e.moveErr
}

func (e *fakeEngine) Delete(_ context.Context, request mountwrite.DeleteRequest) (mountwrite.MutationResult, error) {
	e.callCount++
	e.deleteRequest = request
	return e.deleteResult, e.deleteErr
}

func (e *fakeEngine) Recover(context.Context) (mountwrite.RecoveryReport, error) {
	e.recoverCalls++
	return mountwrite.RecoveryReport{}, nil
}

func (e *fakeEngine) Status() mountwrite.Status {
	return mountwrite.Status{Accepting: true, Active: 2}
}

func (e *fakeEngine) Drain(context.Context) error {
	e.drainCalls++
	return nil
}

func (e *fakeEngine) Close(context.Context) error {
	e.closeCalls++
	return nil
}

func newTestSession(resolver Resolver, engine Engine) *Session {
	return &Session{
		driveID:        testDriveID,
		resolver:       resolver,
		engine:         engine,
		maxObjectBytes: defaultMaxObjectBytes,
	}
}

var _ Engine = (*fakeEngine)(nil)

func TestConstants(t *testing.T) {
	if defaultTrashRetention != 30*24*time.Hour {
		t.Fatal("trash retention changed")
	}
}
