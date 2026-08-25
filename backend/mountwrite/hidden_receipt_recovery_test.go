package mountwrite

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"testing"
	"time"
)

func TestCoordinatorRecoveryReconcilesUploadingReceiptBeforeRemovingStage(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	remote := &receiptRecoveryRemote{fakeRemote: &fakeRemote{}}
	coordinator, journal, staging := newTestCoordinator(t, remote, &fakeInvalidator{})
	payload := []byte("durable staged bytes for receipt recovery")
	record := seedUploadingRecord(t, ctx, journal, staging, "recover-send-receipt", payload)

	report, err := coordinator.Recover(ctx)
	if err != nil || report.Aborted != 1 || report.Failed != 0 {
		t.Fatalf("Recover report=%#v err=%v", report, err)
	}
	if remote.recoveryCalls != 1 || remote.discardCalls != 1 {
		t.Fatalf("cleanup calls recovery=%d exact-discard=%d", remote.recoveryCalls, remote.discardCalls)
	}
	if remote.lastRecovery.OperationID != record.OperationID ||
		remote.lastRecovery.DriveID != record.Mutation.DriveID ||
		remote.lastRecovery.Name != record.Mutation.DestinationName ||
		remote.lastRecovery.StoredSize != record.Staged.StoredSize ||
		!bytes.Equal(remote.recoveryPayload, payload) {
		t.Fatalf("recovery request=%#v payload=%q", remote.lastRecovery, remote.recoveryPayload)
	}
	if staging.UsedBytes() != 0 {
		t.Fatalf("staging bytes after confirmed remote cleanup = %d", staging.UsedBytes())
	}
	if state := mustJournalRecord(t, journal, record.OperationID).State; state != StateAborted {
		t.Fatalf("journal state = %s, want aborted", state)
	}
}

func TestCoordinatorRecoveryRetainsStageUntilReceiptCleanupSucceeds(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	recoveryErr := errors.New("Telegram cleanup unavailable")
	remote := &receiptRecoveryRemote{fakeRemote: &fakeRemote{}, recoveryErr: recoveryErr}
	coordinator, journal, staging := newTestCoordinator(t, remote, &fakeInvalidator{})
	payload := []byte("must remain available until exact receipt is recovered")
	record := seedUploadingRecord(t, ctx, journal, staging, "retain-stage-for-receipt", payload)

	report, err := coordinator.Recover(ctx)
	if err != nil || report.Pending != 1 || report.Failed != 0 {
		t.Fatalf("first Recover report=%#v err=%v", report, err)
	}
	if state := mustJournalRecord(t, journal, record.OperationID).State; state != StateCleanupPending {
		t.Fatalf("state after failed cleanup = %s, want cleanup_pending", state)
	}
	if staging.UsedBytes() != int64(len(payload)) {
		t.Fatalf("staging bytes after failed cleanup = %d, want %d", staging.UsedBytes(), len(payload))
	}

	remote.recoveryErr = nil
	report, err = coordinator.Recover(ctx)
	if err != nil || report.Aborted != 1 || report.Failed != 0 {
		t.Fatalf("second Recover report=%#v err=%v", report, err)
	}
	if remote.recoveryCalls != 2 {
		t.Fatalf("recovery calls = %d, want retry", remote.recoveryCalls)
	}
	if staging.UsedBytes() != 0 {
		t.Fatalf("staging bytes after successful retry = %d", staging.UsedBytes())
	}
	if state := mustJournalRecord(t, journal, record.OperationID).State; state != StateAborted {
		t.Fatalf("final journal state = %s, want aborted", state)
	}
}

func TestCoordinatorRecoveryValidatesExactStagedBytesBeforeRemoteCleanup(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		tamper func(*testing.T, StagedObject, []byte)
	}{
		{
			name: "stored length",
			tamper: func(t *testing.T, staged StagedObject, payload []byte) {
				t.Helper()
				if err := os.WriteFile(staged.Path, payload[:len(payload)-1], 0o600); err != nil {
					t.Fatalf("truncate stage: %v", err)
				}
			},
		},
		{
			name: "stored hash",
			tamper: func(t *testing.T, staged StagedObject, payload []byte) {
				t.Helper()
				corrupt := append([]byte(nil), payload...)
				corrupt[len(corrupt)/2] ^= 0xff
				if err := os.WriteFile(staged.Path, corrupt, 0o600); err != nil {
					t.Fatalf("corrupt stage: %v", err)
				}
			},
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			remote := &receiptRecoveryRemote{fakeRemote: &fakeRemote{}}
			coordinator, journal, staging := newTestCoordinator(t, remote, &fakeInvalidator{})
			payload := []byte("validate this exact staged representation")
			record := seedUploadingRecord(t, ctx, journal, staging, "invalid-stage-"+test.name, payload)
			test.tamper(t, *record.Staged, payload)

			report, err := coordinator.Recover(ctx)
			if err != nil || report.Pending != 1 || report.Failed != 0 {
				t.Fatalf("Recover report=%#v err=%v", report, err)
			}
			if remote.recoveryCalls != 0 || remote.discardCalls != 0 {
				t.Fatalf("unverified bytes reached remote: recovery=%d discard=%d", remote.recoveryCalls, remote.discardCalls)
			}
			if state := mustJournalRecord(t, journal, record.OperationID).State; state != StateCleanupPending {
				t.Fatalf("journal state = %s, want cleanup_pending", state)
			}
			if _, statErr := os.Stat(record.Staged.Path); statErr != nil {
				t.Fatalf("unverified stage was removed: %v", statErr)
			}
		})
	}
}

func TestCoordinatorPersistsRecoveredReceiptBeforeStageRemovalAndDelete(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	deleteErr := errors.New("Telegram delete unavailable")
	base := &fakeRemote{discardErr: deleteErr}
	remote := &receiptRecoveryRemote{fakeRemote: base}
	coordinator, journal, staging := newTestCoordinator(t, remote, &fakeInvalidator{})
	payload := []byte("receipt becomes durable before this stage can disappear")
	record := seedUploadingRecord(t, ctx, journal, staging, "persist-recovered-receipt", payload)

	report, err := coordinator.Recover(ctx)
	if err != nil || report.Pending != 1 || report.Failed != 0 {
		t.Fatalf("first Recover report=%#v err=%v", report, err)
	}
	persisted := mustJournalRecord(t, journal, record.OperationID)
	if persisted.State != StateCleanupPending || persisted.Body == nil ||
		len(persisted.Body.MessageIDs) != 1 || persisted.Body.MessageIDs[0] != 701 {
		t.Fatalf("durable cleanup receipt = %#v", persisted)
	}
	if staging.UsedBytes() != int64(len(payload)) {
		t.Fatalf("stage removed before remote cleanup succeeded: %d bytes", staging.UsedBytes())
	}
	if remote.recoveryCalls != 1 || remote.discardCalls != 1 {
		t.Fatalf("calls after failed delete recovery=%d discard=%d", remote.recoveryCalls, remote.discardCalls)
	}

	// A restart sees the durable cleanup receipt directly. A transient Telegram
	// delete failure must leave the receipt and stage pending without preventing
	// the writable mount from starting; later recovery can retry the exact IDs.
	report, err = coordinator.Recover(ctx)
	if err != nil || report.Pending != 1 || report.Failed != 0 {
		t.Fatalf("deferred Recover report=%#v err=%v", report, err)
	}
	if remote.recoveryCalls != 1 || remote.discardCalls != 2 {
		t.Fatalf("deferred calls recovery=%d discard=%d", remote.recoveryCalls, remote.discardCalls)
	}
	if state := mustJournalRecord(t, journal, record.OperationID).State; state != StateCleanupPending {
		t.Fatalf("deferred state = %s, want cleanup_pending", state)
	}
	if staging.UsedBytes() != int64(len(payload)) {
		t.Fatalf("deferred cleanup removed stage: %d bytes", staging.UsedBytes())
	}

	base.mu.Lock()
	base.discardErr = nil
	base.mu.Unlock()
	report, err = coordinator.Recover(ctx)
	if err != nil || report.Aborted != 1 || report.Failed != 0 {
		t.Fatalf("second Recover report=%#v err=%v", report, err)
	}
	if remote.recoveryCalls != 1 || remote.discardCalls != 3 {
		t.Fatalf("retry re-recovered receipt: recovery=%d discard=%d", remote.recoveryCalls, remote.discardCalls)
	}
	if state := mustJournalRecord(t, journal, record.OperationID).State; state != StateAborted {
		t.Fatalf("final journal state = %s, want aborted", state)
	}
	if staging.UsedBytes() != 0 {
		t.Fatalf("stage retained after exact remote cleanup: %d bytes", staging.UsedBytes())
	}
}

func TestCoordinatorRecoveryRetainsStageWhenPersistedReceiptIsRejected(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	remote := &receiptRecoveryRemote{fakeRemote: &fakeRemote{discardErr: ErrInvalidRequest}}
	coordinator, journal, staging := newTestCoordinator(t, remote, &fakeInvalidator{})
	payload := []byte("do not remove this stage for an untrusted persisted receipt")
	record := seedUploadingRecord(t, ctx, journal, staging, "persisted-receipt-rejected", payload)
	body := RemoteBody{
		UploadUUID:    "persisted-receipt-rejected",
		PartCount:     1,
		PlaintextSize: int64(len(payload)),
		StoredSize:    int64(len(payload)),
		MessageIDs:    []int64{912345},
	}
	if _, err := journal.Transition(
		ctx,
		record.OperationID,
		StateUploading,
		StateCleanupPending,
		JournalPatch{Body: &body},
	); err != nil {
		t.Fatalf("persist cleanup receipt: %v", err)
	}

	report, err := coordinator.Recover(ctx)
	if err == nil || report.Failed != 1 {
		t.Fatalf("Recover report=%#v err=%v, want one fail-closed rejection", report, err)
	}
	if remote.discardCalls != 1 {
		t.Fatalf("discard calls = %d, want one validation attempt", remote.discardCalls)
	}
	if staging.UsedBytes() != int64(len(payload)) {
		t.Fatalf("stage removed for rejected persisted receipt: %d", staging.UsedBytes())
	}
	if state := mustJournalRecord(t, journal, record.OperationID).State; state != StateCleanupPending {
		t.Fatalf("journal state = %s, want cleanup_pending", state)
	}
}

func TestCoordinatorLiveUnknownUploadRetainsStageUntilReceiptRecovery(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	recoveryErr := errors.New("receipt recovery temporarily unavailable")
	receipts := &receiptRecoveryRemote{fakeRemote: &fakeRemote{}, recoveryErr: recoveryErr}
	remote := &liveUnknownUploadRemote{receiptRecoveryRemote: receipts}
	coordinator, journal, staging := newTestCoordinator(t, remote, &fakeInvalidator{})
	payload := []byte("accepted remotely but the response receipt was lost")

	_, err := coordinator.Put(ctx, PutRequest{
		OperationID:   "live-unknown-upload",
		DriveID:       42,
		Name:          "unknown.bin",
		ContentLength: int64(len(payload)),
		MaxBytes:      1024,
	}, bytes.NewReader(payload))
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("Put error = %v, want unavailable unknown outcome", err)
	}
	if state := mustJournalRecord(t, journal, "live-unknown-upload").State; state != StateCleanupPending {
		t.Fatalf("state after unavailable receipt recovery = %s, want cleanup_pending", state)
	}
	if staging.UsedBytes() != int64(len(payload)) {
		t.Fatalf("stage removed before receipt recovery: %d bytes", staging.UsedBytes())
	}
	if remote.uploadCalls != 1 || receipts.recoveryCalls != 1 || receipts.discardCalls != 0 {
		t.Fatalf("first calls upload=%d recovery=%d discard=%d", remote.uploadCalls, receipts.recoveryCalls, receipts.discardCalls)
	}

	receipts.recoveryErr = nil
	report, err := coordinator.Recover(ctx)
	if err != nil || report.Aborted != 1 || report.Failed != 0 {
		t.Fatalf("Recover report=%#v err=%v", report, err)
	}
	if remote.uploadCalls != 1 || receipts.recoveryCalls != 2 || receipts.discardCalls != 1 {
		t.Fatalf("final calls upload=%d recovery=%d discard=%d", remote.uploadCalls, receipts.recoveryCalls, receipts.discardCalls)
	}
	if staging.UsedBytes() != 0 {
		t.Fatalf("stage retained after exact discard: %d bytes", staging.UsedBytes())
	}
	record := mustJournalRecord(t, journal, "live-unknown-upload")
	if record.State != StateAborted || record.Body == nil || len(record.Body.MessageIDs) != 1 {
		t.Fatalf("final durable cleanup record = %#v", record)
	}
}

func TestCoordinatorDefiniteZeroPrefixDoesNotInventCleanupUpload(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	receipts := &receiptRecoveryRemote{fakeRemote: &fakeRemote{}}
	remote := &definitePartialUploadRemote{
		receiptRecoveryRemote: receipts,
		body: RemoteBody{
			UploadUUID: "zero-prefix", PartCount: 3,
		},
	}
	coordinator, journal, staging := newTestCoordinator(t, remote, &fakeInvalidator{})
	payload := []byte("definite failure before first Telegram send")

	_, err := coordinator.Put(ctx, PutRequest{
		OperationID: "definite-zero-prefix", DriveID: 42, Name: "zero.bin",
		ContentLength: int64(len(payload)), MaxBytes: 1024,
	}, bytes.NewReader(payload))
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("Put error = %v, want unavailable", err)
	}
	if remote.uploadCalls != 1 || receipts.recoveryCalls != 0 || receipts.discardCalls != 1 {
		t.Fatalf("calls upload=%d recovery=%d discard=%d", remote.uploadCalls, receipts.recoveryCalls, receipts.discardCalls)
	}
	if staging.UsedBytes() != 0 {
		t.Fatalf("zero-prefix stage retained after exact empty cleanup: %d", staging.UsedBytes())
	}
	record := mustJournalRecord(t, journal, "definite-zero-prefix")
	if record.State != StateAborted || record.Body == nil || record.Body.UploadUUID != "zero-prefix" || len(record.Body.MessageIDs) != 0 {
		t.Fatalf("zero-prefix cleanup record = %#v", record)
	}
}

func TestCoordinatorRejectsMalformedPartialCleanupReceipts(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		body RemoteBody
	}{
		{name: "duplicate ids", body: RemoteBody{UploadUUID: "bad-duplicate", PartCount: 2, MessageIDs: []int64{91, 91}}},
		{name: "nonpositive id", body: RemoteBody{UploadUUID: "bad-id", PartCount: 1, MessageIDs: []int64{0}}},
		{name: "too many ids", body: RemoteBody{UploadUUID: "bad-count", PartCount: 1, MessageIDs: []int64{91, 92}}},
		{name: "unbounded parts", body: RemoteBody{UploadUUID: "bad-bound", PartCount: maxHiddenCleanupParts + 1}},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			receipts := &receiptRecoveryRemote{fakeRemote: &fakeRemote{}}
			remote := &definitePartialUploadRemote{receiptRecoveryRemote: receipts, body: test.body}
			coordinator, journal, staging := newTestCoordinator(t, remote, &fakeInvalidator{})
			payload := []byte("do not delete adapter supplied ids")
			operationID := "malformed-" + test.name

			_, err := coordinator.Put(ctx, PutRequest{
				OperationID: operationID, DriveID: 42, Name: "bad.bin",
				ContentLength: int64(len(payload)), MaxBytes: 1024,
			}, bytes.NewReader(payload))
			if !errors.Is(err, ErrUnavailable) {
				t.Fatalf("Put error = %v, want unavailable", err)
			}
			if receipts.discardCalls != 0 || receipts.recoveryCalls != 0 {
				t.Fatalf("malformed receipt reached cleanup: recovery=%d discard=%d", receipts.recoveryCalls, receipts.discardCalls)
			}
			if staging.UsedBytes() != int64(len(payload)) {
				t.Fatalf("stage removed for malformed receipt: %d", staging.UsedBytes())
			}
			record := mustJournalRecord(t, journal, operationID)
			if record.State != StateCleanupPending || record.Body != nil {
				t.Fatalf("malformed receipt was persisted: %#v", record)
			}
		})
	}
}

type receiptRecoveryRemote struct {
	*fakeRemote
	recoveryCalls   int
	recoveryErr     error
	lastRecovery    HiddenUpload
	recoveryPayload []byte
}

type liveUnknownUploadRemote struct {
	*receiptRecoveryRemote
	uploadCalls int
}

type definitePartialUploadRemote struct {
	*receiptRecoveryRemote
	body        RemoteBody
	uploadCalls int
}

func (r *definitePartialUploadRemote) UploadHidden(_ context.Context, request HiddenUpload, _ io.ReadSeeker) (RemoteBody, error) {
	r.uploadCalls++
	body := cloneBody(r.body)
	body.PlaintextSize = request.PlaintextSize
	body.StoredSize = request.StoredSize
	body.Encrypted = request.Encrypted
	body.EncryptionVersion = request.EncryptionVersion
	body.SHA256 = request.SHA256
	body.StoredSHA256 = request.StoredSHA256
	return body, errors.New("definite upload failure")
}

func (r *liveUnknownUploadRemote) UploadHidden(context.Context, HiddenUpload, io.ReadSeeker) (RemoteBody, error) {
	r.uploadCalls++
	return RemoteBody{}, errors.Join(ErrUploadOutcomeUnknown, errors.New("accepted send response lost"))
}

func (r *receiptRecoveryRemote) RecoverHidden(_ context.Context, request HiddenUpload, source io.ReadSeeker) (RemoteBody, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.recoveryCalls++
	r.lastRecovery = request
	payload, err := io.ReadAll(source)
	if err != nil {
		return RemoteBody{}, err
	}
	r.recoveryPayload = append([]byte(nil), payload...)
	if r.recoveryErr != nil {
		return RemoteBody{}, r.recoveryErr
	}
	return RemoteBody{
		UploadUUID:        "recovered-" + request.OperationID,
		PartCount:         1,
		PlaintextSize:     request.PlaintextSize,
		StoredSize:        request.StoredSize,
		Encrypted:         request.Encrypted,
		EncryptionVersion: request.EncryptionVersion,
		SHA256:            request.SHA256,
		StoredSHA256:      request.StoredSHA256,
		MessageIDs:        []int64{701},
	}, nil
}

func seedUploadingRecord(
	t *testing.T,
	ctx context.Context,
	journal Journal,
	staging StagingStore,
	operationID string,
	payload []byte,
) JournalRecord {
	t.Helper()
	staged, err := staging.Stage(
		ctx,
		plaintextStageRequest(operationID, int64(len(payload)), 0),
		bytes.NewReader(payload),
	)
	if err != nil {
		t.Fatalf("stage: %v", err)
	}
	at := time.Unix(1_700_000_000, 0).UTC()
	record := JournalRecord{
		OperationID: operationID,
		Mutation: Mutation{
			Kind:                MutationPut,
			DriveID:             42,
			DestinationParentID: "",
			DestinationName:     "receipt.bin",
			ContentLength:       int64(len(payload)),
		},
		State:     StateUploading,
		Staged:    &staged,
		CreatedAt: at,
		UpdatedAt: at,
	}
	if err := journal.Create(ctx, record); err != nil {
		t.Fatalf("create uploading record: %v", err)
	}
	return record
}
