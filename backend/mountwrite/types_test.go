package mountwrite

import (
	"bytes"
	"encoding/json"
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

func TestEncryptedPutRequestRequiresKnownLengthKeyAndCiphertextBudget(t *testing.T) {
	t.Parallel()

	key := bytes.Repeat([]byte("S"), masterKeySize)
	valid := PutRequest{
		DriveID:           42,
		Name:              "private.txt",
		ContentLength:     1,
		MaxBytes:          67,
		EncryptionVersion: EncryptionTDE1,
		MasterKey:         key,
	}
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid encrypted PUT rejected: %v", err)
	}
	tooSmall := valid
	tooSmall.MaxBytes = 66
	if err := tooSmall.Validate(); !errors.Is(err, ErrQuotaExceeded) {
		t.Fatalf("ciphertext budget error = %v, want ErrQuotaExceeded", err)
	}
	invalid := []PutRequest{
		{DriveID: 42, Name: "private.txt", ContentLength: -1, EncryptionVersion: EncryptionTDE1, MasterKey: key},
		{DriveID: 42, Name: "private.txt", ContentLength: 1, EncryptionVersion: EncryptionTDE1, MasterKey: key[:masterKeySize-1]},
		{DriveID: 42, Name: "private.txt", ContentLength: 1, EncryptionVersion: EncryptionVersion(2), MasterKey: key},
		{DriveID: 42, Name: "plain.txt", ContentLength: 1, MasterKey: key},
	}
	for _, request := range invalid {
		if err := request.Validate(); !errors.Is(err, ErrInvalidRequest) {
			t.Fatalf("invalid encrypted PUT %#v error = %v", request, err)
		}
	}

	mutationJSON, err := json.Marshal(valid.mutation())
	if err != nil {
		t.Fatalf("marshal mutation: %v", err)
	}
	if bytes.Contains(mutationJSON, key) {
		t.Fatalf("mutation JSON leaked key: %s", mutationJSON)
	}
	if !bytes.Contains(mutationJSON, []byte(`"encryption_version":1`)) {
		t.Fatalf("mutation JSON omitted encryption version: %s", mutationJSON)
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
