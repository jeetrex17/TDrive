package mountwrite

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestSafeOperationErrorsPreserveOnlyPublicClassification(t *testing.T) {
	t.Parallel()

	tests := []struct {
		cause error
		label string
	}{
		{ErrInvalidRequest, "invalid"},
		{ErrForbidden, "forbidden"},
		{ErrNotFound, "not found"},
		{ErrConflict, "conflicted"},
		{ErrPreconditionFailed, "precondition failed"},
		{ErrTooLarge, "too large"},
		{ErrLocked, "locked"},
		{ErrQuotaExceeded, "out of space"},
		{context.Canceled, "canceled"},
		{ErrLengthMismatch, "length mismatch"},
		{ErrDraining, "draining"},
		{ErrBusy, "busy"},
		{ErrOperationExists, "already active"},
		{ErrOperationInProgress, "already active"},
		{errors.New("secret internal detail"), "unavailable"},
	}
	for _, test := range tests {
		test := test
		t.Run(test.label, func(t *testing.T) {
			err := newOperationError("safe-id", MutationPut, test.cause)
			if got := safeErrorLabel(err); got != test.label {
				t.Fatalf("label = %q, want %q", got, test.label)
			}
		})
	}
	if err := newOperationError("safe-id", MutationPut, nil); err != nil {
		t.Fatalf("nil cause produced %v", err)
	}
	unsafe := newOperationError("bad\noperation", MutationPut, ErrInvalidRequest)
	if got := unsafe.Error(); got != "mount write put invalid" {
		t.Fatalf("unsafe operation ID leaked in %q", got)
	}
}

func TestRequestValidatorsRejectMalformedBoundaries(t *testing.T) {
	t.Parallel()

	put := PutRequest{DriveID: 42, Name: "file.txt", ContentLength: -1}
	invalidPuts := []PutRequest{
		{Name: "file.txt", ContentLength: -1},
		{DriveID: 42, Name: "../file", ContentLength: -1},
		{DriveID: 42, Name: "file.txt", ContentLength: -2},
		{DriveID: 42, Name: "file.txt", ContentLength: -1, MaxBytes: -1},
		{OperationID: "bad\noperation", DriveID: 42, Name: "file.txt", ContentLength: -1},
		{DriveID: 42, Name: "file.txt", ExistingObjectID: "existing", CreateOnly: true, ContentLength: -1},
	}
	for _, request := range invalidPuts {
		if err := request.Validate(); !errors.Is(err, ErrInvalidRequest) {
			t.Fatalf("PUT %#v error = %v", request, err)
		}
	}
	if err := put.Validate(); err != nil {
		t.Fatalf("valid PUT: %v", err)
	}

	invalidMkdir := []MkdirRequest{{DriveID: 0, Name: "folder"}, {DriveID: 42, Name: ".."}}
	for _, request := range invalidMkdir {
		if err := request.Validate(); !errors.Is(err, ErrInvalidRequest) {
			t.Fatalf("MKDIR %#v error = %v", request, err)
		}
	}

	move := MoveRequest{DriveID: 42, ObjectID: "source", DestinationName: "file.txt"}
	invalidMoves := []MoveRequest{
		{ObjectID: "source", DestinationName: "file.txt"},
		{DriveID: 42, DestinationName: "file.txt"},
		{DriveID: 42, ObjectID: "source", DestinationName: "bad/name"},
		{DriveID: 42, ObjectID: "source", DestinationName: "file.txt", OverwriteTargetID: "source"},
		{DriveID: 42, ObjectID: "source", DestinationName: "file.txt", ExpectedTargetRevision: 2},
	}
	for _, request := range invalidMoves {
		if err := request.Validate(); !errors.Is(err, ErrInvalidRequest) {
			t.Fatalf("MOVE %#v error = %v", request, err)
		}
	}
	if err := move.Validate(); err != nil {
		t.Fatalf("valid MOVE: %v", err)
	}

	invalidDeletes := []DeleteRequest{{DriveID: 0, ObjectID: "file"}, {DriveID: 42}, {DriveID: 42, ObjectID: "file", TrashRetention: -time.Second}}
	for _, request := range invalidDeletes {
		if err := request.Validate(); !errors.Is(err, ErrInvalidRequest) {
			t.Fatalf("DELETE %#v error = %v", request, err)
		}
	}
}

func TestMutationValidationAndFunctionInvalidator(t *testing.T) {
	t.Parallel()

	invalid := []Mutation{
		{},
		{Kind: MutationKind("unknown"), DriveID: 42},
		{Kind: MutationPut, DriveID: 42, DestinationName: ""},
		{Kind: MutationMkdir, DriveID: 42, DestinationName: "a/b"},
		{Kind: MutationMove, DriveID: 42, DestinationName: "file.txt"},
		{Kind: MutationDelete, DriveID: 42},
	}
	for _, mutation := range invalid {
		if err := validateMutation(mutation); !errors.Is(err, ErrInvalidRequest) {
			t.Fatalf("mutation %#v error = %v", mutation, err)
		}
	}
	valid := []Mutation{
		{Kind: MutationPut, DriveID: 42, DestinationName: "file.txt"},
		{Kind: MutationMkdir, DriveID: 42, DestinationName: "folder"},
		{Kind: MutationMove, DriveID: 42, ObjectID: "file", DestinationName: "moved.txt"},
		{Kind: MutationDelete, DriveID: 42, ObjectID: "file"},
	}
	for _, mutation := range valid {
		if err := validateMutation(mutation); err != nil {
			t.Fatalf("valid mutation %#v: %v", mutation, err)
		}
	}

	called := false
	invalidator := SnapshotInvalidatorFunc(func(_ context.Context, got SnapshotInvalidation) error {
		called = got.OperationID == "op"
		return ErrUnavailable
	})
	if err := invalidator.Invalidate(context.Background(), SnapshotInvalidation{OperationID: "op"}); !errors.Is(err, ErrUnavailable) || !called {
		t.Fatalf("function invalidator called=%v err=%v", called, err)
	}
}
