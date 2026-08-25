package mountadapter

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"io"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"TDrive/backend/mountwrite"
	"TDrive/backend/projection"
	fileservice "TDrive/backend/services/file"
	"TDrive/backend/tgclient"
)

func TestTelegramRemoteUploadAndCleanupAdapters(t *testing.T) {
	store := &fakeHiddenStore{uploadResult: fileservice.HiddenBody{
		UploadUUID: "u-1", PartCount: 2, StoredSize: 7, PlaintextSize: 7,
		MessageIDs: []int64{41, 42},
	}, recoveryResult: fileservice.HiddenBody{
		UploadUUID: "u-1", PartCount: 2, StoredSize: 7, PlaintextSize: 7,
		MessageIDs: []int64{41, 42},
	}}
	remote := &TelegramRemote{driveID: testDriveID, files: store}
	digest := sha256.Sum256([]byte("payload"))
	body, err := remote.UploadHidden(context.Background(), mountwrite.HiddenUpload{
		OperationID: "op-upload", DriveID: testDriveID, ParentID: "d:p",
		Name: "x.txt", PlaintextSize: 7, StoredSize: 7, SHA256: digest,
	}, bytes.NewReader([]byte("payload")))
	if err != nil {
		t.Fatalf("UploadHidden: %v", err)
	}
	if body.UploadUUID != "u-1" || body.PartCount != 2 || body.SHA256 != digest || body.MessageIDs[0] != 41 {
		t.Fatalf("body = %+v", body)
	}
	if store.uploadRequest.OperationID != "op-upload" || store.uploadRequest.Name != "x.txt" {
		t.Fatalf("service request = %+v", store.uploadRequest)
	}
	recovered, err := remote.RecoverHidden(context.Background(), mountwrite.HiddenUpload{
		OperationID: "op-upload", DriveID: testDriveID, ParentID: "d:p",
		Name: "x.txt", PlaintextSize: 7, StoredSize: 7, SHA256: digest,
	}, bytes.NewReader([]byte("payload")))
	if err != nil {
		t.Fatalf("RecoverHidden: %v", err)
	}
	if recovered.UploadUUID != "u-1" || recovered.MessageIDs[1] != 42 ||
		store.recoveryRequest.OperationID != "op-upload" || !bytes.Equal(store.recoveryPayload, []byte("payload")) {
		t.Fatalf("recovered=%+v request=%+v payload=%q", recovered, store.recoveryRequest, store.recoveryPayload)
	}
	if err := remote.DiscardHidden(context.Background(), "op-upload", &body); err != nil {
		t.Fatalf("DiscardHidden precise: %v", err)
	}
	if store.discardBody.UploadUUID != "u-1" || store.discardOperation != "" {
		t.Fatalf("precise cleanup = body %+v operation %q", store.discardBody, store.discardOperation)
	}
	if err := remote.DiscardHidden(context.Background(), "op-crashed", nil); err != nil {
		t.Fatalf("DiscardHidden operation: %v", err)
	}
	if store.discardOperation != "op-crashed" {
		t.Fatalf("operation cleanup = %q", store.discardOperation)
	}
}

func TestTelegramRemotePreservesPartialHiddenReceiptsOnUploadError(t *testing.T) {
	tests := []struct {
		name        string
		serviceErr  error
		wantUnknown bool
	}{
		{name: "definite", serviceErr: errors.New("next part rejected")},
		{name: "unknown", serviceErr: errors.Join(tgclient.ErrSendOutcomeUnknown, errors.New("response lost")), wantUnknown: true},
		{name: "projection", serviceErr: errors.Join(fileservice.ErrHiddenReceiptRecoveryRequired, errors.New("projection unavailable")), wantUnknown: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store := &fakeHiddenStore{
				uploadResult: fileservice.HiddenBody{
					UploadUUID: "partial-u", PartCount: 3, StoredSize: 10, PlaintextSize: 10,
					MessageIDs: []int64{41},
				},
				uploadErr: test.serviceErr,
			}
			remote := &TelegramRemote{driveID: testDriveID, files: store}
			body, err := remote.UploadHidden(context.Background(), mountwrite.HiddenUpload{
				OperationID: "partial-op", DriveID: testDriveID, Name: "partial.bin",
				PlaintextSize: 10, StoredSize: 10,
			}, bytes.NewReader([]byte("0123456789")))
			if !errors.Is(err, test.serviceErr) {
				t.Fatalf("UploadHidden error = %v, want %v", err, test.serviceErr)
			}
			if errors.Is(err, mountwrite.ErrUploadOutcomeUnknown) != test.wantUnknown {
				t.Fatalf("unknown classification = %v, want %v (err=%v)", errors.Is(err, mountwrite.ErrUploadOutcomeUnknown), test.wantUnknown, err)
			}
			if body.UploadUUID != "partial-u" || body.PartCount != 3 || len(body.MessageIDs) != 1 || body.MessageIDs[0] != 41 {
				t.Fatalf("partial body was lost: %+v", body)
			}
		})
	}
}

func TestCoordinatorMultipartUnknownOutcomeRecoversOnlyUncertainPart(t *testing.T) {
	ctx := context.Background()
	db := newProjectionDB(t)
	if err := mountwrite.EnsureJournalSchema(ctx, db); err != nil {
		t.Fatalf("EnsureJournalSchema: %v", err)
	}
	journal, err := mountwrite.NewSQLiteJournal(db)
	if err != nil {
		t.Fatalf("NewSQLiteJournal: %v", err)
	}
	staging, err := mountwrite.NewDiskStagingStore(mountwrite.DiskStagingConfig{
		Root: filepath.Join(t.TempDir(), "staging"), MaxObjectBytes: 1024,
		MaxAggregateBytes: 2048, MaxConcurrent: 1,
	})
	if err != nil {
		t.Fatalf("NewDiskStagingStore: %v", err)
	}

	fakeTG := tgclient.NewFake(7)
	peer := tgclient.InputPeer{ChannelID: testDriveID, AccessHash: 91}
	fakeTG.SeedChannel(peer, "Personal")
	telegram := &loseSecondHiddenReceiptClient{Fake: fakeTG}
	peers := testPeerResolver{peer: peer}
	files := &fileservice.Service{
		DB: db, TG: telegram, Peers: peers, ActorID: fakeTG.SelfID,
		MaxUploadBytes: 4,
	}
	remote, err := NewTelegramRemote(TelegramRemoteConfig{
		DB: db, DriveID: testDriveID, Files: files, Telegram: telegram,
		Peers: peers, ActorID: fakeTG.SelfID,
	})
	if err != nil {
		t.Fatalf("NewTelegramRemote: %v", err)
	}
	coordinator, err := mountwrite.NewCoordinator(mountwrite.CoordinatorConfig{
		Journal: journal, Staging: staging, Remote: remote,
		Invalidator:         mountwrite.SnapshotInvalidatorFunc(func(context.Context, mountwrite.SnapshotInvalidation) error { return nil }),
		MaxActiveOperations: 1, MaxQueuedOperations: 0,
	})
	if err != nil {
		t.Fatalf("NewCoordinator: %v", err)
	}
	payload := []byte("0123456789") // three 4-byte parts; part 2 is never attempted.
	_, err = coordinator.Put(ctx, mountwrite.PutRequest{
		OperationID: "multipart-unknown-e2e", DriveID: testDriveID,
		Name: "unknown.bin", ContentLength: int64(len(payload)), MaxBytes: 1024,
	}, bytes.NewReader(payload))
	if !errors.Is(err, mountwrite.ErrUnavailable) {
		t.Fatalf("Put error = %v, want unavailable", err)
	}

	sent := fakeTG.SentFiles()
	if len(sent) != 2 {
		t.Fatalf("sent files = %+v, want accepted part 0 and uncertain part 1 only", sent)
	}
	for index, message := range sent {
		op, parseErr := projection.Parse(message.Caption)
		if parseErr != nil || op.PartIndex != index {
			t.Fatalf("sent[%d] op=%+v err=%v", index, op, parseErr)
		}
	}
	deleted := fakeTG.DeletedBatches()
	wantIDs := []int64{sent[0].MsgID, sent[1].MsgID}
	if len(deleted) != 1 || !slices.Equal(deleted[0], wantIDs) {
		t.Fatalf("deleted = %+v, want exact accepted receipts %v", deleted, wantIDs)
	}
	record, found, err := journal.Get(ctx, "multipart-unknown-e2e")
	if err != nil || !found || record.State != mountwrite.StateAborted || record.Body == nil {
		t.Fatalf("journal record=%+v found=%v err=%v", record, found, err)
	}
	if record.Body.PartCount != 3 || !slices.Equal(record.Body.MessageIDs, wantIDs) {
		t.Fatalf("durable partial cleanup body = %+v", record.Body)
	}
	if staging.UsedBytes() != 0 {
		t.Fatalf("staging retained after exact cleanup: %d", staging.UsedBytes())
	}
	parts, err := projection.PartsForUUID(db, testDriveID, record.Body.UploadUUID)
	if err != nil || len(parts) != 0 {
		t.Fatalf("hidden part pointers after cleanup = %+v, err=%v", parts, err)
	}
}

func TestCoordinatorRecoveryFailsClosedOnPersistedHiddenReceiptTamper(t *testing.T) {
	tests := []struct {
		name   string
		tamper func(*persistedHiddenCleanupHarness)
	}{
		{
			name: "arbitrary unique message id",
			tamper: func(harness *persistedHiddenCleanupHarness) {
				harness.record.Body.MessageIDs[0] += 10_000
			},
		},
		{
			name: "another operation upload uuid",
			tamper: func(harness *persistedHiddenCleanupHarness) {
				harness.record.Body.UploadUUID = "hu-00000000000000000000000000000000"
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			harness := newPersistedHiddenCleanupHarness(t, "restart-tamper-"+test.name)
			test.tamper(harness)
			if err := harness.journal.Create(context.Background(), harness.record); err != nil {
				t.Fatalf("persist tampered cleanup record: %v", err)
			}

			report, err := harness.coordinator.Recover(context.Background())
			if err == nil || report.Failed != 1 {
				t.Fatalf("Recover report=%+v err=%v, want fail-closed cleanup", report, err)
			}
			if harness.staging.UsedBytes() != harness.record.Staged.StoredSize {
				t.Fatalf("stage removed before receipt ownership validation: %d", harness.staging.UsedBytes())
			}
			if deleted := harness.telegram.DeletedBatches(); len(deleted) != 0 {
				t.Fatalf("tampered receipt reached Telegram deletion: %+v", deleted)
			}
			record, found, getErr := harness.journal.Get(context.Background(), harness.record.OperationID)
			if getErr != nil || !found || record.State != mountwrite.StateCleanupPending {
				t.Fatalf("cleanup record=%+v found=%v err=%v", record, found, getErr)
			}
		})
	}
}

func TestCoordinatorRecoveryTreatsMissingHiddenProjectionAsCompletedCleanup(t *testing.T) {
	harness := newPersistedHiddenCleanupHarness(t, "restart-after-pointer-cleanup")
	if err := harness.journal.Create(context.Background(), harness.record); err != nil {
		t.Fatalf("persist cleanup record: %v", err)
	}
	if err := projection.DeleteFileParts(
		harness.db,
		testDriveID,
		harness.record.Body.UploadUUID,
	); err != nil {
		t.Fatalf("simulate completed pointer cleanup: %v", err)
	}

	report, err := harness.coordinator.Recover(context.Background())
	if err != nil || report.Aborted != 1 || report.Failed != 0 {
		t.Fatalf("Recover report=%+v err=%v", report, err)
	}
	if harness.staging.UsedBytes() != 0 {
		t.Fatalf("stage retained after idempotent cleanup completion: %d", harness.staging.UsedBytes())
	}
	if deleted := harness.telegram.DeletedBatches(); len(deleted) != 0 {
		t.Fatalf("missing projection triggered a second Telegram deletion: %+v", deleted)
	}
}

type persistedHiddenCleanupHarness struct {
	db          *sql.DB
	journal     *mountwrite.SQLiteJournal
	staging     *mountwrite.DiskStagingStore
	coordinator *mountwrite.Coordinator
	telegram    *tgclient.Fake
	record      mountwrite.JournalRecord
}

func newPersistedHiddenCleanupHarness(t *testing.T, operationID string) *persistedHiddenCleanupHarness {
	t.Helper()
	ctx := context.Background()
	payload := []byte("persisted hidden cleanup receipt")
	db := newProjectionDB(t)
	if err := mountwrite.EnsureJournalSchema(ctx, db); err != nil {
		t.Fatalf("EnsureJournalSchema: %v", err)
	}
	journal, err := mountwrite.NewSQLiteJournal(db)
	if err != nil {
		t.Fatalf("NewSQLiteJournal: %v", err)
	}
	staging, err := mountwrite.NewDiskStagingStore(mountwrite.DiskStagingConfig{
		Root: filepath.Join(t.TempDir(), "staging"), MaxObjectBytes: 1024,
		MaxAggregateBytes: 2048, MaxConcurrent: 1,
	})
	if err != nil {
		t.Fatalf("NewDiskStagingStore: %v", err)
	}
	staged, err := staging.Stage(ctx, mountwrite.StageRequest{
		OperationID: operationID, PlaintextSize: int64(len(payload)), MaxBytes: 1024,
	}, bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("Stage: %v", err)
	}

	telegram := tgclient.NewFake(7)
	peer := tgclient.InputPeer{ChannelID: testDriveID, AccessHash: 91}
	telegram.SeedChannel(peer, "Personal")
	peers := testPeerResolver{peer: peer}
	files := &fileservice.Service{
		DB: db, TG: telegram, Peers: peers, ActorID: telegram.SelfID,
		MaxUploadBytes: 1024,
	}
	hidden, err := files.UploadHidden(ctx, testDriveID, fileservice.HiddenUploadRequest{
		OperationID: operationID, Name: "receipt.bin",
		StoredSize: int64(len(payload)), PlaintextSize: int64(len(payload)),
	}, bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("UploadHidden: %v", err)
	}
	remote, err := NewTelegramRemote(TelegramRemoteConfig{
		DB: db, DriveID: testDriveID, Files: files, Telegram: telegram,
		Peers: peers, ActorID: telegram.SelfID,
	})
	if err != nil {
		t.Fatalf("NewTelegramRemote: %v", err)
	}
	coordinator, err := mountwrite.NewCoordinator(mountwrite.CoordinatorConfig{
		Journal: journal, Staging: staging, Remote: remote,
		Invalidator:         mountwrite.SnapshotInvalidatorFunc(func(context.Context, mountwrite.SnapshotInvalidation) error { return nil }),
		MaxActiveOperations: 1, MaxQueuedOperations: 0,
	})
	if err != nil {
		t.Fatalf("NewCoordinator: %v", err)
	}
	at := time.Unix(1_700_000_000, 0).UTC()
	body := mountwrite.RemoteBody{
		UploadUUID: hidden.UploadUUID, PartCount: hidden.PartCount,
		PlaintextSize: hidden.PlaintextSize, StoredSize: hidden.StoredSize,
		SHA256: staged.SHA256, StoredSHA256: staged.StoredSHA256,
		MessageIDs: append([]int64(nil), hidden.MessageIDs...),
	}
	return &persistedHiddenCleanupHarness{
		db: db, journal: journal, staging: staging, coordinator: coordinator, telegram: telegram,
		record: mountwrite.JournalRecord{
			OperationID: operationID,
			Mutation: mountwrite.Mutation{
				Kind: mountwrite.MutationPut, DriveID: testDriveID,
				DestinationName: "receipt.bin", ContentLength: int64(len(payload)), MaxBytes: 1024,
			},
			State: mountwrite.StateCleanupPending, Staged: &staged, Body: &body,
			CreatedAt: at, UpdatedAt: at,
		},
	}
}

type loseSecondHiddenReceiptClient struct {
	*tgclient.Fake
	calls int
}

func (c *loseSecondHiddenReceiptClient) SendFileWithRandomID(
	ctx context.Context,
	peer tgclient.InputPeer,
	source io.Reader,
	name string,
	caption string,
	size int64,
	progress func(int64, int64),
	randomID int64,
) (tgclient.SendFileResult, error) {
	c.calls++
	result, err := c.Fake.SendFileWithRandomID(ctx, peer, source, name, caption, size, progress, randomID)
	if err != nil {
		return result, err
	}
	if c.calls == 2 {
		return tgclient.SendFileResult{}, errors.Join(tgclient.ErrSendOutcomeUnknown, errors.New("lost accepted receipt"))
	}
	return result, nil
}

func TestTelegramRemoteCommitsAndReconcilesVisibleFileExactlyOnce(t *testing.T) {
	db := newProjectionDB(t)
	project(t, db, 50, projection.Op{Type: projection.OpFilePart, UploadUUID: "hidden-u", PartIndex: 0, FileSize: 5})
	fakeTG := tgclient.NewFake(99)
	fakeTG.SeedChannel(tgclient.InputPeer{ChannelID: testDriveID, AccessHash: 7}, "Personal")
	now := time.Unix(2_000, 0)
	remote := newTestTelegramRemote(t, db, fakeTG, now)
	digest := sha256.Sum256([]byte("hello"))
	request := mountwrite.CommitRequest{
		OperationID: "op-file-commit",
		Mutation: mountwrite.Mutation{
			Kind: mountwrite.MutationPut, DriveID: testDriveID,
			DestinationParentID: "", DestinationName: "Hello.txt", CreateOnly: true,
		},
		Body: &mountwrite.RemoteBody{
			UploadUUID: "hidden-u", PartCount: 1, StoredSize: 5, PlaintextSize: 5,
			SHA256: digest, MessageIDs: []int64{50},
		},
	}

	first, err := remote.Commit(context.Background(), request)
	if err != nil {
		t.Fatalf("Commit: %v", err)
	}
	second, err := remote.Commit(context.Background(), request)
	if err != nil {
		t.Fatalf("idempotent Commit: %v", err)
	}
	if first != second || first.ObjectID != "f:100" || first.Revision != 1 || !first.Created || first.SHA256 != digest {
		t.Fatalf("results = %+v / %+v", first, second)
	}
	controls := fakeTG.SentControls()
	if len(controls) != 1 || !controls[0].Silent {
		t.Fatalf("controls = %+v, want one silent idempotent control", controls)
	}
	op, err := projection.Parse(controls[0].Text)
	if err != nil {
		t.Fatalf("Parse control: %v", err)
	}
	if op.Type != projection.OpFileCommit || op.OpID != request.OperationID || op.UploadUUID != "hidden-u" || op.PartCount != 1 || op.ContentHash == "" || op.FileUploadTime != now.Unix() {
		t.Fatalf("commit op = %+v", op)
	}
	resolved, found, err := remote.Reconcile(context.Background(), request.OperationID)
	if err != nil || !found || resolved != first {
		t.Fatalf("Reconcile = %+v, found=%v, err=%v", resolved, found, err)
	}
}

func TestBuildProjectionOperationEncryptedBodyPublishesOnlyCryptoMetadata(t *testing.T) {
	request := mountwrite.CommitRequest{
		OperationID: "encrypted-file",
		Mutation: mountwrite.Mutation{
			Kind: mountwrite.MutationPut, DriveID: testDriveID,
			DestinationName: "secret.bin", CreateOnly: true,
			EncryptionVersion: mountwrite.EncryptionTDE1,
		},
		Body: &mountwrite.RemoteBody{
			UploadUUID: "hidden-encrypted", PartCount: 1,
			PlaintextSize: 7, StoredSize: 73,
			Encrypted: true, EncryptionVersion: mountwrite.EncryptionTDE1,
		},
	}
	op, err := buildProjectionOperation(request, time.Unix(4_000, 0))
	if err != nil {
		t.Fatalf("buildProjectionOperation: %v", err)
	}
	if !op.Encrypted || op.EncryptionVersion != int(mountwrite.EncryptionTDE1) || op.PlaintextSize != 7 || op.FileSize != 73 {
		t.Fatalf("encrypted projection metadata = %+v", op)
	}
	if op.ContentHash != "" {
		t.Fatalf("encrypted projection exposed a plaintext hash: %q", op.ContentHash)
	}
}

func TestTelegramRemoteBuildsRetentionAndCASOperations(t *testing.T) {
	now := time.Unix(5_000, 0)
	digest := sha256.Sum256([]byte("new"))
	body := &mountwrite.RemoteBody{UploadUUID: "u", PartCount: 1, StoredSize: 3, PlaintextSize: 3, SHA256: digest}
	tests := []struct {
		name     string
		request  mountwrite.CommitRequest
		wantType projection.OpType
		assert   func(*testing.T, projection.Op)
	}{
		{
			name: "replace",
			request: mountwrite.CommitRequest{OperationID: "replace", Mutation: mountwrite.Mutation{
				Kind: mountwrite.MutationPut, DriveID: testDriveID, ObjectID: "f:4", ExpectedRevision: 3,
			}, Body: body},
			wantType: projection.OpFileReplace,
			assert: func(t *testing.T, op projection.Op) {
				if op.ExpectedRevision != 3 || op.RetainedUntil != now.Add(defaultRevisionRetention).Unix() {
					t.Fatalf("replace op = %+v", op)
				}
			},
		},
		{
			name: "mkdir",
			request: mountwrite.CommitRequest{OperationID: "mkdir", Mutation: mountwrite.Mutation{
				Kind: mountwrite.MutationMkdir, DriveID: testDriveID, DestinationParentID: "d:p", DestinationName: "New",
			}},
			wantType: projection.OpFolderCommit,
			assert: func(t *testing.T, op projection.Op) {
				if !projection.IsFolderID(op.Obj) || op.Obj != deterministicFolderID("mkdir") {
					t.Fatalf("mkdir op = %+v", op)
				}
			},
		},
		{
			name: "overwrite move",
			request: mountwrite.CommitRequest{OperationID: "move", Mutation: mountwrite.Mutation{
				Kind: mountwrite.MutationMove, DriveID: testDriveID, ObjectID: "f:4", ExpectedRevision: 3,
				DestinationParentID: "d:p", DestinationName: "Moved", OverwriteTargetID: "f:5", ExpectedTargetRevision: 8,
			}},
			wantType: projection.OpRelocate,
			assert: func(t *testing.T, op projection.Op) {
				if !op.Overwrite || op.DestinationObj != "f:5" || op.ExpectedDestinationRevision != 8 || op.PurgeAfter != now.Add(defaultTrashRetention).Unix() {
					t.Fatalf("move op = %+v", op)
				}
			},
		},
		{
			name: "trash",
			request: mountwrite.CommitRequest{OperationID: "trash", Mutation: mountwrite.Mutation{
				Kind: mountwrite.MutationDelete, DriveID: testDriveID, ObjectID: "d:p", ExpectedRevision: 4,
				TrashRetention: defaultTrashRetention,
			}},
			wantType: projection.OpTrashTree,
			assert: func(t *testing.T, op projection.Op) {
				if op.DeletedAt != now.Unix() || op.PurgeAfter != now.Add(defaultTrashRetention).Unix() {
					t.Fatalf("trash op = %+v", op)
				}
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			op, err := buildProjectionOperation(test.request, now)
			if err != nil {
				t.Fatalf("buildProjectionOperation: %v", err)
			}
			if op.Type != test.wantType || op.ProtocolVersion != 1 || op.OpID != test.request.OperationID {
				t.Fatalf("op = %+v", op)
			}
			test.assert(t, op)
		})
	}
}

func TestTelegramRemoteCommitRejectedProjectionReturnsPrecondition(t *testing.T) {
	db := newProjectionDB(t)
	fakeTG := tgclient.NewFake(1)
	fakeTG.SeedChannel(tgclient.InputPeer{ChannelID: testDriveID}, "Personal")
	remote := newTestTelegramRemote(t, db, fakeTG, time.Unix(100, 0))
	_, err := remote.Commit(context.Background(), mountwrite.CommitRequest{
		OperationID: "stale-replace",
		Mutation: mountwrite.Mutation{
			Kind: mountwrite.MutationPut, DriveID: testDriveID,
			ObjectID: "f:999", ExpectedRevision: 1,
		},
		Body: &mountwrite.RemoteBody{ContentRef: "50", StoredSize: 1, PlaintextSize: 1},
	})
	if !errors.Is(err, mountwrite.ErrPreconditionFailed) {
		t.Fatalf("Commit error = %v, want precondition failure", err)
	}
}

func TestTelegramRemoteReconcilesRecentHistoryAndRejectedOutcome(t *testing.T) {
	sourceDB := newProjectionDB(t)
	project(t, sourceDB, 70, projection.Op{Type: projection.OpFilePart, UploadUUID: "history-u", PartIndex: 0, FileSize: 1})
	op := projection.Op{
		Type: projection.OpFileCommit, ProtocolVersion: 1, OpID: "history-op",
		Name: "history.txt", UploadUUID: "history-u", PartCount: 1,
		FileSize: 1, PlaintextSize: 1,
	}
	fakeTG := tgclient.NewFake(1)
	fakeTG.SeedChannel(tgclient.InputPeer{ChannelID: testDriveID}, "Personal")
	fakeTG.SeedHistory(tgclient.HistoryMessage{MsgID: 71, FromID: 1, Text: projection.Format(op)})

	db := newProjectionDB(t)
	project(t, db, 70, projection.Op{Type: projection.OpFilePart, UploadUUID: "history-u", PartIndex: 0, FileSize: 1})
	remote := newTestTelegramRemote(t, db, fakeTG, time.Unix(100, 0))
	result, found, err := remote.Reconcile(context.Background(), "history-op")
	if err != nil || !found || result.ObjectID != "f:71" || result.Revision != 1 {
		t.Fatalf("history reconcile = %+v, found=%v, err=%v", result, found, err)
	}

	conflicting := projection.Op{
		Type: projection.OpFolderCommit, ProtocolVersion: 1, OpID: "conflicting-folder",
		Obj: "d:other", Name: "history.txt",
	}
	projectRaw(t, db, 72, conflicting)
	_, rejectedFound, err := remote.Reconcile(context.Background(), "conflicting-folder")
	if !rejectedFound || !errors.Is(err, mountwrite.ErrConflict) {
		t.Fatalf("rejected reconcile = found %v error %v, want found conflict", rejectedFound, err)
	}
}

func TestTelegramRemoteNeverReportsSuccessBeforeProjectionApplies(t *testing.T) {
	db := newProjectionDB(t)
	project(t, db, 50, projection.Op{Type: projection.OpFilePart, UploadUUID: "uncertain-u", PartIndex: 0, FileSize: 1})
	fakeTG := tgclient.NewFake(1)
	fakeTG.SeedChannel(tgclient.InputPeer{ChannelID: testDriveID}, "Personal")
	remote := newTestTelegramRemote(t, db, fakeTG, time.Unix(100, 0))
	if _, err := db.Exec(`DROP TABLE replay_log`); err != nil {
		t.Fatal(err)
	}
	request := mountwrite.CommitRequest{
		OperationID: "uncertain-op",
		Mutation:    mountwrite.Mutation{Kind: mountwrite.MutationPut, DriveID: testDriveID, DestinationName: "x.txt", CreateOnly: true},
		Body:        &mountwrite.RemoteBody{UploadUUID: "uncertain-u", PartCount: 1, StoredSize: 1, PlaintextSize: 1},
	}
	persistedReceipt := ""
	request.PersistCommitRef = func(receipt string) error {
		persistedReceipt = receipt
		return nil
	}
	uncertain, err := remote.Commit(context.Background(), request)
	if !errors.Is(err, mountwrite.ErrCommitOutcomeUnknown) {
		t.Fatalf("Commit error = %v, want ErrCommitOutcomeUnknown", err)
	}
	if uncertain.CommitRef == "" || persistedReceipt != uncertain.CommitRef {
		t.Fatal("Commit did not preserve the accepted Telegram message receipt")
	}
	if len(fakeTG.SentControls()) != 1 {
		t.Fatalf("controls = %+v, want sent uncertain commit", fakeTG.SentControls())
	}

	recoveryDB := newProjectionDB(t)
	project(t, recoveryDB, 50, projection.Op{Type: projection.OpFilePart, UploadUUID: "uncertain-u", PartIndex: 0, FileSize: 1})
	recovery := newTestTelegramRemote(t, recoveryDB, fakeTG, time.Unix(100, 0))
	result, found, err := recovery.Reconcile(context.Background(), request.OperationID)
	if err != nil || !found || result.ObjectID != "f:100" {
		t.Fatalf("recovery reconcile = %+v, found=%v, err=%v", result, found, err)
	}
	if len(fakeTG.SentControls()) != 1 {
		t.Fatal("reconciliation duplicated the Telegram control")
	}
}

func TestTelegramRemoteReconcilesExactReceiptOutsideHistoryWindow(t *testing.T) {
	db := newProjectionDB(t)
	fakeTG := tgclient.NewFake(1)
	fakeTG.SeedChannel(tgclient.InputPeer{ChannelID: testDriveID}, "Personal")
	op := projection.Op{
		Type: projection.OpFolderCommit, ProtocolVersion: 1, OpID: "old-receipt",
		Obj: deterministicFolderID("old-receipt"), Name: "Recovered",
	}
	messages := []tgclient.HistoryMessage{{MsgID: 51, FromID: 1, Text: projection.Format(op)}}
	for msgID := int64(52); msgID <= 552; msgID++ {
		messages = append(messages, tgclient.HistoryMessage{MsgID: msgID, FromID: 1, Text: "ordinary message"})
	}
	fakeTG.SeedHistory(messages...)
	remote, err := NewTelegramRemote(TelegramRemoteConfig{
		DB: db, DriveID: testDriveID, Files: &fakeHiddenStore{}, Telegram: fakeTG,
		Peers:   testPeerResolver{peer: tgclient.InputPeer{ChannelID: testDriveID}},
		ActorID: fakeTG.SelfID, Now: func() time.Time { return time.Unix(100, 0) },
		HistoryPageSize: 2, HistoryPages: 1,
	})
	if err != nil {
		t.Fatalf("NewTelegramRemote: %v", err)
	}

	request := mountwrite.CommitRequest{
		OperationID: op.OpID,
		CommitTime:  time.Unix(100, 0),
		Mutation: mountwrite.Mutation{
			Kind: mountwrite.MutationMkdir, DriveID: testDriveID, DestinationName: op.Name,
		},
	}
	result, found, err := remote.ReconcileReceipt(context.Background(), request, "51")
	if err != nil || !found || result.ObjectID != op.Obj || result.CommitRef != "51" {
		t.Fatalf("ReconcileReceipt = %+v, found=%v, err=%v", result, found, err)
	}
}

func TestTelegramRemoteRejectsInvalidCommitReceipt(t *testing.T) {
	remote := newTestTelegramRemote(t, newProjectionDB(t), tgclient.NewFake(1), time.Unix(100, 0))
	request := mountwrite.CommitRequest{OperationID: "receipt-op", CommitTime: time.Unix(100, 0), Mutation: mountwrite.Mutation{Kind: mountwrite.MutationMkdir, DriveID: testDriveID, DestinationName: "Receipt"}}
	if _, _, err := remote.ReconcileReceipt(context.Background(), request, "not-a-message"); !errors.Is(err, mountwrite.ErrInvalidRequest) {
		t.Fatalf("ReconcileReceipt error = %v, want ErrInvalidRequest", err)
	}
}

func TestTelegramRemoteReceiptReconciliationBoundaries(t *testing.T) {
	t.Run("already projected", func(t *testing.T) {
		db := newProjectionDB(t)
		op := projection.Op{
			Type: projection.OpFolderCommit, ProtocolVersion: 1, OpID: "already-projected",
			Obj: deterministicFolderID("already-projected"), Name: "Ready",
		}
		project(t, db, 81, op)
		remote := newTestTelegramRemote(t, db, tgclient.NewFake(1), time.Unix(100, 0))
		request := mountwrite.CommitRequest{OperationID: op.OpID, CommitTime: time.Unix(100, 0), Mutation: mountwrite.Mutation{Kind: mountwrite.MutationMkdir, DriveID: testDriveID, DestinationName: op.Name}}
		result, found, err := remote.ReconcileReceipt(context.Background(), request, "81")
		if err != nil || !found || result.CommitRef != "81" || result.ObjectID != op.Obj {
			t.Fatalf("ReconcileReceipt = %+v, found=%v, err=%v", result, found, err)
		}
	})

	t.Run("exact message absent", func(t *testing.T) {
		fakeTG := tgclient.NewFake(1)
		remote := newTestTelegramRemote(t, newProjectionDB(t), fakeTG, time.Unix(100, 0))
		request := mountwrite.CommitRequest{OperationID: "missing-receipt", CommitTime: time.Unix(100, 0), Mutation: mountwrite.Mutation{Kind: mountwrite.MutationMkdir, DriveID: testDriveID, DestinationName: "Missing"}}
		result, found, err := remote.ReconcileReceipt(context.Background(), request, "91")
		if err != nil || found || result != (mountwrite.MutationResult{}) {
			t.Fatalf("ReconcileReceipt = %+v, found=%v, err=%v", result, found, err)
		}
	})

	t.Run("receipt belongs to another operation", func(t *testing.T) {
		fakeTG := tgclient.NewFake(1)
		fakeTG.SeedHistory(tgclient.HistoryMessage{
			MsgID: 92, FromID: 1,
			Text: projection.Format(projection.Op{
				Type: projection.OpFolderCommit, ProtocolVersion: 1, OpID: "different-op",
				Obj: deterministicFolderID("different-op"), Name: "Other",
			}),
		})
		remote := newTestTelegramRemote(t, newProjectionDB(t), fakeTG, time.Unix(100, 0))
		request := mountwrite.CommitRequest{OperationID: "expected-op", CommitTime: time.Unix(100, 0), Mutation: mountwrite.Mutation{Kind: mountwrite.MutationMkdir, DriveID: testDriveID, DestinationName: "Expected"}}
		if _, _, err := remote.ReconcileReceipt(context.Background(), request, "92"); !errors.Is(err, mountwrite.ErrConflict) {
			t.Fatalf("ReconcileReceipt error = %v, want ErrConflict", err)
		}
	})
}

func TestTelegramRemoteRejectsReceiptWhoseControlDiffersFromJournal(t *testing.T) {
	db := newProjectionDB(t)
	fakeTG := tgclient.NewFake(1)
	actual := projection.Op{
		Type: projection.OpFolderCommit, ProtocolVersion: 1, OpID: "same-operation-id",
		Obj: deterministicFolderID("same-operation-id"), Name: "Tampered name",
	}
	fakeTG.SeedHistory(tgclient.HistoryMessage{MsgID: 93, FromID: 1, Text: projection.Format(actual)})
	remote := newTestTelegramRemote(t, db, fakeTG, time.Unix(100, 0))
	request := mountwrite.CommitRequest{
		OperationID: actual.OpID,
		CommitTime:  time.Unix(100, 0),
		Mutation: mountwrite.Mutation{
			Kind: mountwrite.MutationMkdir, DriveID: testDriveID, DestinationName: "Journal name",
		},
	}
	if _, _, err := remote.ReconcileReceipt(context.Background(), request, "93"); !errors.Is(err, mountwrite.ErrConflict) {
		t.Fatalf("ReconcileReceipt error = %v, want ErrConflict", err)
	}
	if _, found, err := projection.ProjectionOperationByID(db, testDriveID, actual.OpID); err != nil || found {
		t.Fatalf("projection found=%v err=%v, want rejected before projection", found, err)
	}
}

func TestTelegramRemoteIdempotentRetryRecoversAcceptedCommitOutsideHistoryWindow(t *testing.T) {
	firstDB := newProjectionDB(t)
	project(t, firstDB, 50, projection.Op{Type: projection.OpFilePart, UploadUUID: "crash-u", PartIndex: 0, FileSize: 1})
	if _, err := firstDB.Exec(`DROP TABLE replay_log`); err != nil {
		t.Fatal(err)
	}
	fakeTG := tgclient.NewFake(1)
	fakeTG.SeedChannel(tgclient.InputPeer{ChannelID: testDriveID}, "Personal")
	request := mountwrite.CommitRequest{
		OperationID: "crash-window-retry",
		CommitTime:  time.Unix(1234, 0).UTC(),
		Mutation: mountwrite.Mutation{
			Kind: mountwrite.MutationPut, DriveID: testDriveID,
			DestinationName: "recovered.txt", CreateOnly: true,
		},
		Body: &mountwrite.RemoteBody{UploadUUID: "crash-u", PartCount: 1, StoredSize: 1, PlaintextSize: 1},
	}
	first := newTestTelegramRemote(t, firstDB, fakeTG, time.Unix(1234, 0))
	accepted, err := first.Commit(context.Background(), request)
	if !errors.Is(err, mountwrite.ErrCommitOutcomeUnknown) || accepted.CommitRef == "" {
		t.Fatalf("first Commit = %+v, err=%v", accepted, err)
	}
	messages := make([]tgclient.HistoryMessage, 0, 500)
	for msgID := int64(101); msgID <= 600; msgID++ {
		messages = append(messages, tgclient.HistoryMessage{MsgID: msgID, FromID: 1, Text: "newer message"})
	}
	fakeTG.SeedHistory(messages...)

	recoveryDB := newProjectionDB(t)
	project(t, recoveryDB, 50, projection.Op{Type: projection.OpFilePart, UploadUUID: "crash-u", PartIndex: 0, FileSize: 1})
	recovery := newTestTelegramRemote(t, recoveryDB, fakeTG, time.Unix(9999, 0))
	persisted := ""
	request.PersistCommitRef = func(commitRef string) error {
		persisted = commitRef
		return nil
	}
	result, err := recovery.Commit(context.Background(), request)
	if err != nil || result.ObjectID != "f:100" || persisted != accepted.CommitRef || result.CommitRef != accepted.CommitRef {
		t.Fatalf("recovery Commit = %+v, persisted=%q, err=%v", result, persisted, err)
	}
	if len(fakeTG.SentControls()) != 1 {
		t.Fatalf("controls = %d, want one idempotent Telegram commit", len(fakeTG.SentControls()))
	}
	op, found, err := recovery.replayOp(testDriveID, 100)
	if err != nil || !found || op.FileUploadTime != request.CommitTime.Unix() {
		t.Fatalf("replayed op = %+v, found=%v, err=%v", op, found, err)
	}
}

func TestTelegramRemoteStopsAfterCommitReceiptPersistenceFailure(t *testing.T) {
	db := newProjectionDB(t)
	fakeTG := tgclient.NewFake(1)
	fakeTG.SeedChannel(tgclient.InputPeer{ChannelID: testDriveID}, "Personal")
	remote := newTestTelegramRemote(t, db, fakeTG, time.Unix(100, 0))
	request := mountwrite.CommitRequest{
		OperationID: "receipt-persist-failed",
		Mutation: mountwrite.Mutation{
			Kind: mountwrite.MutationMkdir, DriveID: testDriveID, DestinationName: "Pending",
		},
		PersistCommitRef: func(string) error { return errors.New("journal unavailable") },
	}
	result, err := remote.Commit(context.Background(), request)
	if !errors.Is(err, mountwrite.ErrCommitOutcomeUnknown) || result.CommitRef == "" {
		t.Fatalf("Commit = %+v, err=%v", result, err)
	}
	if _, found, lookupErr := projection.ProjectionOperationByID(db, testDriveID, request.OperationID); lookupErr != nil || found {
		t.Fatalf("projection found=%v err=%v, want no local projection", found, lookupErr)
	}
}

func TestTelegramRemoteCommitReturnsProjectedConflict(t *testing.T) {
	db := newProjectionDB(t)
	project(t, db, 10, projection.Op{Type: projection.OpFolderCommit, ProtocolVersion: 1, OpID: "existing", Obj: "d:existing", Name: "Taken"})
	fakeTG := tgclient.NewFake(1)
	fakeTG.SeedChannel(tgclient.InputPeer{ChannelID: testDriveID}, "Personal")
	remote := newTestTelegramRemote(t, db, fakeTG, time.Unix(100, 0))
	_, err := remote.Commit(context.Background(), mountwrite.CommitRequest{
		OperationID: "conflicting-mkdir",
		Mutation:    mountwrite.Mutation{Kind: mountwrite.MutationMkdir, DriveID: testDriveID, DestinationName: "taken"},
	})
	if !errors.Is(err, mountwrite.ErrConflict) {
		t.Fatalf("Commit error = %v, want projected conflict", err)
	}
}

func TestTelegramRemoteReconcilesMetadataObjectIdentity(t *testing.T) {
	db := newProjectionDB(t)
	project(t, db, 10, projection.Op{Type: projection.OpFolderCommit, ProtocolVersion: 1, OpID: "parent", Obj: "d:parent", Name: "Parent"})
	project(t, db, 20, projection.Op{Type: projection.OpFilePart, UploadUUID: "meta-u", PartIndex: 0, FileSize: 1})
	project(t, db, 21, projection.Op{Type: projection.OpFileCommit, ProtocolVersion: 1, OpID: "meta-file", Name: "Before", UploadUUID: "meta-u", PartCount: 1, FileSize: 1, PlaintextSize: 1})
	project(t, db, 22, projection.Op{
		Type: projection.OpRelocate, ProtocolVersion: 1, OpID: "meta-move",
		Obj: "f:21", Parent: "d:parent", Name: "After", ExpectedRevision: 1,
	})
	fakeTG := tgclient.NewFake(1)
	fakeTG.SeedChannel(tgclient.InputPeer{ChannelID: testDriveID}, "Personal")
	remote := newTestTelegramRemote(t, db, fakeTG, time.Unix(100, 0))
	result, found, err := remote.Reconcile(context.Background(), "meta-move")
	if err != nil || !found || result.ObjectID != "f:21" || result.Revision != 2 {
		t.Fatalf("metadata reconcile = %+v, found=%v, err=%v", result, found, err)
	}
}

func TestTelegramRemoteRetriesBoundedFloodWaitsForCommit(t *testing.T) {
	db := newProjectionDB(t)
	project(t, db, 50, projection.Op{Type: projection.OpFilePart, UploadUUID: "retry-u", PartIndex: 0, FileSize: 1})
	fakeTG := tgclient.NewFake(1)
	fakeTG.SeedChannel(tgclient.InputPeer{ChannelID: testDriveID}, "Personal")
	fakeTG.InjectFloodWaits(2)
	sleeps := 0
	remote, err := NewTelegramRemote(TelegramRemoteConfig{
		DB: db, DriveID: testDriveID, Files: &fakeHiddenStore{}, Telegram: fakeTG,
		Peers:   testPeerResolver{peer: tgclient.InputPeer{ChannelID: testDriveID}},
		ActorID: fakeTG.SelfID, Now: func() time.Time { return time.Unix(100, 0) },
		FloodWaitRetry: tgclient.FloodWaitRetryPolicy{
			MaxRetries: 2, MaxWait: time.Second, MaxTotalWait: 2 * time.Second,
			Sleep: func(context.Context, time.Duration) error { sleeps++; return nil },
		},
	})
	if err != nil {
		t.Fatalf("NewTelegramRemote: %v", err)
	}
	result, err := remote.Commit(context.Background(), mountwrite.CommitRequest{
		OperationID: "retry-commit",
		Mutation: mountwrite.Mutation{
			Kind: mountwrite.MutationPut, DriveID: testDriveID,
			DestinationName: "retry.txt", CreateOnly: true,
		},
		Body: &mountwrite.RemoteBody{UploadUUID: "retry-u", PartCount: 1, StoredSize: 1, PlaintextSize: 1},
	})
	if err != nil || result.ObjectID == "" || sleeps != 2 || len(fakeTG.SentControls()) != 1 {
		t.Fatalf("Commit = %+v, err=%v, sleeps=%d, controls=%d", result, err, sleeps, len(fakeTG.SentControls()))
	}
}

func TestTelegramRemoteStopsAfterFloodWaitRetryBudget(t *testing.T) {
	db := newProjectionDB(t)
	project(t, db, 50, projection.Op{Type: projection.OpFilePart, UploadUUID: "bounded-u", PartIndex: 0, FileSize: 1})
	fakeTG := tgclient.NewFake(1)
	fakeTG.SeedChannel(tgclient.InputPeer{ChannelID: testDriveID}, "Personal")
	fakeTG.InjectFloodWaits(3)
	sleeps := 0
	remote, err := NewTelegramRemote(TelegramRemoteConfig{
		DB: db, DriveID: testDriveID, Files: &fakeHiddenStore{}, Telegram: fakeTG,
		Peers:   testPeerResolver{peer: tgclient.InputPeer{ChannelID: testDriveID}},
		ActorID: fakeTG.SelfID, Now: func() time.Time { return time.Unix(100, 0) },
		FloodWaitRetry: tgclient.FloodWaitRetryPolicy{
			MaxRetries: 2, MaxWait: time.Second, MaxTotalWait: 2 * time.Second,
			Sleep: func(context.Context, time.Duration) error { sleeps++; return nil },
		},
	})
	if err != nil {
		t.Fatalf("NewTelegramRemote: %v", err)
	}
	_, err = remote.Commit(context.Background(), mountwrite.CommitRequest{
		OperationID: "bounded-commit",
		Mutation: mountwrite.Mutation{
			Kind: mountwrite.MutationPut, DriveID: testDriveID,
			DestinationName: "bounded.txt", CreateOnly: true,
		},
		Body: &mountwrite.RemoteBody{UploadUUID: "bounded-u", PartCount: 1, StoredSize: 1, PlaintextSize: 1},
	})
	if !errors.Is(err, mountwrite.ErrCommitOutcomeUnknown) || sleeps != 2 || len(fakeTG.SentControls()) != 0 {
		t.Fatalf("Commit err=%v, sleeps=%d, controls=%d", err, sleeps, len(fakeTG.SentControls()))
	}
}

func TestTelegramRemoteRetriesBoundedFloodWaitsWhileReconciling(t *testing.T) {
	db := newProjectionDB(t)
	project(t, db, 50, projection.Op{Type: projection.OpFilePart, UploadUUID: "history-retry-u", PartIndex: 0, FileSize: 1})
	fakeTG := tgclient.NewFake(1)
	fakeTG.SeedChannel(tgclient.InputPeer{ChannelID: testDriveID}, "Personal")
	fakeTG.SeedHistory(tgclient.HistoryMessage{MsgID: 51, FromID: 1, Text: projection.Format(projection.Op{
		Type: projection.OpFileCommit, ProtocolVersion: 1, OpID: "history-retry",
		Name: "history.txt", UploadUUID: "history-retry-u", PartCount: 1, FileSize: 1, PlaintextSize: 1,
	})})
	fakeTG.InjectReadFloodWaits(2)
	sleeps := 0
	remote, err := NewTelegramRemote(TelegramRemoteConfig{
		DB: db, DriveID: testDriveID, Files: &fakeHiddenStore{}, Telegram: fakeTG,
		Peers:   testPeerResolver{peer: tgclient.InputPeer{ChannelID: testDriveID}},
		ActorID: fakeTG.SelfID, Now: func() time.Time { return time.Unix(100, 0) },
		FloodWaitRetry: tgclient.FloodWaitRetryPolicy{
			MaxRetries: 2, MaxWait: time.Second, MaxTotalWait: 2 * time.Second,
			Sleep: func(context.Context, time.Duration) error { sleeps++; return nil },
		},
	})
	if err != nil {
		t.Fatalf("NewTelegramRemote: %v", err)
	}
	result, found, err := remote.Reconcile(context.Background(), "history-retry")
	if err != nil || !found || result.ObjectID != "f:51" || sleeps != 2 {
		t.Fatalf("Reconcile = %+v, found=%v, err=%v, sleeps=%d", result, found, err, sleeps)
	}
}

func TestTelegramRemoteCommitRequiresConfirmedAppliedProjection(t *testing.T) {
	db := newProjectionDB(t)
	project(t, db, 100, projection.Op{
		Type: projection.OpFilePart, UploadUUID: "occupied-message", PartIndex: 0, FileSize: 1,
	})
	fakeTG := tgclient.NewFake(1)
	fakeTG.SeedChannel(tgclient.InputPeer{ChannelID: testDriveID}, "Personal")
	remote := newTestTelegramRemote(t, db, fakeTG, time.Unix(100, 0))

	_, err := remote.Commit(context.Background(), mountwrite.CommitRequest{
		OperationID: "unconfirmed-mkdir",
		Mutation: mountwrite.Mutation{
			Kind: mountwrite.MutationMkdir, DriveID: testDriveID, DestinationName: "Unconfirmed",
		},
	})
	if !errors.Is(err, mountwrite.ErrCommitOutcomeUnknown) {
		t.Fatalf("Commit error = %v, want ErrCommitOutcomeUnknown", err)
	}
	if _, found, lookupErr := projection.ProjectionOperationByID(db, testDriveID, "unconfirmed-mkdir"); lookupErr != nil || found {
		t.Fatalf("projection operation found=%v, err=%v", found, lookupErr)
	}
}

func TestTelegramRemoteReconcileUsesOldestControlAndTelegramActor(t *testing.T) {
	db := newProjectionDB(t)
	project(t, db, 50, projection.Op{Type: projection.OpFilePart, UploadUUID: "first-wins-u", PartIndex: 0, FileSize: 1})
	op := projection.Op{
		Type: projection.OpFileCommit, ProtocolVersion: 1, OpID: "first-wins-op",
		Name: "first.txt", UploadUUID: "first-wins-u", PartCount: 1, FileSize: 1, PlaintextSize: 1,
	}
	fakeTG := tgclient.NewFake(1)
	fakeTG.SeedChannel(tgclient.InputPeer{ChannelID: testDriveID}, "Personal")
	fakeTG.SeedHistory(
		tgclient.HistoryMessage{MsgID: 51, FromID: 77, Text: projection.Format(op)},
		tgclient.HistoryMessage{MsgID: 52, FromID: 88, Text: projection.Format(op)},
	)
	remote := newTestTelegramRemote(t, db, fakeTG, time.Unix(100, 0))

	result, found, err := remote.Reconcile(context.Background(), op.OpID)
	if err != nil || !found || result.ObjectID != "f:51" {
		t.Fatalf("Reconcile = %+v, found=%v, err=%v", result, found, err)
	}
	file, found, err := projection.FileByID(db, testDriveID, 51)
	if err != nil || !found || file.UploaderUserID != 77 {
		t.Fatalf("projected file = %+v, found=%v, err=%v", file, found, err)
	}
}

type fakeHiddenStore struct {
	uploadRequest    fileservice.HiddenUploadRequest
	uploadResult     fileservice.HiddenBody
	uploadErr        error
	recoveryRequest  fileservice.HiddenUploadRequest
	recoveryResult   fileservice.HiddenBody
	recoveryPayload  []byte
	discardBody      fileservice.HiddenBody
	discardOperation string
}

func (s *fakeHiddenStore) UploadHidden(_ context.Context, _ int64, request fileservice.HiddenUploadRequest, source io.ReadSeeker) (fileservice.HiddenBody, error) {
	s.uploadRequest = request
	_, _ = io.Copy(io.Discard, source)
	return s.uploadResult, s.uploadErr
}

func (s *fakeHiddenStore) RecoverHiddenUpload(_ context.Context, _ int64, request fileservice.HiddenUploadRequest, source io.ReadSeeker) (fileservice.HiddenBody, error) {
	s.recoveryRequest = request
	s.recoveryPayload, _ = io.ReadAll(source)
	return s.recoveryResult, nil
}

func (s *fakeHiddenStore) DiscardHiddenReceipt(_ context.Context, _ int64, _ string, body fileservice.HiddenBody) error {
	s.discardBody = body
	return nil
}

func (s *fakeHiddenStore) DiscardHiddenOperation(_ context.Context, _ int64, operationID string) error {
	s.discardOperation = operationID
	return nil
}

type testPeerResolver struct{ peer tgclient.InputPeer }

func (r testPeerResolver) ResolvePeer(context.Context, int64) (tgclient.InputPeer, error) {
	return r.peer, nil
}

func newTestTelegramRemote(t *testing.T, db *sql.DB, fakeTG *tgclient.Fake, now time.Time) *TelegramRemote {
	t.Helper()
	remote, err := NewTelegramRemote(TelegramRemoteConfig{
		DB: db, DriveID: testDriveID, Files: &fakeHiddenStore{}, Telegram: fakeTG,
		Peers:   testPeerResolver{peer: tgclient.InputPeer{ChannelID: testDriveID}},
		ActorID: fakeTG.SelfID, Now: func() time.Time { return now },
		HistoryPageSize: 10, HistoryPages: 3,
	})
	if err != nil {
		t.Fatalf("NewTelegramRemote: %v", err)
	}
	return remote
}

func projectRaw(t *testing.T, db *sql.DB, msgID int64, op projection.Op) {
	t.Helper()
	if _, err := projection.ProjectFromOp(db, testDriveID, msgID, op, 1, projection.Format(op)); err != nil {
		t.Fatalf("ProjectFromOp: %v", err)
	}
}
