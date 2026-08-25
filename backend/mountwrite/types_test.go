package mountwrite

import (
	"errors"
	"testing"
	"time"
)

func TestMutationAffectedParentsReturnsSortedUniqueExactSet(t *testing.T) {
	t.Parallel()

	mutation := Mutation{
		Kind:                MutationMove,
		SourceParentID:      "parent-z",
		DestinationParentID: "parent-a",
	}

	got := mutation.AffectedParents()
	want := []string{"parent-a", "parent-z"}
	assertStringsEqual(t, got, want)

	sameParent := mutation
	sameParent.DestinationParentID = sameParent.SourceParentID
	assertStringsEqual(t, sameParent.AffectedParents(), []string{"parent-z"})
}

func TestValidateTransitionRejectsSkippedAndTerminalTransitions(t *testing.T) {
	t.Parallel()

	if err := ValidateTransition(StateReceiving, StateStaged); err != nil {
		t.Fatalf("valid transition rejected: %v", err)
	}
	if err := ValidateTransition(StateReceiving, StateCommitting); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("skipped transition error = %v, want ErrInvalidTransition", err)
	}
	if err := ValidateTransition(StateDone, StateReceiving); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("terminal transition error = %v, want ErrInvalidTransition", err)
	}
}

func TestRequestsValidateFirstReleaseContract(t *testing.T) {
	t.Parallel()

	validPut := PutRequest{
		DriveID:       42,
		ParentID:      "",
		Name:          "notes.txt",
		ContentLength: 12,
		MaxBytes:      1024,
	}
	if err := validPut.Validate(); err != nil {
		t.Fatalf("valid PUT rejected: %v", err)
	}

	invalidLength := validPut
	invalidLength.ContentLength = 2048
	if err := invalidLength.Validate(); !errors.Is(err, ErrQuotaExceeded) {
		t.Fatalf("oversized PUT error = %v, want ErrQuotaExceeded", err)
	}

	move := MoveRequest{
		DriveID:                42,
		ObjectID:               "file-1",
		SourceParentID:         "folder-a",
		DestinationParentID:    "folder-b",
		DestinationName:        "moved.txt",
		ExpectedSourceRevision: 3,
		OverwriteTargetID:      "file-2",
		ExpectedTargetRevision: 7,
	}
	if err := move.Validate(); err != nil {
		t.Fatalf("valid MOVE rejected: %v", err)
	}

	deleteRequest := DeleteRequest{
		DriveID:        42,
		ObjectID:       "file-1",
		ParentID:       "",
		TrashRetention: 30 * 24 * time.Hour,
	}
	if err := deleteRequest.Validate(); err != nil {
		t.Fatalf("valid DELETE rejected: %v", err)
	}
	deleteRequest.TrashRetention = -time.Second
	if err := deleteRequest.Validate(); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("negative retention error = %v, want ErrInvalidRequest", err)
	}
}

func TestSafeErrorDoesNotLeakInternalDetails(t *testing.T) {
	t.Parallel()

	err := newOperationError("operation-1", MutationPut, errors.New("secret Telegram token abc"))
	if got := err.Error(); got != "mount write put unavailable (operation operation-1)" {
		t.Fatalf("safe error = %q", got)
	}
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("safe error should classify as unavailable: %v", err)
	}
}

func assertStringsEqual(t *testing.T, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}
